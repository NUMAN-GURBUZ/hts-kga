// Package integrity, bütünlük tespitinin precision/recall ölçümüdür
// (T-E04-09, F.5, ADR-09, ADR-28, ADR-31).
//
// # Bu paket neden doğrulama servisinde
//
// F.5 `ground_truth.injected_rule` etiketine bakar. O etiketi yalnızca S3b
// görebilir: `svc_validation` rolünün `ground_truth` üzerinde SELECT yetkisi
// vardır, `svc_integrity`'nin yoktur (migration 004). Ölçüm bu yüzden
// dedektörün değil doğrulamanın işidir — kör test böyle korunur.
//
// # Kanonik ölçüm nedir
//
// ADR-28: motor tüm isabetleri yazar, öncelik yarışını kaybedenler
// `suppressed_by` ile işaretlenir. K7'nin precision'ı **kanonik** bulgular
// üzerinde hesaplanır (`suppressed_by IS NULL`); bu süzgeç ölçümden önce beyan
// edilmiştir. Karışıklık matrisi ise tüm satırları kullanır — bastırma bir
// etikettir, filtre değil.
//
// # Planın F.5 sorgusundaki düzeltme
//
// Plan `JOIN ground_truth` yazıyor. `LEFT JOIN` olmalı: `ground_truth`'ta eşi
// olmayan bir bulgu iç birleştirmede **denominatörden düşer** ve precision
// sessizce yükselir. Yanlış pozitifi ölçümün dışına atan bir precision formülü,
// ölçmediği şeyi ölçüyor sanır (ADR-31/6).
//
// # Bölüm anahtarı süzgeci uygulanmaz
//
// Bütünlük tespitinde kalibre edilen bir şey yoktur; K4 enforcer'ının tip
// zorunluluğu bu hatta taşınmaz. Taşınsaydı recall denominatörü %20'ye inerdi.
package integrity

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// Row, bir kuralın K7 ölçümüdür (`integrity_metrics` satırı).
type Row struct {
	RuleID   integrityrule.ID
	RuleName string
	Scenario string

	// Findings, kanonik bulgu sayısıdır (precision denominatörü).
	Findings int64
	// TruePositives, kuralın doğru kuralı işaret ettiği bulgu sayısıdır.
	TruePositives int64
	// Injected, bu kuralla enjekte edilmiş olay sayısıdır (recall denominatörü).
	Injected int64

	// Precision nil ise ölçülemedi: kanonik bulgu yok, ya da kural olay-çıpalı
	// değil (ADR-30: kural 4 daima nil).
	Precision *float64
	// Recall daima ölçülür; enjekte olay yoksa 0.
	Recall float64
	// PrecisionLo/Hi, Wilson %95 güven aralığıdır.
	PrecisionLo *float64
	PrecisionHi *float64
	// Sufficient, K7 eşiğinin uygulanabilir olup olmadığıdır (ADR-31/9).
	Sufficient bool
}

// MeetsK7, kuralın K7 eşiğini geçtiğini bildirir.
//
// Üç durumun ayrımı önemlidir ve ADR-31/9'da ölçümden önce beyan edilmiştir:
//
//	precision nil            → "ölçülemedi": ne geçti ne kaldı
//	sufficient false         → "istatistiksel olarak yetersiz": geçmiş sayılmaz
//	precision >= threshold   → geçti
func (r Row) MeetsK7(threshold float64) bool {
	return r.Precision != nil && r.Sufficient && *r.Precision >= threshold
}

// Verdict, kuralın K7 durumunu okunabilir biçimde döndürür.
func (r Row) Verdict(threshold float64) string {
	switch {
	case !r.RuleID.EventAnchored():
		return "kapsam dışı (ADR-30)"
	case r.Precision == nil:
		return "ölçülemedi (kanonik bulgu yok)"
	case !r.Sufficient:
		return fmt.Sprintf("yetersiz (%d bulgu)", r.Findings)
	case *r.Precision >= threshold:
		return "geçti"
	default:
		return "tutmadı"
	}
}

// ConfusionCell, karışıklık matrisinin bir gözüdür.
//
// Satır = tespit eden kural, sütun = gerçek enjeksiyon kuralı (nil = temiz).
type ConfusionCell struct {
	DetectedBy integrityrule.ID
	// InjectedRule nil ise kayıt temizdi (yanlış pozitif).
	InjectedRule *integrityrule.ID
	// Canonical, bu gözün kanonik bulgu sayısıdır.
	Canonical int64
	// Suppressed, öncelik nedeniyle bastırılmış bulgu sayısıdır.
	Suppressed int64
}

// Report, bir koşunun K7 ölçümüdür.
type Report struct {
	RunID     uuid.UUID
	Scenario  string
	Rows      []Row
	Confusion []ConfusionCell
	// Threshold, K7 eşiğidir (%90).
	Threshold float64
	// MinFindings, ölçülebilirlik eşiğidir (ADR-31/9).
	MinFindings int
}

// K7Threshold, plan BÖLÜM J'deki eşiktir. Ölçümden önce beyan edilmiştir ve
// sonuca göre değiştirilmez.
const K7Threshold = 0.90

// Options, ölçümün parametreleridir.
type Options struct {
	RunID uuid.UUID
	// MinFindings, K7 eşiğinin uygulanabilmesi için gereken asgari kanonik
	// bulgu sayısıdır (integrity.detection.min_findings_for_threshold).
	MinFindings int
}

// Compute, F.5'i koşturur ve K7 raporunu üretir.
//
// Ölçüm **bir kez** hesaplanır ve sonucu ne olursa raporlanır (Sprint 6 planı,
// beyan-sonra-ölç disiplini).
func Compute(ctx context.Context, db *pgxpool.Pool, opts Options) (Report, error) {
	if db == nil {
		return Report{}, fmt.Errorf("F.5: veritabanı bağlantısı zorunlu")
	}
	if opts.RunID == uuid.Nil {
		return Report{}, fmt.Errorf("F.5: run_id zorunlu (ADR-05)")
	}
	if opts.MinFindings < 1 {
		opts.MinFindings = 30 // ADR-31/9
	}

	scenario, err := scenarioOf(ctx, db, opts.RunID)
	if err != nil {
		return Report{}, err
	}

	precision, err := precisionByRule(ctx, db, opts.RunID)
	if err != nil {
		return Report{}, err
	}
	recall, err := recallByRule(ctx, db, opts.RunID)
	if err != nil {
		return Report{}, err
	}
	confusion, err := confusionMatrix(ctx, db, opts.RunID)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		RunID:       opts.RunID,
		Scenario:    scenario,
		Confusion:   confusion,
		Threshold:   K7Threshold,
		MinFindings: opts.MinFindings,
	}

	// Beş kuralın hepsi için satır üretilir — bulgu üretmeyen kural da
	// raporlanır. Eksik satır "ölçülmedi" ile "sıfır bulgu" ayrımını
	// kaybettirirdi.
	for _, id := range integrityrule.ByID {
		row := Row{
			RuleID:   id,
			RuleName: id.Name(),
			Scenario: scenario,
			Injected: recall[id].injected,
			Findings: precision[id].findings,
		}
		row.TruePositives = precision[id].truePositives

		if row.Injected > 0 {
			row.Recall = float64(recall[id].caught) / float64(row.Injected)
		}

		// ADR-30: kural 4 olay-çıpalı değil; precision tanımsız.
		if id.EventAnchored() && row.Findings > 0 {
			p := float64(row.TruePositives) / float64(row.Findings)
			row.Precision = &p
			lo, hi := WilsonInterval(row.TruePositives, row.Findings)
			row.PrecisionLo, row.PrecisionHi = &lo, &hi
			row.Sufficient = row.Findings >= int64(opts.MinFindings)
		}
		report.Rows = append(report.Rows, row)
	}
	return report, nil
}

// ─── SQL ──────────────────────────────────────────────────────────────────────

// scenarioOf, koşunun senaryosunu okur.
func scenarioOf(ctx context.Context, db *pgxpool.Pool, runID uuid.UUID) (string, error) {
	var scenario string
	err := db.QueryRow(ctx,
		`SELECT scenario FROM run_config WHERE run_id = $1::uuid`, runID.String()).Scan(&scenario)
	if err != nil {
		return "", fmt.Errorf("F.5: koşu senaryosu okunamadı (%s): %w", runID, err)
	}
	return scenario, nil
}

type precisionCount struct {
	findings      int64
	truePositives int64
}

// precisionSQL, F.5'in düzeltilmiş precision sorgusudur (ADR-31/6).
//
// İki fark plandan:
//
//	LEFT JOIN               → eşi olmayan bulgu denominatörde KALIR
//	suppressed_by IS NULL   → kanonik ölçüm (ADR-28/4)
const precisionSQL = `
SELECT f.rule_id,
       count(*)                                             AS findings,
       count(*) FILTER (WHERE g.injected_rule = f.rule_id)   AS true_positives
FROM integrity_findings f
LEFT JOIN ground_truth g
       ON g.run_id = f.run_id AND g.event_id = f.event_id
WHERE f.run_id = $1::uuid
  AND f.suppressed_by IS NULL
GROUP BY f.rule_id`

func precisionByRule(ctx context.Context, db *pgxpool.Pool, runID uuid.UUID) (map[integrityrule.ID]precisionCount, error) {
	rows, err := db.Query(ctx, precisionSQL, runID.String())
	if err != nil {
		return nil, fmt.Errorf("F.5 precision sorgusu başarısız (%s): %w", runID, err)
	}
	defer rows.Close()

	out := make(map[integrityrule.ID]precisionCount)
	for rows.Next() {
		var ruleID int
		var c precisionCount
		if err := rows.Scan(&ruleID, &c.findings, &c.truePositives); err != nil {
			return nil, fmt.Errorf("F.5 precision satırı okunamadı: %w", err)
		}
		id, err := integrityrule.Parse(ruleID)
		if err != nil {
			return nil, fmt.Errorf("F.5 precision: %w", err)
		}
		out[id] = c
	}
	return out, rows.Err()
}

type recallCount struct {
	injected int64
	caught   int64
}

// recallSQL, F.5'in recall sorgusudur.
//
// Kanonik süzgeç birleştirme koşulunda durur (WHERE'de değil): bastırılmış bir
// bulgu "yakalandı" saymamalı, ama enjekte olay recall denominatöründe kalmalı.
const recallSQL = `
SELECT g.injected_rule,
       count(*)                                          AS injected,
       count(*) FILTER (WHERE f.event_id IS NOT NULL)     AS caught
FROM ground_truth g
LEFT JOIN integrity_findings f
       ON f.run_id = g.run_id
      AND f.event_id = g.event_id
      AND f.rule_id = g.injected_rule
      AND f.suppressed_by IS NULL
WHERE g.run_id = $1::uuid
  AND g.injected_rule IS NOT NULL
GROUP BY g.injected_rule`

func recallByRule(ctx context.Context, db *pgxpool.Pool, runID uuid.UUID) (map[integrityrule.ID]recallCount, error) {
	rows, err := db.Query(ctx, recallSQL, runID.String())
	if err != nil {
		return nil, fmt.Errorf("F.5 recall sorgusu başarısız (%s): %w", runID, err)
	}
	defer rows.Close()

	out := make(map[integrityrule.ID]recallCount)
	for rows.Next() {
		var ruleID int
		var c recallCount
		if err := rows.Scan(&ruleID, &c.injected, &c.caught); err != nil {
			return nil, fmt.Errorf("F.5 recall satırı okunamadı: %w", err)
		}
		id, err := integrityrule.Parse(ruleID)
		if err != nil {
			return nil, fmt.Errorf("F.5 recall: %w", err)
		}
		out[id] = c
	}
	return out, rows.Err()
}

// confusionSQL, 5×6 karışıklık matrisini üretir (5 kural × 5 enjeksiyon + temiz).
//
// Bastırılmış satırlar **dâhildir**: hangi kuralın hangi enjeksiyonu gördüğü
// bu çalışmanın en ilginç bulgularından biridir (kaydırılmış damga kinematik
// anomali de üretir) ve bastırma onu gizlememelidir (ADR-28/4).
const confusionSQL = `
SELECT f.rule_id,
       g.injected_rule,
       count(*) FILTER (WHERE f.suppressed_by IS NULL)     AS canonical,
       count(*) FILTER (WHERE f.suppressed_by IS NOT NULL) AS suppressed
FROM integrity_findings f
LEFT JOIN ground_truth g
       ON g.run_id = f.run_id AND g.event_id = f.event_id
WHERE f.run_id = $1::uuid
GROUP BY f.rule_id, g.injected_rule`

func confusionMatrix(ctx context.Context, db *pgxpool.Pool, runID uuid.UUID) ([]ConfusionCell, error) {
	rows, err := db.Query(ctx, confusionSQL, runID.String())
	if err != nil {
		return nil, fmt.Errorf("F.5 karışıklık matrisi başarısız (%s): %w", runID, err)
	}
	defer rows.Close()

	var out []ConfusionCell
	for rows.Next() {
		var detectedBy int
		var injected *int
		var cell ConfusionCell
		if err := rows.Scan(&detectedBy, &injected, &cell.Canonical, &cell.Suppressed); err != nil {
			return nil, fmt.Errorf("F.5 matris satırı okunamadı: %w", err)
		}
		id, err := integrityrule.Parse(detectedBy)
		if err != nil {
			return nil, fmt.Errorf("F.5 matris: %w", err)
		}
		cell.DetectedBy = id
		if injected != nil {
			injectedID, err := integrityrule.Parse(*injected)
			if err != nil {
				return nil, fmt.Errorf("F.5 matris (enjeksiyon etiketi): %w", err)
			}
			cell.InjectedRule = &injectedID
		}
		out = append(out, cell)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Deterministik sıra: rapor koşudan koşuya aynı görünmeli.
	sort.Slice(out, func(i, j int) bool {
		if out[i].DetectedBy != out[j].DetectedBy {
			return out[i].DetectedBy < out[j].DetectedBy
		}
		return injectedKey(out[i].InjectedRule) < injectedKey(out[j].InjectedRule)
	})
	return out, nil
}

// injectedKey, sıralama için enjeksiyon etiketini sayıya çevirir (temiz = 0).
func injectedKey(id *integrityrule.ID) int {
	if id == nil {
		return 0
	}
	return int(*id)
}

// ─── Wilson aralığı (ADR-31/9) ────────────────────────────────────────────────

// wilsonZ, %95 iki taraflı güven düzeyinin z değeridir.
const wilsonZ = 1.959963984540054

// WilsonInterval, oran için Wilson %95 güven aralığını döndürür.
//
// # Neden Wilson, normal yaklaşım değil
//
// Küçük örneklemde ve uç oranlarda (p ≈ 1) normal yaklaşım çöker: 3/3 için
// aralık [1, 1] çıkar ve "kesinlikle %100" der. Wilson iki durumda da
// davranışlıdır ve K7'nin en yakıcı sorusunu dürüstçe cevaplar: "bu kural 3
// bulguyla %100 precision verdi — bu ne kadar bilgi?"
//
// Ön analizde katı üçlü sınama kural 2 için 1–7 bulgu üretiyordu; ADR-31/9'un
// 30 bulgu eşiği ve bu aralık, o tasarımın "%100 precision" beyanını ölçümden
// önce engellemek için konulmuştur.
func WilsonInterval(successes, trials int64) (lo, hi float64) {
	if trials <= 0 {
		return 0, 0
	}
	n := float64(trials)
	p := float64(successes) / n
	z2 := wilsonZ * wilsonZ

	denom := 1 + z2/n
	centre := p + z2/(2*n)
	spread := wilsonZ * math.Sqrt(p*(1-p)/n+z2/(4*n*n))

	lo = (centre - spread) / denom
	hi = (centre + spread) / denom
	return math.Max(0, lo), math.Min(1, hi)
}
