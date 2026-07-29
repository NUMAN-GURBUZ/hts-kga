// T-E04-04 — Alan daralması: çalışmanın asıl iddiası (plan F.4, K2 · K3).
//
//	K2  M@90 vs B0 medyan alan daralması ≥ %75
//	K3  M@90 vs B1 medyan alan daralması  TA var ≥ %50 · TA yok ≥ %20
//
// # Karşılaştırma kuralları (plan F.4 notu)
//
//  1. M daima **@90%** seviyesinde karşılaştırılır. M@95 kullanılsaydı daralma
//     olduğundan küçük görünürdü; seviye ölçümden önce beyan edilmiştir.
//  2. Ölçüm **yalnızca 'V' kümesinde** yapılır (K4). Kalibrasyon kümesinde
//     ölçmek, modeli kendi verisinde sınamak olurdu.
//  3. B0/B1'in `confidence = −1` sentinel'i karşılaştırmaya girmez; yalnızca
//     satırı ayırt eder.
//
// # Olay kümesi eşitliği
//
// Medyan alanlar ancak aynı olay kümesi üzerinde karşılaştırılabilir. Örnekleme
// (ADR-24) her olay için ya beş satırın tamamını yazar ya da hiçbirini, bu
// yüzden küme yapı gereği eşittir — ama varsayılmaz: `EqualEventSets` bunu
// ölçer ve rapor bunu içerir.

package comparison

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ModelConfidence, karşılaştırmanın yapıldığı güven seviyesidir (plan F.4).
const ModelConfidence = 0.90

// Result, daralma ölçümüdür.
type Result struct {
	// B0AreaKM2, naif daire taban çizgisinin medyan alanıdır.
	B0AreaKM2 float64
	// B1AreaKM2, sektör dilimi taban çizgisinin medyan alanıdır.
	B1AreaKM2 float64
	// MAreaKM2, M@90'ın medyan alanıdır.
	MAreaKM2 float64
	// ReductionVsB0, 1 − alan(M@90)/alan(B0) — K2.
	ReductionVsB0 float64
	// ReductionVsB1, 1 − alan(M@90)/alan(B1) — K3.
	ReductionVsB1 float64
	// Events, karşılaştırmaya giren olay sayısıdır.
	Events int64
}

// reductionSQL, plan F.4'tür.
//
// Yalnızca 'V' kümesi; bölüm anahtarı sorguya **gömülüdür**, parametre
// değildir: kalibrasyon kümesinde daralma ölçmenin geçerli bir kullanımı
// yoktur (K4).
const reductionSQL = `
WITH a AS (
    SELECT e.method,
           e.confidence,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY e.area_km2) AS med_area,
           count(*)                                                AS n
      FROM estimates e
      JOIN ground_truth g ON g.run_id = e.run_id AND g.event_id = e.event_id
     WHERE e.run_id = $1::uuid
       AND g.partition_key = 'V'
     GROUP BY e.method, e.confidence
)
SELECT
    (SELECT med_area FROM a WHERE method = 'B0')                       AS b0_area,
    (SELECT med_area FROM a WHERE method = 'B1')                       AS b1_area,
    (SELECT med_area FROM a WHERE method = 'M' AND confidence = $2)    AS m_area,
    (SELECT n        FROM a WHERE method = 'M' AND confidence = $2)    AS n_events`

// Reductions, F.4'ü çalıştırır.
func Reductions(ctx context.Context, db *pgxpool.Pool, runID uuid.UUID) (Result, error) {
	var b0, b1, m *float64
	var n *int64

	err := db.QueryRow(ctx, reductionSQL, runID.String(), ModelConfidence).
		Scan(&b0, &b1, &m, &n)
	if err != nil {
		return Result{}, fmt.Errorf("F.4 daralma sorgusu (%s): %w", runID, err)
	}

	switch {
	case b0 == nil:
		return Result{}, fmt.Errorf("F.4: 'V' kümesinde B0 satırı yok (%s)", runID)
	case b1 == nil:
		return Result{}, fmt.Errorf("F.4: 'V' kümesinde B1 satırı yok (%s)", runID)
	case m == nil:
		return Result{}, fmt.Errorf("F.4: 'V' kümesinde M@%.2f satırı yok (%s)",
			ModelConfidence, runID)
	case !(*b0 > 0) || !(*b1 > 0):
		return Result{}, fmt.Errorf("F.4: taban çizgisi alanı pozitif değil (B0=%g, B1=%g)", *b0, *b1)
	}

	res := Result{
		B0AreaKM2:     *b0,
		B1AreaKM2:     *b1,
		MAreaKM2:      *m,
		ReductionVsB0: 1 - *m / *b0,
		ReductionVsB1: 1 - *m / *b1,
	}
	if n != nil {
		res.Events = *n
	}
	return res, nil
}

// EqualEventSets, üç yöntemin aynı olay kümesi üzerinde ölçülüp
// ölçülmediğini denetler.
//
// Farklı kümeler medyan alanları karşılaştırılamaz kılar: bir yöntemin
// eksik olayları sistematik olarak dar ya da geniş bölgelerse daralma oranı
// olduğundan farklı çıkar.
func EqualEventSets(ctx context.Context, db *pgxpool.Pool, runID uuid.UUID) (bool, error) {
	const sql = `
SELECT count(DISTINCT event_id) FILTER (WHERE method = 'B0') AS b0,
       count(DISTINCT event_id) FILTER (WHERE method = 'B1') AS b1,
       count(DISTINCT event_id) FILTER (WHERE method = 'M')  AS m
  FROM estimates
 WHERE run_id = $1::uuid`

	var b0, b1, m int64
	if err := db.QueryRow(ctx, sql, runID.String()).Scan(&b0, &b1, &m); err != nil {
		return false, fmt.Errorf("olay kümesi denetimi (%s): %w", runID, err)
	}
	return b0 == b1 && b1 == m, nil
}
