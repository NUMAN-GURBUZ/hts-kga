// Ölçüm ve koşu uçları (T-E06-04, ADR-33/3).
//
// # Bu uçlarda k=5 uygulanmaz
//
// ADR-33/5: `metrics` ve `integrity_metrics` zaten koşu düzeyinde
// toplulaştırılmıştır — satır başına `n_events` binlercedir. k=5 denetimi
// hiçbir zaman tetiklenmez ve "k-anonimlik uygulanıyor" iddiasını boşa
// çıkarırdı. k=5'in anlamlı olduğu tek uç `CellActivity`'dir.

package query

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Run, bir koşunun özetidir.
type Run struct {
	RunID            uuid.UUID
	Scenario         string
	Morphology       string
	Seed             int64
	StartedAt        time.Time
	FinishedAt       *time.Time
	PublishedEvents  *int64
	AnalyzedEvents   *int64
	InspectedRecords *int64
	Lambda           *float64
}

// ListRuns, koşuları en yeniden eskiye döndürür.
func (s *Service) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.db.Query(ctx, `
        SELECT run_id, scenario, morphology, seed, started_at, finished_at,
               published_events, analyzed_events, inspected_records, lambda
          FROM run_config
         ORDER BY started_at DESC
         LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("koşu listesi: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.RunID, &r.Scenario, &r.Morphology, &r.Seed,
			&r.StartedAt, &r.FinishedAt, &r.PublishedEvents, &r.AnalyzedEvents,
			&r.InspectedRecords, &r.Lambda); err != nil {
			return nil, fmt.Errorf("koşu satırı: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SubscriberRow, bir abonenin koşu içindeki özetidir.
type SubscriberRow struct {
	PseudoMSISDN string
	Records      int64
	Estimates    int64
}

// Subscribers, koşunun tahmin geometrisi üretilmiş abonelerini, en zengin
// veriye sahip olandan başlayarak döndürür.
//
// # Neden var
//
// ADR-13'ün "tek abone zorunlu" kuralını değiştirmez — `Estimates` ucu
// hâlâ tek bir `subscriber` ister. Bu yalnızca o tek aboneyi **bulma**
// adımını kolaylaştırır: öncesinde tek yol `psql` ile elle sorgu atmaktı.
// Döndürülen `pseudo_msisdn`, gerçek numaranın geri döndürülemez HMAC
// takma adıdır (ADR-15); gerçek kimlik, konum ya da `ground_truth` taşımaz.
func (s *Service) Subscribers(ctx context.Context, runID uuid.UUID, limit int) ([]SubscriberRow, error) {
	if runID == uuid.Nil {
		return nil, ErrRunIDRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	rows, err := s.db.Query(ctx, `
        SELECT h.pseudo_msisdn,
               count(DISTINCT h.event_id)                               AS records,
               count(DISTINCT e.event_id) FILTER (WHERE e.method='M')   AS estimates
          FROM hts_records h
          LEFT JOIN estimates e
                 ON e.run_id = h.run_id AND e.event_id = h.event_id
         WHERE h.run_id = $1::uuid
         GROUP BY h.pseudo_msisdn
        HAVING count(DISTINCT e.event_id) FILTER (WHERE e.method='M') > 0
         ORDER BY estimates DESC, h.pseudo_msisdn
         LIMIT $2`, runID.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("abone listesi: %w", err)
	}
	defer rows.Close()

	var out []SubscriberRow
	for rows.Next() {
		var r SubscriberRow
		if err := rows.Scan(&r.PseudoMSISDN, &r.Records, &r.Estimates); err != nil {
			return nil, fmt.Errorf("abone satırı: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MetricRow, `metrics` tablosunun bir satırıdır (K1–K3).
type MetricRow struct {
	Scenario      string
	Method        string
	Confidence    float64
	PartitionKey  string
	NEvents       int64
	CoverageRate  float64
	MedianAreaKM2 float64
	P90AreaKM2    float64
	R50M          float64
	R95M          float64
	P95PartCount  int
	RepairedRatio float64
	ReductionVsB0 *float64
	ReductionVsB1 *float64
}

// Metrics, koşunun K1–K3 ölçümlerini döndürür.
func (s *Service) Metrics(ctx context.Context, runID uuid.UUID) ([]MetricRow, error) {
	if runID == uuid.Nil {
		return nil, ErrRunIDRequired
	}

	rows, err := s.db.Query(ctx, `
        SELECT scenario, method, confidence, partition_key, n_events,
               coverage_rate, median_area_km2, p90_area_km2,
               r50_m, r95_m, p95_part_count, repaired_ratio,
               reduction_vs_b0, reduction_vs_b1
          FROM metrics
         WHERE run_id = $1::uuid
         ORDER BY method, confidence`, runID.String())
	if err != nil {
		return nil, fmt.Errorf("metrik sorgusu: %w", err)
	}
	defer rows.Close()

	var out []MetricRow
	for rows.Next() {
		var m MetricRow
		if err := rows.Scan(&m.Scenario, &m.Method, &m.Confidence, &m.PartitionKey,
			&m.NEvents, &m.CoverageRate, &m.MedianAreaKM2, &m.P90AreaKM2,
			&m.R50M, &m.R95M, &m.P95PartCount, &m.RepairedRatio,
			&m.ReductionVsB0, &m.ReductionVsB1); err != nil {
			return nil, fmt.Errorf("metrik satırı: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// IntegrityMetricRow, `integrity_metrics` tablosunun bir satırıdır (K7).
type IntegrityMetricRow struct {
	RuleID        int
	RuleName      string
	Findings      int64
	TruePositives int64
	Injected      int64
	Precision     *float64
	Recall        float64
	PrecisionLo   *float64
	PrecisionHi   *float64
	Sufficient    bool
}

// IntegrityMetrics, koşunun K7 ölçümlerini döndürür.
func (s *Service) IntegrityMetrics(ctx context.Context, runID uuid.UUID) ([]IntegrityMetricRow, error) {
	if runID == uuid.Nil {
		return nil, ErrRunIDRequired
	}

	rows, err := s.db.Query(ctx, `
        SELECT rule_id, rule_name, findings, true_positives, injected,
               precision, recall, precision_lo, precision_hi, sufficient
          FROM integrity_metrics
         WHERE run_id = $1::uuid
         ORDER BY rule_id`, runID.String())
	if err != nil {
		return nil, fmt.Errorf("bütünlük metriği sorgusu: %w", err)
	}
	defer rows.Close()

	var out []IntegrityMetricRow
	for rows.Next() {
		var m IntegrityMetricRow
		if err := rows.Scan(&m.RuleID, &m.RuleName, &m.Findings, &m.TruePositives,
			&m.Injected, &m.Precision, &m.Recall, &m.PrecisionLo, &m.PrecisionHi,
			&m.Sufficient); err != nil {
			return nil, fmt.Errorf("bütünlük metriği satırı: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
