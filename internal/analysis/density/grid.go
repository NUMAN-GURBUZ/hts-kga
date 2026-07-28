// T-E03-05 — Kütle ızgarasının üretimi.
//
// # Izgara koşu bazında demirlidir (G6)
//
// Hücre merkezleri ENU başlangıcından türetilir (`geo.HexGrid.Center`), olayın
// serving direğinden değil. Yani aynı koşudaki iki farklı olay, aynı fiziksel
// noktada **aynı** hücreyi görür.
//
// Bu bir kolaylık değil, zorunluluktur. Izgara olay başına direğe demirlenseydi:
//
//   - S4'te kontur birleştirme farklı olayların hücrelerini üst üste
//     bindiremezdi (aynı bölge, kayık iki ızgara),
//   - K10 bozulurdu: hücre kimlikleri serving hücreye bağlı olurdu,
//   - komşuluk ilişkileri olaylar arasında karşılaştırılamazdı.
//
// # Bölge, dilimin çevreleyen kutusundan taranır
//
// Sektör dilimi eksen hizalı olmadığı için tarama kutudan yapılır, sonra
// `Sector.Contains` ile elenir. Kaba "merkez ± r_max" kutusu yerine dilimin
// gerçek uç noktalarından kurulan kutu kullanılır (bkz. geometry.Sector):
// kentsel 65°'lik dilimde bu, taranan hücre sayısını ~3,7 kat azaltır.

package density

import (
	"fmt"
	"math"
	"sort"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Grid, analiz motorunun kütle ızgarasıdır.
//
// Değişmezdir; koşu boyunca tek örnek paylaşılır.
type Grid struct {
	hex *geo.HexGrid
}

// NewGrid, verilen çözünürlükte (merkez-merkez metre) bir ızgara kurar.
//
// Çözünürlük senaryo config'inden gelir (analysis.grid_resolution_m).
func NewGrid(resolutionM float64) (*Grid, error) {
	hex, err := geo.NewHexGrid(resolutionM)
	if err != nil {
		return nil, fmt.Errorf("kütle ızgarası: %w", err)
	}
	return &Grid{hex: hex}, nil
}

// ResolutionM, ızgara adımını döndürür (merkez-merkez metre).
func (g *Grid) ResolutionM() float64 { return g.hex.Resolution() }

// CellAreaM2, tek bir hücrenin alanıdır.
func (g *Grid) CellAreaM2() float64 { return g.hex.CellAreaM2() }

// Center, eksenel koordinatın ENU merkezidir.
func (g *Grid) Center(a geo.Axial) geo.Point { return g.hex.Center(a) }

// Corners, hücrenin altı köşesidir (örtüşme oranı hesabı için).
func (g *Grid) Corners(a geo.Axial) []geo.Point { return g.hex.Corners(a) }

// Polygonize, hücre kümesinin sınırını MULTIPOLYGON'a çevirir (T-E03-11).
//
// Izgaranın kendi altıgen tanımıyla yapılması zorunludur: köşe kimlikleri
// çözünürlüğe bağlıdır, başka bir ızgarayla üretilen kümede halkalar
// birleşmezdi.
func (g *Grid) Polygonize(cells []geo.Axial) (geometry.MultiPolygon, error) {
	return geometry.Polygonize(cells, g.hex)
}

// Region, sektör dilimini kaplayan hücreleri döndürür.
//
// Bir hücre, **merkezi** dilim içindeyse sonuca dâhildir. Kenar hücrelerin
// kısmi katkısı burada değil, TA örtüşme oranında ele alınır (ADR-18/3);
// dilim sınırındaki yumuşama ise anten deseninden gelir.
//
// Dönen dilim (r, q) sırasına göre deterministiktir: kütle toplamının kayan
// nokta sırası koşudan koşuya sabit kalır (K10).
func (g *Grid) Region(sec geometry.Sector) []geo.Axial {
	min, max := sec.BoundingBox()

	rowHeight := 1.5 * g.hex.Size()
	colWidth := math.Sqrt(3) * g.hex.Size()

	rMin := int(math.Floor(min.Y/rowHeight)) - 1
	rMax := int(math.Ceil(max.Y/rowHeight)) + 1

	cells := make([]geo.Axial, 0, estimateRegionSize(sec, g.hex.CellAreaM2()))

	for r := rMin; r <= rMax; r++ {
		offset := float64(r) / 2
		qMin := int(math.Floor(min.X/colWidth-offset)) - 1
		qMax := int(math.Ceil(max.X/colWidth-offset)) + 1

		for q := qMin; q <= qMax; q++ {
			a := geo.Axial{Q: q, R: r}
			if sec.Contains(g.hex.Center(a)) {
				cells = append(cells, a)
			}
		}
	}

	sort.Slice(cells, func(i, j int) bool {
		if cells[i].R != cells[j].R {
			return cells[i].R < cells[j].R
		}
		return cells[i].Q < cells[j].Q
	})
	return cells
}

// estimateRegionSize, ön ayırma büyüklüğüdür: dilim alanı / hücre alanı.
func estimateRegionSize(sec geometry.Sector, cellArea float64) int {
	fraction := sec.BeamWidthDeg / 360
	area := math.Pi * sec.RMaxM * sec.RMaxM * fraction
	n := area / cellArea
	if n < 1 || math.IsInf(n, 1) || math.IsNaN(n) {
		return 1
	}
	return int(n) + 1
}
