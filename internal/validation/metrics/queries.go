// T-E04-03 — Metrik SQL şablonları (plan BÖLÜM F).
//
//	F.1 kapsama oranı            → coverage_rate            (K1)
//	F.2 alan ve parça sayıları   → median/p90 alan, p95 parça (K8)
//	F.3 Haversine hata dilimleri → median/r50/r95           (adli anlam)
//
// # Neden SQL, Go değil
//
// Üç sorgu da yüzdelik (percentile) ve mekânsal yüklem içeriyor. Yüz binlerce
// satırı Go'ya çekip orada sıralamak hem yavaş hem de gereksiz: PostGIS ve
// `percentile_cont` bu işi veri yerinde yapar. Go tarafında kalan şey
// sonuçların birleştirilmesi ve `metrics` tablosuna yazılmasıdır.
//
// # Bölüm anahtarı sabittir, parametre değil
//
// Her sorgu **tek** bir `partition_key` ile çalışır (K4). Anahtar tipli
// olduğu için (partition.go) çağıran "her ikisi" diyemez.

package metrics

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB, metrik sorgularının ihtiyaç duyduğu bağlantı yüzeyidir.
type DB = *pgxpool.Pool

// Key, bir ölçüm satırının kimliğidir: yöntem ve güven seviyesi.
//
// B0/B1 için Confidence = −1 sentinel'dir (ADR-01/EK-02); karşılaştırmaya
// girmez, yalnızca satırı ayırt eder.
type Key struct {
	Method     string
	Confidence float64
}

// Label, insan tarafından okunabilir gösterimdir (B0, B1, M@90%).
func (k Key) Label() string {
	if k.Method != "M" {
		return k.Method
	}
	return fmt.Sprintf("M@%.0f%%", k.Confidence*100)
}

// CoverageRow, F.1 sonucudur.
type CoverageRow struct {
	Key
	NEvents      int64
	CoverageRate float64
}

// coverageSQL, plan F.1'dir.
//
// `ST_Covers` kullanılır, `ST_Contains` değil: sınır üzerindeki nokta
// `ST_Contains` ile **dışarıda** sayılır. Hex sınırına tam düşen bir gerçek
// konumun olasılığı sıfıra yakındır ama sıfır değildir; kenar durumunun
// tanımsız kalması, kapsama oranını açıklanamaz biçimde düşürürdü.
//
// Yalnızca `covered = TRUE` olaylar sayılır: kapsama dışı ajan için kayıt
// zaten üretilmez (ADR-08/3), ground truth satırı ise ölçüme girmemelidir.
const coverageSQL = `
SELECT e.method,
       e.confidence,
       count(*)                                                        AS n_events,
       avg((ST_Covers(e.geometry::geometry, g.true_location::geometry))::int) AS coverage_rate
  FROM estimates e
  JOIN ground_truth g ON g.run_id = e.run_id AND g.event_id = e.event_id
 WHERE e.run_id = $1::uuid
   AND g.partition_key = $2
   AND g.covered = TRUE
 GROUP BY e.method, e.confidence
 ORDER BY e.method, e.confidence`

// Coverage, F.1'i çalıştırır: yöntem ve güven bazında kapsama oranı.
func Coverage(ctx context.Context, db DB, runID uuid.UUID, pkey PartitionKey) ([]CoverageRow, error) {
	if err := pkey.validate(); err != nil {
		return nil, err
	}

	rows, err := db.Query(ctx, coverageSQL, runID.String(), pkey.String())
	if err != nil {
		return nil, fmt.Errorf("F.1 kapsama sorgusu (%s, %s): %w", runID, pkey, err)
	}
	defer rows.Close()

	var out []CoverageRow
	for rows.Next() {
		var r CoverageRow
		if err := rows.Scan(&r.Method, &r.Confidence, &r.NEvents, &r.CoverageRate); err != nil {
			return nil, fmt.Errorf("F.1 satırı okunamadı: %w", err)
		}
		r.Method = trimMethod(r.Method)
		out = append(out, r)
	}
	return out, rows.Err()
}

// AreaRow, F.2 sonucudur.
type AreaRow struct {
	Key
	MedianAreaKM2   float64
	P90AreaKM2      float64
	MedianPartCount float64
	P95PartCount    float64
	RepairedRatio   float64
}

// areaSQL, plan F.2'dir.
const areaSQL = `
SELECT e.method,
       e.confidence,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY e.area_km2)   AS median_area_km2,
       percentile_cont(0.90) WITHIN GROUP (ORDER BY e.area_km2)   AS p90_area_km2,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY e.part_count) AS median_part_count,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY e.part_count) AS p95_part_count,
       avg(e.repaired::int)                                       AS repaired_ratio
  FROM estimates e
  JOIN ground_truth g ON g.run_id = e.run_id AND g.event_id = e.event_id
 WHERE e.run_id = $1::uuid
   AND g.partition_key = $2
 GROUP BY e.method, e.confidence
 ORDER BY e.method, e.confidence`

// AreaStats, F.2'yi çalıştırır: alan ve parça sayısı dağılımları.
func AreaStats(ctx context.Context, db DB, runID uuid.UUID, pkey PartitionKey) ([]AreaRow, error) {
	if err := pkey.validate(); err != nil {
		return nil, err
	}

	rows, err := db.Query(ctx, areaSQL, runID.String(), pkey.String())
	if err != nil {
		return nil, fmt.Errorf("F.2 alan sorgusu (%s, %s): %w", runID, pkey, err)
	}
	defer rows.Close()

	var out []AreaRow
	for rows.Next() {
		var r AreaRow
		if err := rows.Scan(&r.Method, &r.Confidence, &r.MedianAreaKM2, &r.P90AreaKM2,
			&r.MedianPartCount, &r.P95PartCount, &r.RepairedRatio); err != nil {
			return nil, fmt.Errorf("F.2 satırı okunamadı: %w", err)
		}
		r.Method = trimMethod(r.Method)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrorRow, F.3 sonucudur.
type ErrorRow struct {
	Key
	MedianHaversineM float64
	R50M             float64
	R95M             float64
}

// errorSQL, plan F.3'tür.
//
// `ST_Distance(geography, geography)` jeodezik mesafedir (metre). Argümanlar
// `::geometry`'ye düşürülseydi sonuç **derece** cinsinden anlamsız bir sayı
// olurdu ve hiçbir hata verilmezdi — testte `pkg/geo.Haversine` ile çapraz
// kontrol edilmesinin nedeni budur.
//
// Plan F.3'te `median_haversine_m` ve `r50_m` **aynı** ifadeden gelir; ikisi
// de `metrics` tablosunda NOT NULL sütun olduğu için ikisi de yazılır.
// Tautoloji plandan devralınmıştır ve bilerek korunmuştur: r50/r95 "hata
// yüzdelikleri" olarak tanımlıdır, medyan da zaten %50'lik dilimdir. Sütunu
// yeniden tanımlamak (örneğin "eşdeğer dairesel yarıçap") ölçümün anlamını
// değiştirirdi; şema değişikliği yerine eşitlik belgelenmiştir.
const errorSQL = `
WITH d AS (
    SELECT e.method,
           e.confidence,
           ST_Distance(e.centroid, g.true_location) AS err_m
      FROM estimates e
      JOIN ground_truth g ON g.run_id = e.run_id AND g.event_id = e.event_id
     WHERE e.run_id = $1::uuid
       AND g.partition_key = $2
       AND g.covered = TRUE
)
SELECT method,
       confidence,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY err_m) AS median_haversine_m,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY err_m) AS r50_m,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY err_m) AS r95_m
  FROM d
 GROUP BY method, confidence
 ORDER BY method, confidence`

// Errors, F.3'ü çalıştırır: merkez–gerçek konum hatasının dilimleri.
func Errors(ctx context.Context, db DB, runID uuid.UUID, pkey PartitionKey) ([]ErrorRow, error) {
	if err := pkey.validate(); err != nil {
		return nil, err
	}

	rows, err := db.Query(ctx, errorSQL, runID.String(), pkey.String())
	if err != nil {
		return nil, fmt.Errorf("F.3 hata sorgusu (%s, %s): %w", runID, pkey, err)
	}
	defer rows.Close()

	var out []ErrorRow
	for rows.Next() {
		var r ErrorRow
		if err := rows.Scan(&r.Method, &r.Confidence, &r.MedianHaversineM,
			&r.R50M, &r.R95M); err != nil {
			return nil, fmt.Errorf("F.3 satırı okunamadı: %w", err)
		}
		r.Method = trimMethod(r.Method)
		out = append(out, r)
	}
	return out, rows.Err()
}

// trimMethod, CHAR(2) sütununun boşluk dolgusunu temizler.
//
// PostgreSQL 'M' değerini "M " olarak döndürür; karşılaştırmalar sessizce
// başarısız olurdu (Sprint 4'te K8 raporunda bu hata görülmüştü).
func trimMethod(m string) string {
	for len(m) > 0 && m[len(m)-1] == ' ' {
		m = m[:len(m)-1]
	}
	return m
}
