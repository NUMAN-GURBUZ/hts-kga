// T-E03-09..13 — Bir kaydın beş tahmini.
//
// Kütle haritası (Estimate) bir ara üründür; saklanan şey `estimates`
// tablosunun satırlarıdır. Bu dosya ikisi arasındaki köprüdür ve olay başına
// tam **beş** satır üretir:
//
//	B0        confidence = −1   naif daire
//	B1        confidence = −1   sektör dilimi
//	M @ 0.50  confidence = 0.50 kümülatif kontur
//	M @ 0.90  confidence = 0.90
//	M @ 0.95  confidence = 0.95
//
// Sayı sabittir: `verify_integrity` (003_integrity_fn.sql) `estimates` /
// `hts_records` oranını 4,5–5,5 aralığında bekler ve sapmayı bütünlük uyarısı
// olarak raporlar (ADR-01).
//
// # Sentinel −1 neden
//
// `confidence` UNIQUE indeksin parçasıdır (run_id, event_id, method,
// confidence, time). PostgreSQL'de NULL'lar birbirine eşit sayılmadığı için
// B0/B1 satırları NULL güvenle yinelenebilirdi; sentinel −1 bunu engeller
// (ADR-01/EK-02).
//
// # Katman kuralı
//
// Bu paket veritabanı tanımaz. `Shape`, satırın **geometrik içeriğidir**;
// run_id, event_id, zaman ve senaryo gibi kimlik alanları depolama katmanında
// eklenir (T-E03-13). Böylece kalibrasyon döngüsü (S5) aynı fonksiyonu
// hiçbir şey yazmadan çağırabilir.

package core

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/baseline"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Method, tahmin yöntemidir — `estimates.method` (CHAR(2)).
type Method string

const (
	// MethodB0, naif daire taban çizgisidir.
	MethodB0 Method = "B0"
	// MethodB1, sektör dilimi taban çizgisidir.
	MethodB1 Method = "B1"
	// MethodM, olasılıksal kütle modelidir.
	MethodM Method = "M"
)

// SentinelConfidence, B0/B1 satırlarının güven değeridir (ADR-01/EK-02).
const SentinelConfidence = -1.0

// Shape, tek bir `estimates` satırının geometrik içeriğidir.
type Shape struct {
	// Method, yöntemdir.
	Method Method
	// Confidence, güven seviyesidir; B0/B1 için SentinelConfidence.
	Confidence float64
	// Geometry, saklanacak MULTIPOLYGON'dur (WGS84).
	Geometry geometry.GeoMultiPolygon
	// Centroid, merkezdir (WGS84) — M'de kütle ağırlıklı, taban çizgilerinde
	// geometrik (ADR-10).
	Centroid geo.WGS84
	// AreaKM2, küresel alandır.
	AreaKM2 float64
	// PartCount, bileşen sayısıdır (K8).
	PartCount int
	// TAUsed, TA halkasının gerçekten kullanıldığını bildirir. Taban
	// çizgileri TA'yı hiç görmediği için onlarda daima false'tur.
	TAUsed bool
}

// Shapes, bir kaydın beş tahminini üretir.
//
// levels, senaryo config'inin `analysis.contour_levels` değeridir; sıra
// önemsizdir, çıktı daima B0, B1 ve ardından artan güven seviyeleridir.
func Shapes(rec Record, res Result, inv *params.Inventory, grid *density.Grid, levels []float64) ([]Shape, error) {
	if inv == nil || grid == nil {
		return nil, fmt.Errorf("tahmin üretimi: envanter ve ızgara zorunlu")
	}
	projector := inv.Projector()
	if projector == nil {
		return nil, fmt.Errorf("tahmin üretimi: envanterin izdüşümü yok")
	}

	serving, ok := inv.Cell(rec.CellID)
	if !ok {
		return nil, fmt.Errorf("tahmin üretimi (olay %s): hücre %s envanterde yok", rec.EventID, rec.CellID)
	}
	sector, err := geometry.NewSector(serving.Site, serving.AzimuthDeg, serving.BeamWidthDeg, serving.RMaxM)
	if err != nil {
		return nil, fmt.Errorf("tahmin üretimi (olay %s): %w", rec.EventID, err)
	}

	contours, err := Contours(res.Mass, grid, levels)
	if err != nil {
		return nil, fmt.Errorf("tahmin üretimi (olay %s): %w", rec.EventID, err)
	}

	shapes := make([]Shape, 0, 2+len(contours))

	b0, err := baseline.B0(serving.Site, serving.RMaxM)
	if err != nil {
		return nil, fmt.Errorf("tahmin üretimi (olay %s): %w", rec.EventID, err)
	}
	shapes = append(shapes, shapeFrom(MethodB0, SentinelConfidence, b0, b0.Centroid(), projector, false))

	b1, err := baseline.B1(sector)
	if err != nil {
		return nil, fmt.Errorf("tahmin üretimi (olay %s): %w", rec.EventID, err)
	}
	shapes = append(shapes, shapeFrom(MethodB1, SentinelConfidence, b1, b1.Centroid(), projector, false))

	for _, c := range contours {
		mp, err := grid.Polygonize(c.Cells)
		if err != nil {
			return nil, fmt.Errorf("tahmin üretimi (olay %s, %.2f): %w", rec.EventID, c.Level, err)
		}
		shapes = append(shapes, shapeFrom(MethodM, c.Level, mp, c.Centroid, projector, res.TAUsed))
	}

	return shapes, nil
}

// shapeFrom, ENU geometrisini saklanabilir satır içeriğine çevirir.
//
// Alan, **izdüşümden sonra** hesaplanır: saklanan köşelerin alanı ölçülür,
// ENU düzlemindeki ölçek sapması sonuca girmez (ADR-21).
func shapeFrom(method Method, confidence float64, mp geometry.MultiPolygon,
	centroid geo.Point, pr *geo.Projector, taUsed bool) Shape {

	projected := mp.Project(pr)
	return Shape{
		Method:     method,
		Confidence: confidence,
		Geometry:   projected,
		Centroid:   pr.Inverse(centroid),
		AreaKM2:    projected.AreaKM2(),
		PartCount:  projected.PartCount(),
		TAUsed:     taUsed,
	}
}
