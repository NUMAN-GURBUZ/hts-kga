// Package geo, WGS84 coğrafi koordinatlar ile yerel ENU (East-North-Up)
// düzlemi arasındaki dönüşümleri sağlar.
//
// T-E02-02 — ADR-07 (Y-01, E-01): Tüm hex ızgara, komşuluk ve mesafe hesapları
// yerel ENU düzleminde **metre** cinsinden yapılır; WGS84'e dönüşüm yalnızca
// son adımda (GEOGRAPHY olarak saklama) uygulanır.
//
// Neden ENU: enlem–boylam ölçek farkı (40°N'de boylam derecesi ~85 km, enlem
// derecesi ~111 km) bu katmanda bir kez ele alınır, ızgara matematiğine sızmaz.
package geo

import (
	"fmt"
	"math"
)

// ─── Sabitler (ADR-07) ────────────────────────────────────────────────────────

const (
	// metersPerDegreeLat, bir enlem derecesinin metre karşılığı (ADR-07).
	metersPerDegreeLat = 110_540.0

	// metersPerDegreeLonEquator, ekvatorda bir boylam derecesinin metre
	// karşılığı. Yerel ölçek cos(lat0) ile çarpılarak elde edilir (ADR-07).
	metersPerDegreeLonEquator = 111_320.0

	// maxOriginLatDeg, eşdikdörtgensel yaklaşımın geçerli kabul edildiği en
	// yüksek origin enlemi. Kutuplara yaklaşırken cos(lat0) → 0 olur ve ters
	// dönüşüm sayısal olarak patlar; senaryolar orta enlemlerdedir.
	maxOriginLatDeg = 85.0
)

// ─── Tipler ───────────────────────────────────────────────────────────────────

// WGS84, coğrafi koordinattır (derece).
type WGS84 struct {
	Lat float64
	Lon float64
}

// Point, yerel ENU düzleminde bir noktadır (metre).
// X doğu (East), Y kuzey (North) yönünü gösterir; Up bileşeni bu projede
// kullanılmaz (tüm hesaplar 2 boyutludur).
type Point struct {
	X float64
	Y float64
}

// Sub, iki ENU noktası arasındaki vektör farkını döndürür.
func (p Point) Sub(q Point) Point {
	return Point{X: p.X - q.X, Y: p.Y - q.Y}
}

// Norm, noktanın orijine olan düzlemsel uzaklığıdır (metre).
func (p Point) Norm() float64 {
	return math.Hypot(p.X, p.Y)
}

// Distance, iki ENU noktası arasındaki düzlemsel mesafedir (metre).
func Distance(a, b Point) float64 {
	return math.Hypot(a.X-b.X, a.Y-b.Y)
}

// ─── İzdüşüm ──────────────────────────────────────────────────────────────────

// Projector, sabit bir origin etrafında WGS84 ↔ ENU dönüşümü yapar.
//
// Origin senaryo config'inden gelir (area.origin_lat, area.origin_lon).
// Değişmezdir (immutable) ve eşzamanlı kullanımda güvenlidir: kurulumdan sonra
// hiçbir alan yazılmaz.
type Projector struct {
	originLat float64
	originLon float64

	// metersPerDegLon, origin enlemindeki boylam derecesi ölçeğidir.
	// Kurulumda bir kez hesaplanır (her dönüşümde cos çağrısı yapılmaz).
	metersPerDegLon float64
}

// NewProjector, verilen origin için bir izdüşüm oluşturur.
//
// Origin geçerli bir WGS84 koordinatı olmalı ve |lat| ≤ 85° koşulunu
// sağlamalıdır (eşdikdörtgensel yaklaşımın geçerlilik sınırı).
func NewProjector(originLat, originLon float64) (*Projector, error) {
	if math.IsNaN(originLat) || math.IsNaN(originLon) {
		return nil, fmt.Errorf("ENU origin NaN olamaz (lat=%g, lon=%g)", originLat, originLon)
	}
	if math.Abs(originLat) > maxOriginLatDeg {
		return nil, fmt.Errorf(
			"ENU origin enlemi |%g| > %g°: eşdikdörtgensel yaklaşım kutuplarda geçerli değil",
			originLat, maxOriginLatDeg)
	}
	if math.Abs(originLon) > 180 {
		return nil, fmt.Errorf("ENU origin boylamı [-180,180] aralığında olmalı (%g)", originLon)
	}

	return &Projector{
		originLat:       originLat,
		originLon:       originLon,
		metersPerDegLon: math.Cos(originLat*math.Pi/180) * metersPerDegreeLonEquator,
	}, nil
}

// Origin, izdüşümün referans noktasını döndürür.
func (p *Projector) Origin() WGS84 {
	return WGS84{Lat: p.originLat, Lon: p.originLon}
}

// Forward, WGS84 → yerel ENU (metre) dönüşümüdür (ADR-07):
//
//	x = (lon − lon0) · cos(lat0) · 111_320
//	y = (lat − lat0) · 110_540
func (p *Projector) Forward(g WGS84) Point {
	return Point{
		X: (g.Lon - p.originLon) * p.metersPerDegLon,
		Y: (g.Lat - p.originLat) * metersPerDegreeLat,
	}
}

// Inverse, yerel ENU (metre) → WGS84 dönüşümüdür. Forward'ın tam tersidir:
//
//	lon = lon0 + x / (cos(lat0) · 111_320)
//	lat = lat0 + y / 110_540
func (p *Projector) Inverse(pt Point) WGS84 {
	return WGS84{
		Lat: p.originLat + pt.Y/metersPerDegreeLat,
		Lon: p.originLon + pt.X/p.metersPerDegLon,
	}
}

// ForwardAll, birden çok koordinatı tek seferde ENU'ya çevirir.
// Poligon köşeleri gibi dizi dönüşümlerinde ayırma (allocation) tasarrufu sağlar.
func (p *Projector) ForwardAll(gs []WGS84) []Point {
	pts := make([]Point, len(gs))
	for i, g := range gs {
		pts[i] = p.Forward(g)
	}
	return pts
}

// InverseAll, birden çok ENU noktasını tek seferde WGS84'e çevirir.
// Kontur poligonlarının saklamadan önceki son dönüşümünde kullanılır (ADR-07/4).
func (p *Projector) InverseAll(pts []Point) []WGS84 {
	gs := make([]WGS84, len(pts))
	for i, pt := range pts {
		gs[i] = p.Inverse(pt)
	}
	return gs
}
