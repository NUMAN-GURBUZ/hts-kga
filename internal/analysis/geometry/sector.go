// T-E03-03 — Sektör dilimi geometrisi.
//
// Kayıt, kullanıcının `s` hücresine bağlı olduğunu söyler. Bu bilginin
// geometrik karşılığı, plan BÖLÜM E.1/2'ye göre bir dairesel dilimdir:
// direğin çevresinde, azimut ± hüzme_genişliği/2 açıklığında, r_max
// yarıçapında bir bölge.
//
// Dilim **kütlenin taşıyıcısı değil, arama bölgesidir**: hangi ızgara
// hücrelerinin değerlendirileceğini belirler. Hücrelerin ağırlığı sert bir
// dilim testinden değil, anten deseninden (w_ang) ve radyal bileşenden
// (w_rad) gelir. Bu ayrım önemlidir — sert kenarlı bir dilim, hüzme
// sınırında gerçek konumu keserdi; anten deseni ise oraya küçük ama sıfır
// olmayan ağırlık verir.

package geometry

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Sector, bir sektörün kapsama dilimidir (ENU metre).
type Sector struct {
	// Site, direğin konumudur.
	Site geo.Point
	// AzimuthDeg, hüzme eksenidir (kuzeyden saat yönünde).
	AzimuthDeg float64
	// BeamWidthDeg, yatay 3 dB hüzme genişliğidir.
	BeamWidthDeg float64
	// RMaxM, kapsama yarıçapıdır.
	RMaxM float64
}

// NewSector, dilim tanımını doğrulayarak kurar.
func NewSector(site geo.Point, azimuthDeg, beamWidthDeg, rMaxM float64) (Sector, error) {
	if !(beamWidthDeg > 0 && beamWidthDeg <= 360) {
		return Sector{}, fmt.Errorf("sektör: hüzme genişliği (0,360] olmalı (%g)", beamWidthDeg)
	}
	if !(rMaxM > 0) || math.IsInf(rMaxM, 0) {
		return Sector{}, fmt.Errorf("sektör: r_max pozitif ve sonlu olmalı (%g)", rMaxM)
	}
	if math.IsNaN(azimuthDeg) || math.IsInf(azimuthDeg, 0) {
		return Sector{}, fmt.Errorf("sektör: azimut sonlu olmalı (%g)", azimuthDeg)
	}
	return Sector{
		Site:         site,
		AzimuthDeg:   rf.WrapAngleDeg(azimuthDeg),
		BeamWidthDeg: beamWidthDeg,
		RMaxM:        rMaxM,
	}, nil
}

// HalfWidthDeg, dilimin eksenden itibaren yarı açıklığıdır.
func (s Sector) HalfWidthDeg() float64 { return s.BeamWidthDeg / 2 }

// OffAxisDeg, verilen noktanın hüzme ekseninden sapmasını döndürür
// (−180, 180]. Anten deseninin girdisi budur.
//
// Direğin tam üzerindeki nokta için sapma 0 kabul edilir: yön tanımsızdır,
// ama o noktada anten deseni de anlamsızdır (yatay mesafe sıfır).
func (s Sector) OffAxisDeg(p geo.Point) float64 {
	d := p.Sub(s.Site)
	if d.X == 0 && d.Y == 0 {
		return 0
	}
	return rf.WrapAngleDeg(rf.BearingDeg(d.X, d.Y) - s.AzimuthDeg)
}

// DistanceM, noktanın direğe yatay mesafesidir.
func (s Sector) DistanceM(p geo.Point) float64 { return geo.Distance(s.Site, p) }

// Contains, noktanın dilim içinde olup olmadığını bildirir.
//
// Arama bölgesinin tanımıdır; ağırlık değil. Sınırlar dâhildir.
func (s Sector) Contains(p geo.Point) bool {
	if s.DistanceM(p) > s.RMaxM {
		return false
	}
	if s.BeamWidthDeg >= 360 {
		return true
	}
	return math.Abs(s.OffAxisDeg(p)) <= s.HalfWidthDeg()
}

// Centroid, dilimin alan ağırlık merkezidir (ENU metre).
//
// Dairesel bir dilimin ağırlık merkezi, tepe noktasından hüzme ekseni boyunca
//
//	d = (2·R·sin α) / (3·α)        α = yarı açıklık (radyan)
//
// uzaklıktadır. 65°'lik hüzmede bu ≈ 0,63·R'dir.
//
// Komşu kümesi seçimi (ADR-03, T-E03-07) bu noktaya göre sıralanır: direğin
// kendisi seçilseydi bölgenin uzak yarısına hâkim olan hücreler gözden
// kaçardı, r_max ucu seçilseydi yakın bölge. Kapalı formül olduğu için
// serbest parametre içermez.
func (s Sector) Centroid() geo.Point {
	alpha := s.HalfWidthDeg() * math.Pi / 180
	if alpha <= 0 || alpha >= math.Pi {
		return s.Site // tam daire ya da dejenere: merkez direğin kendisidir
	}
	d := 2 * s.RMaxM * math.Sin(alpha) / (3 * alpha)

	rad := s.AzimuthDeg * math.Pi / 180
	return geo.Point{
		X: s.Site.X + d*math.Sin(rad),
		Y: s.Site.Y + d*math.Cos(rad),
	}
}

// BoundingBox, dilimi çevreleyen eksen hizalı kutuyu döndürür (ENU metre).
//
// Izgara üretimi (T-E03-05) bu kutuyu tarar, sonra Contains ile eler. Kutu
// dilimin gerçek uç noktalarından hesaplanır: dilimin kenar uçları, yayın
// üzerindeki noktalar ve — yay ilgili eksen yönünü içeriyorsa — o eksenin uç
// noktası. Kaba bir "merkez ± r_max" kutusu kentselde dört kat fazla hücre
// taratırdı.
func (s Sector) BoundingBox() (min, max geo.Point) {
	minX, minY := 0.0, 0.0
	maxX, maxY := 0.0, 0.0

	consider := func(x, y float64) {
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}

	half := s.HalfWidthDeg()
	if s.BeamWidthDeg >= 360 {
		half = 180
	}
	start, end := s.AzimuthDeg-half, s.AzimuthDeg+half

	// Yayın iki ucu
	for _, deg := range []float64{start, end} {
		rad := deg * math.Pi / 180
		consider(s.RMaxM*math.Sin(rad), s.RMaxM*math.Cos(rad))
	}
	// Yay üzerindeki eksen yönleri (kuzey 0°, doğu 90°, güney 180°, batı 270°)
	for _, deg := range []float64{0, 90, 180, 270, -90, -180, -270} {
		if deg < start || deg > end {
			continue
		}
		rad := deg * math.Pi / 180
		consider(s.RMaxM*math.Sin(rad), s.RMaxM*math.Cos(rad))
	}

	return geo.Point{X: s.Site.X + minX, Y: s.Site.Y + minY},
		geo.Point{X: s.Site.X + maxX, Y: s.Site.Y + maxY}
}
