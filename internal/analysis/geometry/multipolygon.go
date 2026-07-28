// T-E03-11 — Poligon tipleri, alan ve izdüşüm.
//
// Bu dosya iki dünyayı ayrı tutar:
//
//	MultiPolygon     — ENU düzlemi, metre    (tüm geometri matematiği burada)
//	GeoMultiPolygon  — WGS84, derece         (yalnızca saklama ve alan)
//
// Ayrım ADR-07'nin kuralıdır: hesap ENU'da yapılır, WGS84'e dönüşüm **son
// adımdır**. Karışık bir tip (bazı alanlar derece, bazıları metre) sessiz
// birim hatalarının en verimli kaynağıdır; tip sistemi bunu engellesin diye
// iki ayrı tip vardır.
//
// # Alan neden ENU'da değil WGS84'te hesaplanır (ADR-21)
//
// ENU izdüşümü eşdikdörtgenseldir ve sabit ölçek katsayıları kullanır
// (ADR-07). Sprint 1'de ölçülen sapma kuzey ekseninde %0,59, doğu ekseninde
// %0,11'dir. Düzlemsel alan (shoelace) bu sapmayı iki eksenin çarpımı olarak
// taşır (~%0,7) ve `area_km2` sütununa sistematik bir yanlılık olarak yazılırdı.
//
// K2/K3 kriterleri **alan oranlarına** dayandığı için bu yanlılık pay ve
// paydada büyük ölçüde sadeleşirdi — ama "büyük ölçüde" yeterli değildir:
// M ve B0 farklı büyüklükte bölgelerdir, ölçek sapması enlemle değiştiği için
// tam sadeleşmez. Alan bu yüzden saklanan WGS84 köşelerinden küresel formülle
// hesaplanır: ne saklanıyorsa onun alanı ölçülür.
//
// Düzlemsel alan (AreaM2) yalnızca iç tutarlılık denetimleri için durur.

package geometry

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// authalicRadiusM, WGS84 elipsoiduyla **eşit alanlı** kürenin yarıçapıdır.
//
// Küresel formülün elipsoit alanına en yakın sonucu vermesi için yarıçap
// budur; ortalama yarıçap (6.371.008,8 m) değil. Birkaç km²'lik yerel
// poligonlarda kalan fark %0,1'in altındadır — PostGIS `ST_Area(geography)`
// (varsayılan: elipsoit) ile çapraz kontrolün %1 toleransı bunu kapsar.
const authalicRadiusM = 6_371_007.2

// ─── ENU tarafı ───────────────────────────────────────────────────────────────

// Ring, kapalı bir halkadır (ENU metre): ilk ve son köşe aynıdır.
//
// Yönelim anlamlıdır: dış halka saat yönünün tersine (pozitif işaretli alan),
// delik halkası saat yönünde sıralanır. Bu, OGC'nin ve GeoJSON RFC 7946'nın
// sağ el kuralıyla aynıdır; PostGIS'e dönüştürmeden önce ayrıca düzeltme
// gerekmez.
type Ring []geo.Point

// SignedAreaM2, halkanın işaretli düzlemsel alanıdır (shoelace).
//
// Pozitif → saat yönünün tersi (dış halka), negatif → saat yönü (delik).
func (r Ring) SignedAreaM2() float64 {
	if len(r) < 4 { // kapalı bir halka en az 3 farklı köşe + tekrar eden ilk köşe
		return 0
	}
	sum := 0.0
	for i := 0; i < len(r)-1; i++ {
		sum += r[i].X*r[i+1].Y - r[i+1].X*r[i].Y
	}
	return sum / 2
}

// Contains, noktanın halka içinde olup olmadığını bildirir (ışın atma).
//
// Delik atamasında kullanılır. Sınır üzerindeki noktalar için sonuç tanımsızdır;
// çağıran, hücre merkezleri gibi sınırda olmayan noktalar kullanır.
func (r Ring) Contains(p geo.Point) bool {
	inside := false
	for i := 0; i < len(r)-1; i++ {
		a, b := r[i], r[i+1]
		if (a.Y > p.Y) == (b.Y > p.Y) {
			continue
		}
		x := a.X + (p.Y-a.Y)/(b.Y-a.Y)*(b.X-a.X)
		if p.X < x {
			inside = !inside
		}
	}
	return inside
}

// Polygon, bir dış halka ve sıfır veya daha çok delikten oluşur (ENU metre).
type Polygon struct {
	Exterior Ring
	Holes    []Ring
}

// AreaM2, deliklerin düşüldüğü düzlemsel alandır.
func (p Polygon) AreaM2() float64 {
	area := math.Abs(p.Exterior.SignedAreaM2())
	for _, h := range p.Holes {
		area -= math.Abs(h.SignedAreaM2())
	}
	return area
}

// MultiPolygon, ayrık poligonların kümesidir (ENU metre).
type MultiPolygon []Polygon

// PartCount, bileşen sayısıdır → `estimates.part_count` (K8).
func (m MultiPolygon) PartCount() int { return len(m) }

// AreaM2, toplam düzlemsel alandır (iç tutarlılık denetimi).
func (m MultiPolygon) AreaM2() float64 {
	total := 0.0
	for _, p := range m {
		total += p.AreaM2()
	}
	return total
}

// VertexCount, toplam köşe sayısıdır.
//
// Saklanan geometrinin hacim ölçümü buna dayanır (Gün 6 disk ölçümü):
// hex kafeste sınır her köşede 60° döndüğü için doğrusal köşe sadeleştirmesi
// hiçbir şey kazandırmaz — köşe sayısı, sınırdaki hücre sayısının ta kendisidir.
func (m MultiPolygon) VertexCount() int {
	n := 0
	for _, p := range m {
		n += len(p.Exterior)
		for _, h := range p.Holes {
			n += len(h)
		}
	}
	return n
}

// Contains, noktanın geometrinin içinde olup olmadığını bildirir.
//
// Dış halkanın içinde ve hiçbir deliğin içinde değilse doğrudur. İç içelik
// (PBT #2) ve merkez denetimleri bunu kullanır; sınır üzerindeki noktalarda
// sonuç tanımsızdır.
func (m MultiPolygon) Contains(p geo.Point) bool {
	for _, poly := range m {
		if !poly.Exterior.Contains(p) {
			continue
		}
		inHole := false
		for _, h := range poly.Holes {
			if h.Contains(p) {
				inHole = true
				break
			}
		}
		if !inHole {
			return true
		}
	}
	return false
}

// Centroid, alan ağırlıklı geometrik merkezdir (ENU metre).
//
// **Kütle ağırlıklı merkez değildir** (ADR-10). Bu merkez B0/B1 taban çizgileri
// içindir: onların içinde bir olasılık dağılımı yoktur, tek tanımlı merkez
// geometrik olandır. M yönteminin merkezi kütleden türetilir (core.Contour).
func (m MultiPolygon) Centroid() geo.Point {
	var sx, sy, area float64
	for _, p := range m {
		for _, r := range append([]Ring{p.Exterior}, p.Holes...) {
			cx, cy, a := ringMoments(r)
			sx += cx
			sy += cy
			area += a
		}
	}
	if area == 0 {
		return fallbackCentroid(m)
	}
	return geo.Point{X: sx / (6 * area), Y: sy / (6 * area)}
}

// ringMoments, halkanın işaretli alanını ve ağırlıklandırılmamış moment
// toplamlarını döndürür. Delikler negatif işaretli alanlarıyla katkı verir;
// bu yüzden dış halka ve delikler aynı formülden geçirilebilir.
func ringMoments(r Ring) (sx, sy, area float64) {
	for i := 0; i < len(r)-1; i++ {
		cross := r[i].X*r[i+1].Y - r[i+1].X*r[i].Y
		sx += (r[i].X + r[i+1].X) * cross
		sy += (r[i].Y + r[i+1].Y) * cross
		area += cross
	}
	return sx, sy, area / 2
}

// fallbackCentroid, alanı sıfır olan dejenere geometride köşe ortalamasıdır.
//
// Üretimde erişilmemesi beklenir (hücre kümesi boş olamaz); sessizce (0,0)
// döndürmemek için vardır — origin, senaryo merkezidir ve inandırıcı görünen
// yanlış bir cevap olurdu.
func fallbackCentroid(m MultiPolygon) geo.Point {
	var sx, sy float64
	var n int
	for _, p := range m {
		for _, v := range p.Exterior {
			sx, sy, n = sx+v.X, sy+v.Y, n+1
		}
	}
	if n == 0 {
		return geo.Point{}
	}
	return geo.Point{X: sx / float64(n), Y: sy / float64(n)}
}

// Project, geometriyi WGS84'e taşır (ADR-07 son adım).
func (m MultiPolygon) Project(pr *geo.Projector) GeoMultiPolygon {
	out := make(GeoMultiPolygon, len(m))
	for i, p := range m {
		out[i] = GeoPolygon{Exterior: projectRing(pr, p.Exterior)}
		if len(p.Holes) > 0 {
			out[i].Holes = make([]GeoRing, len(p.Holes))
			for j, h := range p.Holes {
				out[i].Holes[j] = projectRing(pr, h)
			}
		}
	}
	return out
}

func projectRing(pr *geo.Projector, r Ring) GeoRing {
	out := make(GeoRing, len(r))
	for i, p := range r {
		out[i] = pr.Inverse(p)
	}
	return out
}

// ─── WGS84 tarafı ─────────────────────────────────────────────────────────────

// GeoRing, kapalı halkadır (WGS84 derece).
type GeoRing []geo.WGS84

// GeoPolygon, WGS84 poligonudur.
type GeoPolygon struct {
	Exterior GeoRing
	Holes    []GeoRing
}

// GeoMultiPolygon, `estimates.geometry` sütununa yazılan geometridir.
type GeoMultiPolygon []GeoPolygon

// PartCount, bileşen sayısıdır.
func (g GeoMultiPolygon) PartCount() int { return len(g) }

// VertexCount, toplam köşe sayısıdır.
func (g GeoMultiPolygon) VertexCount() int {
	n := 0
	for _, p := range g {
		n += len(p.Exterior)
		for _, h := range p.Holes {
			n += len(h)
		}
	}
	return n
}

// AreaKM2, küre üzerindeki toplam alandır (km²) — `estimates.area_km2`.
//
// Delikler düşülür. Bileşenler ayrık olduğu için toplam, bileşen alanlarının
// toplamıdır.
func (g GeoMultiPolygon) AreaKM2() float64 {
	total := 0.0
	for _, p := range g {
		area := math.Abs(sphericalAreaM2(p.Exterior))
		for _, h := range p.Holes {
			area -= math.Abs(sphericalAreaM2(h))
		}
		total += area
	}
	return total / 1e6
}

// sphericalAreaM2, küresel poligonun işaretli alanıdır (Chamberlain & Duquette).
//
//	A = R²/2 · Σ (λ_{i+1} − λ_i)·(sin φ_i + sin φ_{i+1})
//
// Boylam farkı (−π, π] aralığına indirgenir: senaryolar antimeridyeni
// kesmez, ama indirgeme olmadan formül orada sessizce dünya çapında bir alan
// döndürürdü.
func sphericalAreaM2(r GeoRing) float64 {
	if len(r) < 4 {
		return 0
	}
	sum := 0.0
	for i := 0; i < len(r)-1; i++ {
		lon1 := r[i].Lon * math.Pi / 180
		lon2 := r[i+1].Lon * math.Pi / 180
		lat1 := r[i].Lat * math.Pi / 180
		lat2 := r[i+1].Lat * math.Pi / 180

		dLon := math.Mod(lon2-lon1+3*math.Pi, 2*math.Pi) - math.Pi
		sum += dLon * (math.Sin(lat1) + math.Sin(lat2))
	}
	return authalicRadiusM * authalicRadiusM * sum / 2
}
