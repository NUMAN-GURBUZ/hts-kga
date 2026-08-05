// G0 tanı ölçümü — kapsamanın üst sınırı.
//
// # Neden gerekli
//
// Pilot taraması `coverage(λ)`'nın hem **düz** hem de hedefin (0,90) çok
// altında olduğunu gösterdi (~0,51). İki açıklama mümkündür ve ayırt
// edilmeleri şarttır:
//
//	(a) Model fazla dar   — kütle doğru yerde ama yayılımı yetersiz;
//	                        λ'yı büyütmek kapsamayı yükseltmeliydi.
//	(b) Arama bölgesi dar — gerçek konum, serving hücrenin **dilimi dışında**;
//	                        o zaman hiçbir λ değeri onu içeri alamaz, çünkü
//	                        kütle o bölgede hiç tanımlı değildir.
//
// (b)'nin nedeni tasarımsaldır: analiz, arama bölgesini azimut ± hüzme/2 ile
// sınırlar (T-E03-03), oysa simülatörün best-server seçimi **tüm yönlerde**
// çalışır — anten deseninin yan lobundan ya da gölgeleme sayesinde, dilimin
// dışındaki bir ajan da o hücreye bağlanabilir.
//
// `WedgeUpperBound` bu üst sınırı ölçer: gerçek konumu serving dilimin içinde
// olan olayların oranı. Ölçülen kapsama bu sayıya yakınsa sorun λ'da değil,
// arama bölgesinin tanımındadır.

package calibration

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
)

// Diagnostics, kapsamanın neden sınırlı olduğunu gösteren ölçümlerdir.
type Diagnostics struct {
	// Samples, incelenen olay sayısıdır.
	Samples int
	// InWedge, gerçek konumu serving dilimin içinde olan olay sayısıdır.
	//
	// Kapsamanın **teorik üst sınırı** budur: dilim dışındaki bir konum için
	// kütle tanımlı değildir, hiçbir λ onu kapsayamaz.
	InWedge int
	// InRegion, gerçek konumun hücresi ızgara bölgesinde olan olay sayısıdır.
	//
	// InWedge'den küçük olabilir: dilimin kenarındaki bir konum, hücre
	// merkezi dilim dışında kaldığı için ızgaraya girmemiş olabilir.
	InRegion int
	// BeyondRMax, gerçek konumu r_max'ın ötesinde olan olay sayısıdır.
	BeyondRMax int
	// OffAxisMedianDeg, gerçek konumun hüzme ekseninden medyan sapmasıdır.
	OffAxisMedianDeg float64
}

// WedgeRatio, kapsamanın üst sınırıdır.
func (d Diagnostics) WedgeRatio() float64 {
	if d.Samples == 0 {
		return 0
	}
	return float64(d.InWedge) / float64(d.Samples)
}

// RegionRatio, ızgara bölgesine düşen olayların oranıdır.
func (d Diagnostics) RegionRatio() float64 {
	if d.Samples == 0 {
		return 0
	}
	return float64(d.InRegion) / float64(d.Samples)
}

// Summary, tanının okunabilir özetidir.
func (d Diagnostics) Summary() string {
	return fmt.Sprintf(
		"tanı: %d olay · dilim içinde %%%.1f (kapsamanın üst sınırı) · "+
			"ızgara bölgesinde %%%.1f · r_max ötesinde %%%.1f · "+
			"eksenden medyan sapma %.1f°",
		d.Samples, d.WedgeRatio()*100, d.RegionRatio()*100,
		100*float64(d.BeyondRMax)/float64(max(1, d.Samples)), d.OffAxisMedianDeg)
}

// Diagnose, kapsamanın üst sınırını ve sapma dağılımını ölçer.
func (r *Runner) Diagnose() (Diagnostics, error) {
	d := Diagnostics{Samples: len(r.samples)}
	offsets := make([]float64, 0, len(r.samples))

	for _, s := range r.samples {
		cell, ok := r.inventory.Cell(s.cellID)
		if !ok {
			// Enjeksiyon kural 1 (sahte hücre): envanterde yok, analiz
			// edilemez. Örneklemde olması beklenir, tanıya girmez.
			d.Samples--
			continue
		}

		sector, err := geometry.NewSector(cell.Site, cell.AzimuthDeg, cell.BeamWidthDeg, cell.RMaxM)
		if err != nil {
			return Diagnostics{}, fmt.Errorf("tanı: sektör kurulamadı: %w", err)
		}

		truth := r.projector.Forward(s.truth)
		offsets = append(offsets, abs(sector.OffAxisDeg(truth)))

		if sector.DistanceM(truth) > cell.RMaxM {
			d.BeyondRMax++
		}
		if sector.Contains(truth) {
			d.InWedge++
		}
		if sector.Contains(r.grid.Center(r.grid.At(truth))) {
			d.InRegion++
		}
	}

	d.OffAxisMedianDeg = median(offsets)
	return d, nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// median, sıralanmamış dilimin medyanıdır (kopya üzerinde çalışır).
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted[len(sorted)/2]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
