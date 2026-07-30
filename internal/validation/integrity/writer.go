// K7 ölçümünün `integrity_metrics` tablosuna yazımı ve rapor biçimlendirmesi
// (ADR-31/8).

package integrity

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// upsertSQL, ölçüm satırlarını yazar.
//
// `ON CONFLICT (run_id, rule_id) DO UPDATE`: yeniden hesaplama idempotenttir.
// Bulgu tablosu ekle-yalnızdır ama **ölçüm** tablosu değil — ölçüm bulgulardan
// türetilir ve yeniden türetilmesi bilgi kaybetmez.
const upsertSQL = `
INSERT INTO integrity_metrics (
    run_id, scenario, rule_id, rule_name,
    findings, true_positives, injected,
    precision, recall, precision_lo, precision_hi, sufficient
) VALUES (
    $1::uuid, $2, $3, $4,
    $5, $6, $7,
    $8, $9, $10, $11, $12
)
ON CONFLICT (run_id, rule_id) DO UPDATE SET
    computed_at    = now(),
    scenario       = EXCLUDED.scenario,
    rule_name      = EXCLUDED.rule_name,
    findings       = EXCLUDED.findings,
    true_positives = EXCLUDED.true_positives,
    injected       = EXCLUDED.injected,
    precision      = EXCLUDED.precision,
    recall         = EXCLUDED.recall,
    precision_lo   = EXCLUDED.precision_lo,
    precision_hi   = EXCLUDED.precision_hi,
    sufficient     = EXCLUDED.sufficient`

// Write, raporu `integrity_metrics` tablosuna yazar.
func Write(ctx context.Context, db *pgxpool.Pool, r Report) error {
	if db == nil {
		return fmt.Errorf("K7 ölçümü: veritabanı bağlantısı zorunlu")
	}

	for _, row := range r.Rows {
		_, err := db.Exec(ctx, upsertSQL,
			r.RunID.String(), row.Scenario, int(row.RuleID), row.RuleName,
			row.Findings, row.TruePositives, row.Injected,
			row.Precision, row.Recall, row.PrecisionLo, row.PrecisionHi, row.Sufficient)
		if err != nil {
			return fmt.Errorf("K7 ölçümü yazılamadı (kural %s): %w", row.RuleID, err)
		}
	}
	return nil
}

// Summary, raporu okunabilir bir tabloya çevirir.
func (r Report) Summary() string {
	var b strings.Builder

	fmt.Fprintf(&b, "K7 — bütünlük tespiti (koşu %s, senaryo %s)\n", r.RunID, r.Scenario)
	fmt.Fprintf(&b, "eşik: precision ≥ %.0f%% · ölçülebilirlik: ≥ %d kanonik bulgu (ADR-31/9)\n\n",
		r.Threshold*100, r.MinFindings)

	fmt.Fprintf(&b, "%-4s %-30s %8s %8s %8s %10s %18s %10s %s\n",
		"kural", "ad", "bulgu", "doğru", "enjekte", "precision", "Wilson %95", "recall", "durum")

	for _, row := range r.Rows {
		precision := "—"
		wilson := "—"
		if row.Precision != nil {
			precision = fmt.Sprintf("%.4f", *row.Precision)
			if row.PrecisionLo != nil && row.PrecisionHi != nil {
				wilson = fmt.Sprintf("[%.3f, %.3f]", *row.PrecisionLo, *row.PrecisionHi)
			}
		}
		fmt.Fprintf(&b, "%-4d %-30s %8d %8d %8d %10s %18s %10.4f %s\n",
			int(row.RuleID), row.RuleName,
			row.Findings, row.TruePositives, row.Injected,
			precision, wilson, row.Recall, row.Verdict(r.Threshold))
	}

	b.WriteString("\nkarışıklık matrisi (satır: tespit eden · sütun: gerçek enjeksiyon)\n")
	fmt.Fprintf(&b, "%-14s %-14s %10s %12s\n", "tespit_eden", "enjekte_edilen", "kanonik", "bastırılmış")
	for _, cell := range r.Confusion {
		injected := "temiz"
		if cell.InjectedRule != nil {
			injected = fmt.Sprintf("kural %d", int(*cell.InjectedRule))
		}
		fmt.Fprintf(&b, "%-14s %-14s %10d %12d\n",
			fmt.Sprintf("kural %d", int(cell.DetectedBy)), injected,
			cell.Canonical, cell.Suppressed)
	}
	return b.String()
}

// K7Verdict, koşunun K7 özetini döndürür.
//
// Kural bazında ayrıştırılmış bir sonuç verir: "geçti/tutmadı" ikilisi K7'nin
// kural bazlı tanımını yansıtmaz.
func (r Report) K7Verdict() string {
	var passed, failed, unmeasured, outOfScope []string

	for _, row := range r.Rows {
		label := fmt.Sprintf("%d", int(row.RuleID))
		switch {
		case !row.RuleID.EventAnchored():
			outOfScope = append(outOfScope, label)
		case row.Precision == nil:
			unmeasured = append(unmeasured, label)
		case !row.Sufficient:
			unmeasured = append(unmeasured, label+" (yetersiz)")
		case *row.Precision >= r.Threshold:
			passed = append(passed, label)
		default:
			failed = append(failed, label)
		}
	}

	var parts []string
	if len(passed) > 0 {
		parts = append(parts, "geçti: "+strings.Join(passed, ", "))
	}
	if len(failed) > 0 {
		parts = append(parts, "tutmadı: "+strings.Join(failed, ", "))
	}
	if len(unmeasured) > 0 {
		parts = append(parts, "ölçülemedi: "+strings.Join(unmeasured, ", "))
	}
	if len(outOfScope) > 0 {
		parts = append(parts, "kapsam dışı (ADR-30): "+strings.Join(outOfScope, ", "))
	}
	return "K7 — " + strings.Join(parts, " · ")
}
