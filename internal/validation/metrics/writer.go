// T-E04-03 — Ölçüm sonuçlarının birleştirilmesi ve `metrics` tablosuna yazımı.
//
// F.1, F.2 ve F.3 aynı gruplama (yöntem × güven) üzerinde çalışır ama ayrı
// sorgulardır: plan onları ayrı şablonlar olarak tanımlar ve her birinin kendi
// filtresi vardır (F.1 ve F.3 yalnızca `covered = TRUE` olayları sayar, F.2
// hepsini). Tek bir dev sorguda birleştirmek bu farkları gizlerdi.
//
// Birleştirme bu yüzden Go tarafında, açık bir eşleştirmeyle yapılır: bir
// yöntem/güven anahtarı üç sonuçtan birinde eksikse bu **hata**dır, sessizce
// sıfırla doldurulmaz.

package metrics

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// Row, `metrics` tablosunun bir satırıdır.
type Row struct {
	Key
	PartitionKey     PartitionKey
	Scenario         string
	NEvents          int64
	CoverageRate     float64
	MedianAreaKM2    float64
	P90AreaKM2       float64
	MedianHaversineM float64
	R50M             float64
	R95M             float64
	MedianPartCount  int
	P95PartCount     int
	RepairedRatio    float64
	// ReductionVsB0 / ReductionVsB1, yalnızca M@90 satırında doludur (F.4).
	ReductionVsB0 *float64
	ReductionVsB1 *float64
}

// Compute, F.1–F.3'ü koşturup satırları birleştirir.
//
// Dönen satırlar `metrics` tablosunun CHECK kısıtlarını karşılar; yazımdan
// önce doğrulanır, böylece bir kısıt ihlali hangi ölçümden geldiğini
// söyleyerek düşer.
func Compute(ctx context.Context, db DB, runID uuid.UUID, scenario string,
	pkey PartitionKey) ([]Row, error) {

	if err := pkey.validate(); err != nil {
		return nil, err
	}
	if len(scenario) != 1 {
		return nil, fmt.Errorf("ölçüm: senaryo tek harf olmalı (%q)", scenario)
	}

	coverage, err := Coverage(ctx, db, runID, pkey)
	if err != nil {
		return nil, err
	}
	if len(coverage) == 0 {
		return nil, fmt.Errorf("ölçüm: '%s' kümesinde hiç tahmin yok (%s) — "+
			"koşu tamamlandı mı, örnekleme modu doğru mu?", pkey, runID)
	}

	areas, err := AreaStats(ctx, db, runID, pkey)
	if err != nil {
		return nil, err
	}
	errs, err := Errors(ctx, db, runID, pkey)
	if err != nil {
		return nil, err
	}

	areaBy := make(map[Key]AreaRow, len(areas))
	for _, a := range areas {
		areaBy[a.Key] = a
	}
	errBy := make(map[Key]ErrorRow, len(errs))
	for _, e := range errs {
		errBy[e.Key] = e
	}

	out := make([]Row, 0, len(coverage))
	for _, c := range coverage {
		area, ok := areaBy[c.Key]
		if !ok {
			return nil, fmt.Errorf("ölçüm: %s için F.2 sonucu yok — sorgular farklı "+
				"kümeler döndürdü", c.Label())
		}
		errRow, ok := errBy[c.Key]
		if !ok {
			return nil, fmt.Errorf("ölçüm: %s için F.3 sonucu yok — sorgular farklı "+
				"kümeler döndürdü", c.Label())
		}

		row := Row{
			Key:              c.Key,
			PartitionKey:     pkey,
			Scenario:         scenario,
			NEvents:          c.NEvents,
			CoverageRate:     c.CoverageRate,
			MedianAreaKM2:    area.MedianAreaKM2,
			P90AreaKM2:       area.P90AreaKM2,
			MedianHaversineM: errRow.MedianHaversineM,
			R50M:             errRow.R50M,
			R95M:             errRow.R95M,
			MedianPartCount:  roundPartCount(area.MedianPartCount),
			P95PartCount:     roundPartCount(area.P95PartCount),
			RepairedRatio:    area.RepairedRatio,
		}
		if err := row.validate(); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// roundPartCount, yüzdeliği tam sayıya çevirir.
//
// `metrics.median_part_count` INTEGER ve CHECK ≥ 1'dir; `percentile_cont`
// ise ara değer üretir (örneğin 2,5). Yuvarlama en yakına yapılır ve alt
// sınır 1'e sabitlenir — parça sayısı hiçbir geometride sıfır olamaz (PBT #5).
func roundPartCount(v float64) int {
	n := int(math.Round(v))
	if n < 1 {
		n = 1
	}
	return n
}

// validate, şema kısıtlarını istemci tarafında denetler.
func (r Row) validate() error {
	switch {
	case r.NEvents <= 0:
		return fmt.Errorf("ölçüm %s: n_events pozitif olmalı (%d)", r.Label(), r.NEvents)
	case r.CoverageRate < 0 || r.CoverageRate > 1:
		return fmt.Errorf("ölçüm %s: coverage_rate [0,1] olmalı (%g)", r.Label(), r.CoverageRate)
	case !(r.MedianAreaKM2 > 0) || !(r.P90AreaKM2 > 0):
		return fmt.Errorf("ölçüm %s: alanlar pozitif olmalı (medyan %g, p90 %g)",
			r.Label(), r.MedianAreaKM2, r.P90AreaKM2)
	case r.MedianHaversineM < 0 || r.R50M < 0 || r.R95M < 0:
		return fmt.Errorf("ölçüm %s: hata mesafeleri negatif olamaz", r.Label())
	case r.RepairedRatio < 0 || r.RepairedRatio > 1:
		return fmt.Errorf("ölçüm %s: repaired_ratio [0,1] olmalı (%g)",
			r.Label(), r.RepairedRatio)
	}
	return nil
}

// insertSQL, ölçüm satırlarını yazar.
const insertSQL = `
INSERT INTO metrics (
    run_id, scenario, method, confidence, partition_key, n_events,
    coverage_rate, median_area_km2, p90_area_km2,
    median_haversine_m, r50_m, r95_m,
    median_part_count, p95_part_count, repaired_ratio,
    reduction_vs_b0, reduction_vs_b1
) VALUES (
    $1::uuid, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17
)`

// Write, ölçüm satırlarını `metrics` tablosuna yazar.
//
// Satırlar tek tek yazılır: bir koşuda en çok beş satır vardır (yöntem ×
// güven) ve toplu yazmanın kazandıracağı bir şey yoktur; buna karşılık hangi
// satırın kısıtı ihlal ettiğini görmek değerlidir.
func Write(ctx context.Context, db DB, runID uuid.UUID, rows []Row) (int, error) {
	written := 0
	for _, r := range rows {
		if err := r.validate(); err != nil {
			return written, err
		}
		_, err := db.Exec(ctx, insertSQL,
			runID.String(), r.Scenario, r.Method, r.Confidence, r.PartitionKey.String(),
			r.NEvents, r.CoverageRate, r.MedianAreaKM2, r.P90AreaKM2,
			r.MedianHaversineM, r.R50M, r.R95M,
			r.MedianPartCount, r.P95PartCount, r.RepairedRatio,
			r.ReductionVsB0, r.ReductionVsB1)
		if err != nil {
			return written, fmt.Errorf("ölçüm yazımı (%s, %s): %w", r.Label(), r.PartitionKey, err)
		}
		written++
	}
	return written, nil
}

// DeleteRun, bir koşunun ölçümlerini siler (yeniden hesaplama için).
func DeleteRun(ctx context.Context, db DB, runID uuid.UUID) error {
	_, err := db.Exec(ctx, `DELETE FROM metrics WHERE run_id = $1::uuid`, runID.String())
	if err != nil {
		return fmt.Errorf("ölçüm temizliği (%s): %w", runID, err)
	}
	return nil
}
