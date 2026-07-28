// T-E03-10 · T-E03-12 — Kümülatif kontur ve kütle ağırlıklı merkez.
//
// Kütle haritası (Estimate çıktısı) bir olasılık dağılımıdır; saklanan şey ise
// bölgedir. Dönüşüm plan BÖLÜM E.3'ün ilk iki adımıdır: hücreleri kütleye göre
// azalan sırala, hedef kütle (%50 / %90 / %95) toplanana kadar seç.
//
// Bu, "en yüksek olasılık yoğunluklu bölge" (HPD) tanımıdır: aynı kütleyi
// kaplayan tüm bölgeler arasında **alanı en küçük** olanıdır. K2/K3 daralma
// iddiası bu tanıma dayanır — daha gevşek bir seçim (örneğin yarıçapa göre
// büyütme) aynı güveni daha büyük alanla verirdi ve iddiayı zayıflatırdı.
//
// # İç içelik yapı gereğidir (PBT #2)
//
// Üç seviye **aynı sıralanmış listenin** ön ekleridir. Dolayısıyla
// C(%50) ⊆ C(%90) ⊆ C(%95) küme düzeyinde kesindir; alan sıralaması bunun
// sonucudur, ayrıca sağlanması gereken bir koşul değil. Test yine de
// poligon düzeyinde doğrular (yalnız küme kapsaması değil, üretilen halkaların
// gerçekten iç içe olduğu).
//
// # Eşit kütleli hücrelerde sıra
//
// Simetrik yapılandırmalarda (altın senaryo, σ=0) çok sayıda hücre bit
// düzeyinde aynı kütleye sahiptir. Yalnızca kütleye göre sıralamak, Go'nun
// kararsız (unstable) sıralamasıyla koşudan koşuya farklı hücre seçimi
// demektir — K10 düşerdi. Eşitlik (r, q) ile kırılır.

package core

import (
	"fmt"
	"math"
	"sort"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Contour, tek bir güven seviyesinin hücre kümesidir.
type Contour struct {
	// Level, hedeflenen kümülatif kütledir (0,1] — `estimates.confidence`.
	Level float64
	// Cells, seçilen hücrelerdir; kütleye göre azalan, eşitlikte (r,q) sıralı.
	Cells []geo.Axial
	// Mass, seçilen hücrelerin toplam kütlesidir (≥ Level, son hücre eşiği aşar).
	Mass float64
	// Centroid, seçilen kütlenin ağırlık merkezidir (ENU metre — ADR-10).
	Centroid geo.Point
}

// Contours, kütle haritasından istenen seviyelerin konturlarını çıkarır.
//
// Seviyeler artan sırada döner (girdi sırası ne olursa olsun): ön ek ilişkisi
// ancak bu sırada anlamlıdır ve çağıranın sırayı bozması iç içelik değişmezini
// gizlice kırardı.
func Contours(mass map[geo.Axial]float64, grid *density.Grid, levels []float64) ([]Contour, error) {
	if grid == nil {
		return nil, fmt.Errorf("kontur: ızgara zorunlu")
	}
	if len(mass) == 0 {
		return nil, fmt.Errorf("kontur: kütle haritası boş")
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("kontur: seviye listesi boş")
	}

	sorted := append([]float64(nil), levels...)
	sort.Float64s(sorted)
	for _, l := range sorted {
		if !(l > 0 && l <= 1) || math.IsNaN(l) {
			return nil, fmt.Errorf("kontur: seviye (0,1] aralığında olmalı (%g)", l)
		}
	}

	ranked := rankByMass(mass)

	out := make([]Contour, 0, len(sorted))
	for _, level := range sorted {
		cells, selected := selectPrefix(ranked, level)
		if len(cells) == 0 {
			return nil, fmt.Errorf("kontur: seviye %.2f için hiç hücre seçilemedi "+
				"(toplam kütle %g)", level, selected)
		}
		out = append(out, Contour{
			Level:    level,
			Cells:    cells,
			Mass:     selected,
			Centroid: massCentroid(ranked[:len(cells)], grid, selected),
		})
	}
	return out, nil
}

// rankedCell, sıralama için hücre–kütle çiftidir.
type rankedCell struct {
	cell geo.Axial
	mass float64
}

// rankByMass, kütle haritasını azalan kütle sırasına dizer.
//
// Harita yinelemesi Go'da rastgeledir; bu yüzden sıralama tam bir tanım
// (kütle, sonra r, sonra q) üzerinden yapılır ve sonuç haritanın iç düzeninden
// bağımsızdır — K10'un ön koşulu.
func rankByMass(mass map[geo.Axial]float64) []rankedCell {
	ranked := make([]rankedCell, 0, len(mass))
	for a, m := range mass {
		ranked = append(ranked, rankedCell{cell: a, mass: m})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].mass != ranked[j].mass {
			return ranked[i].mass > ranked[j].mass
		}
		if ranked[i].cell.R != ranked[j].cell.R {
			return ranked[i].cell.R < ranked[j].cell.R
		}
		return ranked[i].cell.Q < ranked[j].cell.Q
	})
	return ranked
}

// selectPrefix, kümülatif kütle hedefi aşana kadarki ön eki döndürür.
//
// Eşiği aşan hücre **dâhil edilir**: %90 hedefi, en az %90 kütle demektir.
// Tolerans (MassTolerance) Σ mass = 1'in kayan nokta payını karşılar; level=1
// istendiğinde toplam 1−1e-12 olsa bile tüm hücreler seçilir, hata dönmez.
func selectPrefix(ranked []rankedCell, level float64) ([]geo.Axial, float64) {
	cum, compensation := 0.0, 0.0
	for i, rc := range ranked {
		y := rc.mass - compensation
		t := cum + y
		compensation = (t - cum) - y
		cum = t

		if cum >= level-density.MassTolerance {
			cells := make([]geo.Axial, i+1)
			for j := 0; j <= i; j++ {
				cells[j] = ranked[j].cell
			}
			return cells, cum
		}
	}

	cells := make([]geo.Axial, len(ranked))
	for j := range ranked {
		cells[j] = ranked[j].cell
	}
	return cells, cum
}

// massCentroid, seçilen hücrelerin kütle ağırlıklı merkezidir (ADR-10):
//
//	centroid = Σ(mass_i · center_i) / Σ mass_i
//
// # Neden tüm dağılım değil de konturun kendisi
//
// ADR-10 formülü verir ama toplamın hangi küme üzerinde olduğunu söylemez.
// Merkez, satır başına saklanır (`estimates.centroid`) ve satır bir **bölgeyi**
// tanımlar; o bölgenin merkezi, bölgenin kendi kütlesinden türetilmelidir.
// Tüm dağılım kullanılsaydı üç M satırı aynı merkezi taşırdı ve %50 konturunun
// merkezi, o konturun dışında kalabilirdi (çok tepeli kütlede gerçekten olur).
//
// Pratikte fark küçüktür: %95 konturu kütlenin neredeyse tamamını içerdiği
// için merkezi tam dağılımın merkezine yakınsar.
func massCentroid(selected []rankedCell, grid *density.Grid, total float64) geo.Point {
	if !(total > 0) {
		return geo.Point{}
	}
	var sx, sy float64
	for _, rc := range selected {
		c := grid.Center(rc.cell)
		sx += rc.mass * c.X
		sy += rc.mass * c.Y
	}
	return geo.Point{X: sx / total, Y: sy / total}
}
