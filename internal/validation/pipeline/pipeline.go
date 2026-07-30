// Package pipeline, doğrulama toplu işidir — S3b (T-E04-02, ADR-04).
//
// Aşamalar sırayla yürür ve her aşama bir öncekinin çıktısına dayanır:
//
//	1. Önkoşul       koşu gerçekten bitti mi (ADR-04, ADR-23)
//	2. Kalibrasyon   λ* bulunur ve run_config'e yazılır (ADR-02) — opsiyonel
//	3. Ölçüm         F.1–F.3, yalnızca 'V' kümesinde (K4)
//	4. Karşılaştırma F.4 daralma → M@90 satırına eklenir (K2, K3)
//	5. Yazım         metrics tablosu
//
// # Neden toplu iş
//
// ADR-04: doğrulama gerçek zamanlı olmak zorunda değil. Akış tabanlı bir
// doğrulama, "analiz bitti mi" sorusunu her olay için yeniden sormak
// demekti; toplu iş bunu bir kez, başta sorar.
//
// # Kalibrasyon neden ölçümden önce
//
// λ, kütle üretiminin genişliğini belirler; ölçüm λ* ile yapılmalıdır.
// Kalibrasyon 'C' kümesinde, ölçüm 'V' kümesinde koşar — K4'ün ayrımı budur.

package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/comparison"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/integrity"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/metrics"
)

// Pipeline, doğrulama aşamalarını yürütür.
type Pipeline struct {
	pool *postgres.Pool
	log  *slog.Logger
}

// New, hattı kurar.
func New(pool *postgres.Pool, log *slog.Logger) (*Pipeline, error) {
	if pool == nil {
		return nil, fmt.Errorf("doğrulama hattı: veritabanı bağlantısı zorunlu")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Pipeline{pool: pool, log: log}, nil
}

// Options, bir doğrulama koşusunun parametreleridir.
type Options struct {
	// RunID, doğrulanacak koşudur.
	RunID uuid.UUID
	// Scenario, senaryo harfidir (metrics.scenario sütunu).
	Scenario string
	// IncludeCalibrationMetrics true ise 'C' kümesi için de ölçüm yazılır.
	//
	// Varsayılan false: 'C' ölçümü **bilgi amaçlıdır** ve K1–K3'e girmez.
	// Yanlışlıkla rapora karışmaması için ayrıca istenmelidir (K4).
	IncludeCalibrationMetrics bool
	// Recompute true ise koşunun mevcut ölçümleri silinip yeniden yazılır.
	Recompute bool
	// IntegrityMinFindings, K7 ölçülebilirlik eşiğidir (ADR-31/9).
	//
	// 0 ise F.5 **atlanır**: bütünlük ölçümü yalnızca açıkça istendiğinde
	// koşar, çünkü `integrity_findings` boşsa (S4 koşmadıysa) tüm kurallar
	// "ölçülemedi" satırı yazar ve bu rapora gürültü katar.
	IntegrityMinFindings int
	// IntegrityOnly true ise yalnızca F.5 koşar; K1–K3 hattı atlanır.
	//
	// # Neden ayrı bir mod gerekiyor
	//
	// K1–K3'ün önkoşulu "analiz tamamlandı"dır (`analyzed_events` yazılı,
	// ADR-23). F.5 ise `estimates` tablosuna **hiç dokunmaz**: bütünlük
	// denetimi kütle modelinden, kontur çıkarımından ve λ'dan bağımsızdır.
	// Analiz önkoşulunu F.5'e uygulamak, K7'yi ölçmek için gereksiz saatler
	// harcamak demek olurdu.
	//
	// F.5'in kendi önkoşulu vardır ve o denetlenir: bütünlük fazları koştu mu
	// (`inspected_records` yazılı) ve ground truth yazıldı mı.
	IntegrityOnly bool
}

// Report, doğrulama koşusunun çıktısıdır.
type Report struct {
	RunID         uuid.UUID
	Scenario      string
	Preconditions []Precondition
	Lambda        *float64
	Validation    []metrics.Row
	Calibration   []metrics.Row
	Reduction     comparison.Result
	EqualSets     bool
	// Integrity, K7 ölçümüdür (F.5). IntegrityMinFindings 0 ise nil.
	Integrity *integrity.Report
	Duration  time.Duration
}

// Run, doğrulama hattını yürütür.
func (p *Pipeline) Run(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	report := Report{RunID: opts.RunID, Scenario: opts.Scenario}

	// ─── 0. Yalnızca F.5 modu ────────────────────────────────────────────────
	if opts.IntegrityOnly {
		if opts.IntegrityMinFindings <= 0 {
			return report, fmt.Errorf(
				"yalnızca-bütünlük modu: IntegrityMinFindings zorunlu (ADR-31/9)")
		}
		if err := p.checkIntegrityPrecondition(ctx, opts.RunID); err != nil {
			return report, err
		}
		if err := p.runIntegrity(ctx, &report, opts); err != nil {
			return report, err
		}
		report.Duration = time.Since(started)
		return report, nil
	}

	// ─── 1. Önkoşul ──────────────────────────────────────────────────────────
	checks, err := CheckPreconditions(ctx, p.pool, opts.RunID)
	report.Preconditions = checks
	if err != nil {
		return report, err
	}
	for _, c := range checks {
		p.log.Info("önkoşul", "denetim", c.Name, "sonuç", "geçti", "ayrıntı", c.Detail)
	}

	status, err := p.pool.RunStatusOf(ctx, opts.RunID)
	if err != nil {
		return report, fmt.Errorf("doğrulama: koşu durumu: %w", err)
	}
	report.Lambda = status.Lambda
	if status.Lambda == nil {
		p.log.Warn("koşu kalibre edilmemiş: metrikler λ=1 varsayımıyla üretilmiş " +
			"tahminler üzerinde hesaplanıyor (ADR-02)")
	}

	db := p.pool.Querier()

	if opts.Recompute {
		if err := metrics.DeleteRun(ctx, db, opts.RunID); err != nil {
			return report, err
		}
	}

	// ─── 3. Ölçüm — yalnızca 'V' (K4) ────────────────────────────────────────
	validation, err := metrics.Compute(ctx, db, opts.RunID, opts.Scenario, metrics.Validation)
	if err != nil {
		return report, err
	}

	// ─── 4. Karşılaştırma — F.4 (K2, K3) ─────────────────────────────────────
	equal, err := comparison.EqualEventSets(ctx, db, opts.RunID)
	if err != nil {
		return report, err
	}
	report.EqualSets = equal
	if !equal {
		p.log.Warn("yöntemler farklı olay kümelerinde ölçüldü — " +
			"medyan alan karşılaştırması yanlı olabilir")
	}

	reduction, err := comparison.Reductions(ctx, db, opts.RunID)
	if err != nil {
		return report, err
	}
	report.Reduction = reduction

	// Daralma yalnızca M@90 satırına yazılır: taban çizgileri kendileriyle
	// karşılaştırılmaz, M@50/M@95 ise plan F.4'ün karşılaştırma seviyesi
	// değildir.
	for i := range validation {
		if validation[i].Method == "M" && validation[i].Confidence == comparison.ModelConfidence {
			b0, b1 := reduction.ReductionVsB0, reduction.ReductionVsB1
			validation[i].ReductionVsB0 = &b0
			validation[i].ReductionVsB1 = &b1
		}
	}
	report.Validation = validation

	// ─── 5. Yazım ────────────────────────────────────────────────────────────
	n, err := metrics.Write(ctx, db, opts.RunID, validation)
	if err != nil {
		return report, err
	}
	p.log.Info("doğrulama ölçümü yazıldı", "küme", "V", "satır", n)

	if opts.IncludeCalibrationMetrics {
		calib, err := metrics.Compute(ctx, db, opts.RunID, opts.Scenario, metrics.Calibration)
		if err != nil {
			return report, err
		}
		if _, err := metrics.Write(ctx, db, opts.RunID, calib); err != nil {
			return report, err
		}
		report.Calibration = calib
		p.log.Info("kalibrasyon kümesi ölçümü yazıldı (bilgi amaçlı, K1–K3'e girmez)",
			"satır", len(calib))
	}

	// ─── 6. Bütünlük ölçümü — F.5 (K7) ───────────────────────────────────────
	//
	// Etiketi (`ground_truth.injected_rule`) yalnızca bu servis görebilir:
	// `svc_validation` rolünün SELECT yetkisi vardır, `svc_integrity`'nin
	// yoktur (ADR-09 katman 2). Ölçüm bu yüzden dedektörün değil doğrulamanın
	// işidir.
	if opts.IntegrityMinFindings > 0 {
		if err := p.runIntegrity(ctx, &report, opts); err != nil {
			return report, err
		}
	}

	report.Duration = time.Since(started)
	return report, nil
}

// runIntegrity, F.5'i hesaplar ve `integrity_metrics` tablosuna yazar.
func (p *Pipeline) runIntegrity(ctx context.Context, report *Report, opts Options) error {
	k7, err := integrity.Compute(ctx, p.pool.Querier(), integrity.Options{
		RunID:       opts.RunID,
		MinFindings: opts.IntegrityMinFindings,
	})
	if err != nil {
		return err
	}
	if err := integrity.Write(ctx, p.pool.Querier(), k7); err != nil {
		return err
	}
	report.Integrity = &k7
	report.Scenario = k7.Scenario
	p.log.Info("bütünlük ölçümü yazıldı (F.5)",
		"kural", len(k7.Rows), "özet", k7.K7Verdict())
	return nil
}

// checkIntegrityPrecondition, F.5'in kendi önkoşulunu denetler.
//
// K1–K3'ün "analiz tamamlandı" önkoşulundan farklıdır ve olması gereken de bu:
// F.5 `estimates`'e dokunmaz. Denetlenen iki şey:
//
//	(a) ground_truth yazıldı  → etiket olmadan precision/recall ölçülemez
//	(b) inspected_records yazılı → bütünlük akış fazı koştu (ADR-31/7)
//
// (b) sağlanmazsa `integrity_findings` boş ya da eksiktir ve ölçüm düşük recall
// verir; bu bir bulgu gibi görünürdü.
func (p *Pipeline) checkIntegrityPrecondition(ctx context.Context, runID uuid.UUID) error {
	truths, err := p.pool.CountGroundTruth(ctx, runID)
	if err != nil {
		return fmt.Errorf("F.5 önkoşulu: %w", err)
	}
	if truths == 0 {
		return fmt.Errorf(
			"F.5 önkoşulu: koşu %s için ground_truth boş — enjeksiyon etiketi "+
				"olmadan precision/recall ölçülemez (ADR-09)", runID)
	}

	status, err := p.pool.RunStatusOf(ctx, runID)
	if err != nil {
		return fmt.Errorf("F.5 önkoşulu: %w", err)
	}
	if status.InspectedRecords == nil {
		return fmt.Errorf(
			"F.5 önkoşulu: koşu %s için inspected_records yazılmamış — bütünlük "+
				"akış fazı koşmadı; ölçüm boş bulgu kümesi üzerinde yapılırdı (ADR-31/7)",
			runID)
	}
	if status.PublishedEvents != nil && *status.InspectedRecords != *status.PublishedEvents {
		return fmt.Errorf(
			"F.5 önkoşulu: bütünlük akış fazı yarım kaldı (%d/%d incelendi) — "+
				"düşük recall bilimsel bulgu gibi görünürdü (ADR-31/7)",
			*status.InspectedRecords, *status.PublishedEvents)
	}

	p.log.Info("F.5 önkoşulu geçti",
		"ground_truth", truths, "incelenen_kayıt", *status.InspectedRecords)
	return nil
}

// Summary, raporun okunabilir özetidir.
func (r Report) Summary() string {
	out := fmt.Sprintf("koşu %s (senaryo %s)\n", r.RunID, r.Scenario)
	if r.Lambda != nil {
		out += fmt.Sprintf("  λ* = %.4f\n", *r.Lambda)
	} else {
		out += "  λ* = yok (kalibre edilmemiş)\n"
	}

	for _, row := range r.Validation {
		out += fmt.Sprintf("  %-7s n=%-6d kapsama=%.4f  medyan alan=%.4f km²  "+
			"r50=%.0f m  r95=%.0f m  p95_parça=%d\n",
			row.Label(), row.NEvents, row.CoverageRate, row.MedianAreaKM2,
			row.R50M, row.R95M, row.P95PartCount)
	}

	out += fmt.Sprintf("  daralma: M@90 vs B0 %%%.1f (K2 ≥ %%75) · vs B1 %%%.1f (K3)\n",
		r.Reduction.ReductionVsB0*100, r.Reduction.ReductionVsB1*100)

	if r.Integrity != nil {
		out += "  " + r.Integrity.K7Verdict() + "\n"
	}
	return out
}
