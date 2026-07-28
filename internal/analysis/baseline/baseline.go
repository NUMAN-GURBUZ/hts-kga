// Package baseline, karşılaştırma taban çizgilerini üretir (T-E03-09).
//
// Çalışmanın iddiası "olasılıksal kütle modeli, konum belirsizliğini
// operatörün elindeki kaba yöntemlere göre daraltır"dır. İddia ancak o kaba
// yöntemler **aynı veriden, aynı kodda** üretilirse ölçülebilir:
//
//	B0 — naif daire  : direk merkezli, r_max yarıçaplı disk.
//	                   "Abone bu hücrenin kapsama alanındaydı" demenin
//	                   geometrik karşılığı; sektör bilgisi kullanılmaz.
//	B1 — sektör dilimi: azimut ± hüzme/2 açıklığında, r_max yarıçaplı dilim.
//	                   Operatörün elindeki en iyi geometrik kestirim.
//
// K2, M@90'ı B0'a karşı ölçer (≥ %75 daralma). Asıl iddia K3'tür: M@90 vs B1
// (TA var ≥ %50, TA yok ≥ %20). B1 zaten dar bir bölge olduğu için buradaki
// kazanç modelin gerçek katkısıdır.
//
// # Taban çizgileri TA'yı kullanmaz
//
// B0 ve B1, kaydın `ta_value` alanını **görmez**. Bu bilinçlidir: taban
// çizgisi, TA'yı işlemeyen bir operatör pratiğini temsil eder. TA'nın katkısı
// K3'ün "TA var / TA yok" senaryo ayrımında zaten ölçülür — taban çizgisine de
// TA eklemek, ölçülmek istenen farkı iki taraftan da eksiltirdi.
//
// # Yay ayrıklaştırması
//
// Daire ve yay 1°'lik adımlarla poligonlaştırılır. İçten çizilen çokgen
// gerçek daireden %0,005 küçüktür (N=360 için 2π²/3N²); K2/K3 eşikleri %20–75
// mertebesinde olduğu için bu fark ölçüme giremez. Yön, üretilen halkanın
// saat yönünün tersi (dış halka) olması için azalan azimutla yürütülür.
package baseline

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// arcStepDeg, yay ayrıklaştırma adımıdır (derece).
const arcStepDeg = 1.0

// B0, naif daireyi üretir: direk merkezli, r_max yarıçaplı disk (ENU metre).
func B0(site geo.Point, rMaxM float64) (geometry.MultiPolygon, error) {
	if !(rMaxM > 0) || math.IsInf(rMaxM, 0) || math.IsNaN(rMaxM) {
		return nil, fmt.Errorf("B0: r_max pozitif ve sonlu olmalı (%g)", rMaxM)
	}

	ring := arc(site, rMaxM, 360, 0, false)
	return geometry.MultiPolygon{{Exterior: ring}}, nil
}

// B1, sektör dilimini üretir (ENU metre).
//
// Dilim, kütle üretiminde kullanılan **arama bölgesiyle aynı** geometridir
// (geometry.Sector): taban çizgisi ile model aynı sektör tanımını paylaşır,
// aksi hâlde K3 farkı geometri tanımından da beslenirdi.
func B1(sec geometry.Sector) (geometry.MultiPolygon, error) {
	if !(sec.RMaxM > 0) || math.IsInf(sec.RMaxM, 0) {
		return nil, fmt.Errorf("B1: r_max pozitif ve sonlu olmalı (%g)", sec.RMaxM)
	}
	if !(sec.BeamWidthDeg > 0 && sec.BeamWidthDeg <= 360) {
		return nil, fmt.Errorf("B1: hüzme genişliği (0,360] olmalı (%g)", sec.BeamWidthDeg)
	}
	if sec.BeamWidthDeg >= 360 {
		return B0(sec.Site, sec.RMaxM) // tam daire: tepe noktası yoktur
	}

	ring := arc(sec.Site, sec.RMaxM, sec.BeamWidthDeg, sec.AzimuthDeg, true)
	return geometry.MultiPolygon{{Exterior: ring}}, nil
}

// arc, verilen açıklıkta yayı halkaya çevirir.
//
// spanDeg dilimin toplam açıklığı, centerDeg hüzme eksenidir. apex true ise
// halka direğin kendisinden başlar (dilim), false ise yay kapalı bir daire
// oluşturur.
//
// Azimut **azalan** yönde yürütülür: pusula açısı saat yönünde büyüdüğü için
// azalan azimut, düzlemde saat yönünün tersidir (OGC dış halka yönelimi).
func arc(center geo.Point, radius, spanDeg, centerDeg float64, apex bool) geometry.Ring {
	steps := int(math.Ceil(spanDeg / arcStepDeg))
	if steps < 3 {
		steps = 3
	}

	ring := make(geometry.Ring, 0, steps+3)
	if apex {
		ring = append(ring, center)
	}

	start := centerDeg + spanDeg/2
	for i := 0; i <= steps; i++ {
		if !apex && i == steps {
			break // tam dairede son nokta ilk noktayla çakışır
		}
		deg := start - spanDeg*float64(i)/float64(steps)
		rad := deg * math.Pi / 180
		sin, cos := math.Sincos(rad)
		ring = append(ring, geo.Point{X: center.X + radius*sin, Y: center.Y + radius*cos})
	}

	ring = append(ring, ring[0]) // halkayı kapat
	return ring
}
