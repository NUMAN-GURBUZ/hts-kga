// Package geometry, analiz motorunun metrik düzlemdeki alan hesaplarını
// yapar: sektör dilimi, TA halkası ve bunların ızgara hücreleriyle kesişimi.
//
// Tüm koordinatlar ENU metredir (ADR-07). Paket veri kaynağı tanımaz, saf
// geometridir.
package geometry

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// PolygonArea, basit bir çokgenin işaretsiz alanıdır (ayakkabı bağı formülü).
func PolygonArea(poly []geo.Point) float64 {
	if len(poly) < 3 {
		return 0
	}
	sum := 0.0
	for i := range poly {
		a := poly[i]
		b := poly[(i+1)%len(poly)]
		sum += cross(a, b)
	}
	return math.Abs(sum) / 2
}

// DiskPolygonArea, merkezi center olan r yarıçaplı disk ile çokgenin kesişim
// alanını **tam** olarak döndürür.
//
// # Yöntem
//
// Çokgen, merkezden köşelere çizilen üçgenlere ayrılır. Her kenar için, o
// kenarın diskle kesişimi kapalı formülle hesaplanır: kenarın disk içinde
// kalan parçası üçgen alanı, dışında kalan parçaları dairesel dilim alanı
// verir. İşaretli toplam, çokgenin dolanım yönünden bağımsız olarak doğru
// sonucu üretir; mutlak değer sonda alınır.
//
// Alt-örnekleme (hücre içinde n nokta sayıp oranlamak) kasten
// kullanılmamıştır: n bir serbest parametre olurdu ve ağırlığa örnekleme
// gürültüsü katardı (ADR-18/4).
func DiskPolygonArea(poly []geo.Point, center geo.Point, r float64) float64 {
	if len(poly) < 3 || !(r > 0) {
		return 0
	}
	total := 0.0
	for i := range poly {
		a := poly[i].Sub(center)
		b := poly[(i+1)%len(poly)].Sub(center)
		total += edgeDiskArea(a, b, r)
	}
	return math.Abs(total)
}

// AnnulusPolygonArea, çokgenin halka (iç/dış yarıçap) ile kesişim alanıdır.
//
// Halka = dış disk − iç disk. İkisi de aynı çokgenle kesiştirildiğinden fark
// doğrudan halka kesişimini verir.
func AnnulusPolygonArea(poly []geo.Point, center geo.Point, innerM, outerM float64) float64 {
	if outerM <= innerM {
		return 0
	}
	outer := DiskPolygonArea(poly, center, outerM)
	if innerM <= 0 {
		return outer
	}
	inner := DiskPolygonArea(poly, center, innerM)
	if diff := outer - inner; diff > 0 {
		return diff
	}
	return 0
}

// edgeDiskArea, merkezden (origin) a ve b köşelerine uzanan üçgenin diskle
// kesişiminin işaretli alanıdır.
//
// Kenar, çemberle kesişim parametrelerine göre en fazla üç parçaya bölünür:
// [0,t1] dışarıda (dilim), [t1,t2] içeride (üçgen), [t2,1] dışarıda (dilim).
// Parametreler [0,1] aralığına kırpıldığında bu ayrıştırma, kenarın diski hiç
// kesmediği ve tamamen içinde kaldığı durumları da tek formülle kapsar.
func edgeDiskArea(a, b geo.Point, r float64) float64 {
	d := b.Sub(a)
	qa := d.X*d.X + d.Y*d.Y
	if qa == 0 {
		return 0 // dejenere kenar
	}
	qb := 2 * (a.X*d.X + a.Y*d.Y)
	qc := a.X*a.X + a.Y*a.Y - r*r

	t1, t2 := 0.0, 0.0
	if disc := qb*qb - 4*qa*qc; disc > 0 {
		sq := math.Sqrt(disc)
		t1 = clamp01((-qb - sq) / (2 * qa))
		t2 = clamp01((-qb + sq) / (2 * qa))
	}

	p1 := geo.Point{X: a.X + t1*d.X, Y: a.Y + t1*d.Y}
	p2 := geo.Point{X: a.X + t2*d.X, Y: a.Y + t2*d.Y}

	return arcArea(a, p1, r) + cross(p1, p2)/2 + arcArea(p2, b, r)
}

// arcArea, çember üzerindeki iki nokta arasındaki dairesel dilimin işaretli
// alanıdır: ½·r²·θ.
//
// Noktalar tam olarak çember üzerinde olmasa da (kenar diski hiç kesmiyorsa
// dışarıdadırlar) aralarındaki açı doğru dilimi verir; bu, ayrıştırmanın
// tek formülle çalışmasını sağlayan özelliktir.
func arcArea(a, b geo.Point, r float64) float64 {
	if a == b {
		return 0
	}
	return 0.5 * r * r * math.Atan2(cross(a, b), dot(a, b))
}

// OverlapRatio, çokgenin halkayla örtüşme oranıdır [0,1] (ADR-18/3).
//
// TA penceresinin ağırlığı budur: bir ızgara hücresi halkaya ne kadar
// giriyorsa o kadar ağırlık alır. İkili merkez-içinde-mi testi kullanılmaz —
// LTE'de halka (78,12 m) ızgara adımından (100 m) ince olduğu için ikili test
// halkayı çoğu olayda ıskalardı.
func OverlapRatio(poly []geo.Point, center geo.Point, innerM, outerM float64) float64 {
	area := PolygonArea(poly)
	if area <= 0 {
		return 0
	}
	ratio := AnnulusPolygonArea(poly, center, innerM, outerM) / area
	switch {
	case ratio < ratioEpsilon:
		return 0
	case ratio > 1:
		return 1 // kayan nokta payı
	default:
		return ratio
	}
}

// ratioEpsilon, örtüşme oranının sıfır sayıldığı eşiktir.
//
// Halka, iç ve dış diskin farkıdır. Hücre her iki diskin de tamamen içinde
// kaldığında iki alan birbirine eşittir ve fark, kayan nokta iptalinden
// (catastrophic cancellation) doğan ~10⁻¹⁶ mertebesinde bir artık bırakır.
// Bu artık sıfır sayılmazsa "halka bölgeyle kesişiyor" kararı yanlış çıkar ve
// T-E03-04'ün ∅ geri düşüşü hiç tetiklenmez.
//
// Eşik, gerçek bir örtüşmenin alt sınırının (bir hücreye teğet geçen halka
// bile ~10⁻⁶ oran verir) çok altında, iptal artığının (~10⁻¹⁵) çok üstündedir.
const ratioEpsilon = 1e-12

func cross(a, b geo.Point) float64 { return a.X*b.Y - a.Y*b.X }
func dot(a, b geo.Point) float64   { return a.X*b.X + a.Y*b.Y }

func clamp01(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
