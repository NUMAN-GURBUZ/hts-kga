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
	Duration      time.Duration
}

// Run, doğrulama hattını yürütür.
func (p *Pipeline) Run(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	report := Report{RunID: opts.RunID, Scenario: opts.Scenario}

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

	report.Duration = time.Since(started)
	return report, nil
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
	return out
}
