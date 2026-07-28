// T-E03-13 — `estimates` tablosuna toplu yazma.
//
// # Neden COPY değil
//
// COPY en hızlı yoldur ama sunucu tarafında fonksiyon çağıramaz. Bu tabloda
// yazım anında iki şey yapılmak zorundadır:
//
//	ST_IsValid   → geçersizse ST_MakeValid + repaired = true   (K8 ölçümü)
//	ST_GeomFromWKB(…, 4326)::geography                          (SRID ataması)
//
// COPY ile bunlar ancak ara (staging) tablo üzerinden yapılabilirdi: iki kat
// yazma, ek DDL ve eşzamanlı koşularda ad çakışması. `unnest` ile toplu INSERT
// tek gidiş-dönüşte hem hızlı hem de bu iki denetimi taşıyor — `cells` yazımı
// (T-E02-06) da aynı deseni kullanır.
//
// # repaired sütunu ölçümdür, umut değil
//
// Kenar izleme (ADR-21) yapı gereği geçerli poligon üretir; `repaired`
// satırlarının sıfır çıkması beklenir. Denetim yine de her satırda koşar:
// K8'in `repaired_ratio < %1` ölçütü ancak gerçekten ölçülerek raporlanabilir.
// Beklentiyi doğrulamamak, onu varsaymaktan iyidir.

package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// defaultEstimateBatch, tek sorguda yazılan satır sayısıdır.
//
// Satırlar büyüktür: kırsal bir M@95 geometrisi binlerce köşe taşır (~100 KB).
// 200 satır ≈ 20 MB'lık bir sorgu demektir; daha büyük parti, sunucu tarafında
// bellek baskısı ve `max_allowed_packet` benzeri sınırlara yaklaşma riskidir.
const defaultEstimateBatch = 200

// EstimateRow, `estimates` tablosunun bir satırıdır.
//
// Geometri **kodlanmış** gelir: bu paket analiz alan tiplerini tanımaz
// (katman kuralı), yalnızca WKB baytlarını taşır.
type EstimateRow struct {
	RunID      uuid.UUID
	EventID    uuid.UUID
	Time       time.Time
	Method     string  // 'B0' | 'B1' | 'M'
	Confidence float64 // B0/B1 = -1 sentinel (ADR-01/EK-02)
	// GeometryWKB, MULTIPOLYGON'un OGC WKB kodudur (SRID gömülü değil).
	GeometryWKB []byte
	// CentroidWKB, POINT'in OGC WKB kodudur.
	CentroidWKB []byte
	AreaKM2     float64
	PartCount   int
	TAUsed      bool
	Scenario    string // A/B/C/D
}

// insertEstimatesSQL, tahminleri toplu ekler.
//
// Onarım yolu: ST_MakeValid bir GEOMETRYCOLLECTION ya da POLYGON döndürebilir;
// sütun MULTIPOLYGON beklediği için sonuç ST_CollectionExtract(…, 3) ile
// poligonlara indirgenir ve ST_Multi ile sarılır.
const insertEstimatesSQL = `
WITH input AS (
    SELECT * FROM unnest(
        $1::uuid[], $2::uuid[], $3::timestamptz[], $4::varchar[], $5::float8[],
        $6::bytea[], $7::bytea[], $8::float8[], $9::int[], $10::bool[], $11::varchar[]
    ) AS e(
        run_id, event_id, time, method, confidence,
        geometry_wkb, centroid_wkb, area_km2, part_count, ta_used, scenario
    )
),
decoded AS (
    SELECT
        i.*,
        ST_GeomFromWKB(i.geometry_wkb, 4326) AS geom,
        ST_IsValid(ST_GeomFromWKB(i.geometry_wkb, 4326)) AS valid
    FROM input i
)
INSERT INTO estimates (
    run_id, event_id, time, method, confidence,
    geometry, centroid, area_km2, part_count, repaired, ta_used, scenario
)
SELECT
    d.run_id, d.event_id, d.time, d.method, d.confidence,
    CASE
        WHEN d.valid THEN d.geom
        ELSE ST_Multi(ST_CollectionExtract(ST_MakeValid(d.geom), 3))
    END::geography,
    ST_GeomFromWKB(d.centroid_wkb, 4326)::geography,
    d.area_km2, d.part_count, NOT d.valid, d.ta_used, d.scenario
FROM decoded d`

// InsertEstimates, tahminleri parti parti yazar ve yazılan satır sayısını
// döndürür.
//
// Partiler ayrı işlemlerdir: 300.000 olaylık bir koşuyu tek işlemde tutmak
// WAL'i şişirir ve hata hâlinde saatlerce süren bir geri alma demektir. Yarım
// kalan koşu `verify_integrity` ile görünür olur (ADR-01) — sessiz kalmaz.
func (p *Pool) InsertEstimates(ctx context.Context, rows []EstimateRow) (int64, error) {
	return p.insertEstimatesBatched(ctx, rows, defaultEstimateBatch)
}

func (p *Pool) insertEstimatesBatched(ctx context.Context, rows []EstimateRow, batch int) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	if batch <= 0 {
		batch = defaultEstimateBatch
	}

	var total int64
	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}

		a, err := newEstimateArrays(rows[start:end], start)
		if err != nil {
			return total, err
		}

		tag, err := p.pool.Exec(ctx, insertEstimatesSQL,
			a.runID, a.eventID, a.time, a.method, a.confidence,
			a.geometry, a.centroid, a.areaKM2, a.partCount, a.taUsed, a.scenario)
		if err != nil {
			return total, fmt.Errorf("estimates toplu yazma başarısız (satır %d..%d): %w",
				start, end-1, err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// CountEstimates, bir koşuya ait tahmin sayısını döndürür.
func (p *Pool) CountEstimates(ctx context.Context, runID uuid.UUID) (int64, error) {
	var n int64
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM estimates WHERE run_id = $1::uuid`, runID.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("tahmin sayımı başarısız (%s): %w", runID, err)
	}
	return n, nil
}

// GeometryStats, bir koşunun geometri kararlılığı ölçümüdür (K8).
type GeometryStats struct {
	// Method, ölçümün ait olduğu yöntemdir.
	Method string
	// Confidence, güven seviyesidir (B0/B1 için -1).
	Confidence float64
	// Rows, satır sayısıdır.
	Rows int64
	// Repaired, ST_MakeValid uygulanmış satır sayısıdır.
	Repaired int64
	// P95PartCount, part_count'un %95'lik dilimidir.
	P95PartCount float64
	// MaxPartCount, en çok parçalı geometrinin parça sayısıdır.
	MaxPartCount int
	// AvgVertexCount, ortalama köşe sayısıdır (hacim ölçümü).
	AvgVertexCount float64
	// AvgBytes, geometri sütununun ortalama bayt boyudur.
	AvgBytes float64
}

// geometryStatsSQL, K8 ölçümünü **yöntem başına** üretir.
//
// Havuzlanmış (tüm satırlar birlikte) bir p95, B0/B1'in daima tek parçalı
// olması yüzünden M'nin parçalanmasını maskeler: satırların %40'ı yapısal
// olarak 1'dir. Kriter M'nin geometrik kararlılığını ölçtüğü için gruplama
// (method, confidence) üzerindedir.
const geometryStatsSQL = `
SELECT
    method,
    confidence,
    count(*)                                                        AS rows,
    count(*) FILTER (WHERE repaired)                                AS repaired,
    percentile_cont(0.95) WITHIN GROUP (ORDER BY part_count)        AS p95_part_count,
    max(part_count)                                                 AS max_part_count,
    avg(ST_NPoints(geometry::geometry))                             AS avg_vertices,
    avg(length(ST_AsBinary(geometry)))                              AS avg_bytes
FROM estimates
WHERE run_id = $1::uuid
GROUP BY method, confidence
ORDER BY method, confidence`

// GeometryStatsByMethod, K8 ölçümünü yöntem başına döndürür.
func (p *Pool) GeometryStatsByMethod(ctx context.Context, runID uuid.UUID) ([]GeometryStats, error) {
	rows, err := p.pool.Query(ctx, geometryStatsSQL, runID.String())
	if err != nil {
		return nil, fmt.Errorf("geometri ölçümü başarısız (%s): %w", runID, err)
	}
	defer rows.Close()

	var out []GeometryStats
	for rows.Next() {
		var s GeometryStats
		if err := rows.Scan(&s.Method, &s.Confidence, &s.Rows, &s.Repaired,
			&s.P95PartCount, &s.MaxPartCount, &s.AvgVertexCount, &s.AvgBytes); err != nil {
			return nil, fmt.Errorf("geometri ölçümü okunamadı: %w", err)
		}
		// `method` sütunu CHAR(2)'dir: PostgreSQL sabit uzunluğa boşlukla
		// tamamlar ve 'M' geri okunduğunda "M " olur. Karşılaştırmaların
		// sessizce başarısız olmaması için kırpılır.
		s.Method = strings.TrimSpace(s.Method)
		out = append(out, s)
	}
	return out, rows.Err()
}

// AreaCrossCheck, Go'da hesaplanan alan ile PostGIS ST_Area farkının
// özetidir (ADR-21 çapraz kontrolü).
type AreaCrossCheck struct {
	Rows        int64
	MaxRelDiff  float64
	MeanRelDiff float64
}

// areaCrossCheckSQL, saklanan area_km2 ile ST_Area(geography) farkını ölçer.
//
// İki hesap **aynı köşelerden** gelir: biri Go'da küresel formülle (authalic
// yarıçap), biri PostGIS'te elipsoit üzerinde. Fark, elipsoit–küre farkından
// ibarettir ve %1 toleransın çok altında kalmalıdır.
const areaCrossCheckSQL = `
SELECT
    count(*),
    coalesce(max(abs(area_km2 - st_area_km2) / st_area_km2), 0),
    coalesce(avg(abs(area_km2 - st_area_km2) / st_area_km2), 0)
FROM (
    SELECT area_km2, ST_Area(geometry) / 1e6 AS st_area_km2
    FROM estimates
    WHERE run_id = $1::uuid
) t
WHERE st_area_km2 > 0`

// CheckArea, alan çapraz kontrolünü çalıştırır.
func (p *Pool) CheckArea(ctx context.Context, runID uuid.UUID) (AreaCrossCheck, error) {
	var c AreaCrossCheck
	err := p.pool.QueryRow(ctx, areaCrossCheckSQL, runID.String()).
		Scan(&c.Rows, &c.MaxRelDiff, &c.MeanRelDiff)
	if err != nil {
		return AreaCrossCheck{}, fmt.Errorf("alan çapraz kontrolü başarısız (%s): %w", runID, err)
	}
	return c, nil
}

// ─── Dizi parametreleri ───────────────────────────────────────────────────────

// estimateArrays, unnest parametrelerini sütun dizileri olarak tutar.
type estimateArrays struct {
	runID      []string
	eventID    []string
	time       []time.Time
	method     []string
	confidence []float64
	geometry   [][]byte
	centroid   [][]byte
	areaKM2    []float64
	partCount  []int32
	taUsed     []bool
	scenario   []string
}

// newEstimateArrays, satırları dizilere yerleştirir ve şema kısıtlarını
// istemci tarafında denetler.
//
// offset, hata iletisindeki satır numarasının parti içindeki değil, tüm
// kümedeki sırayı göstermesi içindir.
func newEstimateArrays(rows []EstimateRow, offset int) (*estimateArrays, error) {
	n := len(rows)
	a := &estimateArrays{
		runID: make([]string, n), eventID: make([]string, n),
		time: make([]time.Time, n), method: make([]string, n),
		confidence: make([]float64, n), geometry: make([][]byte, n),
		centroid: make([][]byte, n), areaKM2: make([]float64, n),
		partCount: make([]int32, n), taUsed: make([]bool, n),
		scenario: make([]string, n),
	}

	for i, r := range rows {
		idx := offset + i
		switch {
		case r.RunID == uuid.Nil || r.EventID == uuid.Nil:
			return nil, fmt.Errorf("estimates[%d]: run_id/event_id boş olamaz (ADR-01)", idx)
		case r.Method != "B0" && r.Method != "B1" && r.Method != "M":
			return nil, fmt.Errorf("estimates[%d]: method 'B0'/'B1'/'M' olmalı (%q)", idx, r.Method)
		case len(r.GeometryWKB) == 0 || len(r.CentroidWKB) == 0:
			return nil, fmt.Errorf("estimates[%d]: geometri kodlanmamış", idx)
		case !(r.AreaKM2 > 0):
			return nil, fmt.Errorf("estimates[%d]: area_km2 pozitif olmalı (%g)", idx, r.AreaKM2)
		case r.PartCount < 1:
			return nil, fmt.Errorf("estimates[%d]: part_count ≥ 1 olmalı (%d)", idx, r.PartCount)
		case r.Time.IsZero():
			return nil, fmt.Errorf("estimates[%d]: zaman damgası boş (bölümleme sütunu)", idx)
		case len(r.Scenario) != 1 || r.Scenario < "A" || r.Scenario > "D":
			return nil, fmt.Errorf("estimates[%d]: scenario A/B/C/D olmalı (%q)", idx, r.Scenario)
		}
		if r.Method == "M" {
			if !(r.Confidence > 0 && r.Confidence < 1) {
				return nil, fmt.Errorf("estimates[%d]: M için confidence (0,1) olmalı (%g)", idx, r.Confidence)
			}
		} else if r.Confidence != -1 {
			return nil, fmt.Errorf("estimates[%d]: %s için confidence -1 sentinel olmalı (%g)",
				idx, r.Method, r.Confidence)
		}

		a.runID[i] = r.RunID.String()
		a.eventID[i] = r.EventID.String()
		a.time[i] = r.Time.UTC()
		a.method[i] = r.Method
		a.confidence[i] = r.Confidence
		a.geometry[i] = r.GeometryWKB
		a.centroid[i] = r.CentroidWKB
		a.areaKM2[i] = r.AreaKM2
		a.partCount[i] = int32(r.PartCount)
		a.taUsed[i] = r.TAUsed
		a.scenario[i] = r.Scenario
	}
	return a, nil
}
