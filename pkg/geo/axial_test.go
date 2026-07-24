package geo

import (
	"math"
	"testing"
)

// gridResolutionM, analiz ızgarası çözünürlüğü (configs/*.yaml, ADR-07).
const gridResolutionM = 100.0

func mustGrid(t *testing.T, res float64) *HexGrid {
	t.Helper()
	g, err := NewHexGrid(res)
	if err != nil {
		t.Fatalf("NewHexGrid(%g): %v", res, err)
	}
	return g
}

// TestNewHexGrid_Invalid, geçersiz çözünürlüğün reddedildiğini sınar.
func TestNewHexGrid_Invalid(t *testing.T) {
	for _, res := range []float64{0, -100, math.NaN(), math.Inf(1)} {
		if _, err := NewHexGrid(res); err == nil {
			t.Errorf("NewHexGrid(%g) hata döndürmeliydi", res)
		}
	}
}

// TestHexGrid_ADR07Geometry, ADR-07'de yazılı geometrik büyüklükleri sınar:
// R = resolution/√3 ≈ 57.735 m ve hücre alanı ≈ 8.660 m².
func TestHexGrid_ADR07Geometry(t *testing.T) {
	g := mustGrid(t, gridResolutionM)

	if got, want := g.Size(), gridResolutionM/math.Sqrt(3); math.Abs(got-want) > 1e-9 {
		t.Errorf("Size() = %g, beklenen %g", got, want)
	}
	if got := g.Size(); math.Abs(got-57.735) > 0.001 {
		t.Errorf("Size() = %.3f m, ADR-07'de ≈57.735 m", got)
	}
	if got := g.CellAreaM2(); math.Abs(got-8660) > 1 {
		t.Errorf("CellAreaM2() = %.1f m², ADR-07'de ≈8.660 m²", got)
	}
	if got := g.Resolution(); got != gridResolutionM {
		t.Errorf("Resolution() = %g, beklenen %g", got, gridResolutionM)
	}
}

// TestHexGrid_OriginAtZero, (0,0) hücresinin ENU başlangıcında olduğunu sınar.
func TestHexGrid_OriginAtZero(t *testing.T) {
	g := mustGrid(t, gridResolutionM)
	if c := g.Center(Axial{}); c.X != 0 || c.Y != 0 {
		t.Errorf("Center({0,0}) = %+v, beklenen {0 0}", c)
	}
}

// TestHexGrid_NeighborSpacing, pointy-top ızgaranın tanımlayıcı özelliğini sınar:
// altı komşunun tamamına merkez-merkez mesafe çözünürlüğe eşittir.
func TestHexGrid_NeighborSpacing(t *testing.T) {
	g := mustGrid(t, gridResolutionM)

	for _, a := range []Axial{{}, {Q: 3, R: -2}, {Q: -7, R: 5}, {Q: 40, R: 40}} {
		center := g.Center(a)
		for i, n := range a.Neighbors() {
			d := Distance(center, g.Center(n))
			if math.Abs(d-gridResolutionM) > 1e-9 {
				t.Errorf("%+v komşu[%d]=%+v mesafesi %.9f, beklenen %g",
					a, i, n, d, gridResolutionM)
			}
		}
	}
}

// TestHexGrid_PointyTopOrientation, yönelimi sınar: pointy-top hexte sivri uç
// kuzeyi (+Y) gösterir, yani en yakın komşular doğu-batı ekseninde bulunur.
func TestHexGrid_PointyTopOrientation(t *testing.T) {
	g := mustGrid(t, gridResolutionM)

	// {1,0} komşusu saf doğu yönünde olmalı (y = 0)
	east := g.Center(Axial{Q: 1, R: 0})
	if math.Abs(east.Y) > 1e-9 {
		t.Errorf("{1,0} komşusu saf doğuda olmalı: %+v", east)
	}
	if east.X <= 0 {
		t.Errorf("{1,0} komşusu +X yönünde olmalı: %+v", east)
	}

	// Köşelerin en uzak Y'si merkez→köşe yarıçapına eşit olmalı (sivri uç kuzeyde)
	corners := g.Corners(Axial{})
	var maxY float64
	for _, c := range corners {
		maxY = math.Max(maxY, c.Y)
	}
	if math.Abs(maxY-g.Size()) > 1e-9 {
		t.Errorf("sivri uç kuzeyde olmalı: maxY = %g, beklenen %g", maxY, g.Size())
	}
}

// TestHexGrid_Corners, köşe geometrisini sınar: 6 köşe, hepsi merkeze R uzaklıkta.
func TestHexGrid_Corners(t *testing.T) {
	g := mustGrid(t, gridResolutionM)
	a := Axial{Q: 2, R: -3}
	center := g.Center(a)
	corners := g.Corners(a)

	if len(corners) != 6 {
		t.Fatalf("köşe sayısı = %d, beklenen 6", len(corners))
	}
	for i, c := range corners {
		if d := Distance(center, c); math.Abs(d-g.Size()) > 1e-9 {
			t.Errorf("köşe[%d] merkeze uzaklık %g, beklenen %g", i, d, g.Size())
		}
	}
	// Ardışık köşeler arası kenar uzunluğu da R'ye eşittir (düzgün altıgen)
	for i := range corners {
		next := corners[(i+1)%6]
		if d := Distance(corners[i], next); math.Abs(d-g.Size()) > 1e-9 {
			t.Errorf("kenar[%d] uzunluğu %g, beklenen %g", i, d, g.Size())
		}
	}
}

// TestHexGrid_CenterAtRoundTrip, ADR-07 doğrulama gereksinimidir (Bölüm K):
// axial → ENU → axial çevrimi kayıpsız olmalı.
func TestHexGrid_CenterAtRoundTrip(t *testing.T) {
	for _, res := range []float64{100, 500, 851.3, 2000} {
		g := mustGrid(t, res)
		for q := -50; q <= 50; q += 7 {
			for r := -50; r <= 50; r += 7 {
				a := Axial{Q: q, R: r}
				if got := g.At(g.Center(a)); got != a {
					t.Errorf("res=%g: round-trip %+v → %+v", res, a, got)
				}
			}
		}
	}
}

// TestHexGrid_AtNearestCenter, At()'in gerçekten en yakın merkezi seçtiğini
// bağımsız kaba kuvvet aramasıyla doğrular (yuvarlama kenarları dâhil).
func TestHexGrid_AtNearestCenter(t *testing.T) {
	g := mustGrid(t, gridResolutionM)

	// Izgaraya hizalı olmayan, kenar/köşe bölgelerine denk gelen noktalar
	probes := []Point{
		{X: 0, Y: 0}, {X: 49.9, Y: 0}, {X: 50.1, Y: 0},
		{X: 0, Y: 57.7}, {X: 0, Y: 57.8}, {X: -123.4, Y: 456.7},
		{X: 1234.5, Y: -987.6}, {X: 33.3, Y: 33.3}, {X: -0.001, Y: -0.001},
	}

	for _, p := range probes {
		got := g.At(p)

		// Kaba kuvvet: aday hücrenin komşuluğunda daha yakın merkez olmamalı
		best, bestD := got, Distance(p, g.Center(got))
		for dq := -2; dq <= 2; dq++ {
			for dr := -2; dr <= 2; dr++ {
				cand := Axial{Q: got.Q + dq, R: got.R + dr}
				if d := Distance(p, g.Center(cand)); d < bestD-1e-9 {
					best, bestD = cand, d
				}
			}
		}
		if best != got {
			t.Errorf("At(%+v) = %+v, daha yakın hücre var: %+v", p, got, best)
		}
		// En yakın merkez, hücrenin çevrel yarıçapından uzak olamaz
		if bestD > g.Size()+1e-9 {
			t.Errorf("At(%+v): merkez uzaklığı %g > R=%g", p, bestD, g.Size())
		}
	}
}

// TestHexDistance, hücre cinsinden uzaklık metriğini sınar.
func TestHexDistance(t *testing.T) {
	tests := []struct {
		a, b Axial
		want int
	}{
		{Axial{}, Axial{}, 0},
		{Axial{}, Axial{Q: 1, R: 0}, 1},
		{Axial{}, Axial{Q: 0, R: 1}, 1},
		{Axial{}, Axial{Q: 1, R: -1}, 1},
		{Axial{}, Axial{Q: 2, R: 0}, 2},
		{Axial{}, Axial{Q: -3, R: 1}, 3},
		{Axial{Q: 5, R: -2}, Axial{Q: 5, R: -2}, 0},
	}
	for _, tc := range tests {
		if got := HexDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("HexDistance(%+v, %+v) = %d, beklenen %d", tc.a, tc.b, got, tc.want)
		}
		// simetrik olmalı
		if got := HexDistance(tc.b, tc.a); got != tc.want {
			t.Errorf("HexDistance simetrik değil: %+v ↔ %+v", tc.a, tc.b)
		}
	}
}

// TestAxial_S, küp koordinat kısıtını sınar: q + r + s = 0.
func TestAxial_S(t *testing.T) {
	for _, a := range []Axial{{}, {Q: 3, R: -5}, {Q: -2, R: 7}} {
		if a.Q+a.R+a.S() != 0 {
			t.Errorf("%+v: q+r+s = %d, beklenen 0", a, a.Q+a.R+a.S())
		}
	}
}

// TestHexGrid_Cover, disk kaplamasını sınar: dâhil edilen her hücrenin merkezi
// disk içinde, dışarıda bırakılan hiçbir hücrenin merkezi disk içinde değil.
func TestHexGrid_Cover(t *testing.T) {
	g := mustGrid(t, gridResolutionM)
	center := Point{X: 250, Y: -400}
	const radius float64 = 500

	cells := g.Cover(center, radius)
	if len(cells) == 0 {
		t.Fatal("Cover boş döndü")
	}

	in := make(map[Axial]bool, len(cells))
	for _, a := range cells {
		if d := Distance(center, g.Center(a)); d > radius {
			t.Errorf("%+v merkezi disk dışında (d=%.2f > %g)", a, d, radius)
		}
		if in[a] {
			t.Errorf("%+v tekrarlanmış", a)
		}
		in[a] = true
	}

	// Eksiksizlik: kaplama kutusundaki hiçbir disk-içi hücre atlanmamalı
	span := int(radius/g.Resolution()) + 3
	origin := g.At(center)
	for dq := -span; dq <= span; dq++ {
		for dr := -span; dr <= span; dr++ {
			a := Axial{Q: origin.Q + dq, R: origin.R + dr}
			if Distance(center, g.Center(a)) <= radius && !in[a] {
				t.Errorf("%+v disk içinde ama kaplamada yok", a)
			}
		}
	}
}

// TestHexGrid_CoverAreaConsistency, kaplanan hücre sayısının disk alanıyla
// tutarlı olduğunu sınar (kenar etkisi dışında).
func TestHexGrid_CoverAreaConsistency(t *testing.T) {
	g := mustGrid(t, gridResolutionM)

	// Yarıçap büyüdükçe kenar etkisi azalır, oran 1'e yaklaşmalı
	for _, radius := range []float64{1000, 5000} {
		cells := g.Cover(Point{}, radius)
		gotArea := float64(len(cells)) * g.CellAreaM2()
		wantArea := math.Pi * radius * radius
		ratio := gotArea / wantArea

		if math.Abs(ratio-1) > 0.02 {
			t.Errorf("radius=%g: hücre alanı toplamı / disk alanı = %.4f, beklenen ≈1",
				radius, ratio)
		}
	}
}

// TestHexGrid_CoverDeterministic, aynı girdinin aynı sırayı ürettiğini sınar (K10).
func TestHexGrid_CoverDeterministic(t *testing.T) {
	g := mustGrid(t, gridResolutionM)
	a := g.Cover(Point{X: 10, Y: 20}, 800)
	b := g.Cover(Point{X: 10, Y: 20}, 800)

	if len(a) != len(b) {
		t.Fatalf("uzunluklar farklı: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sıra deterministik değil: [%d] %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestHexGrid_CoverInvalidRadius, geçersiz yarıçapta boş sonuç döndüğünü sınar.
func TestHexGrid_CoverInvalidRadius(t *testing.T) {
	g := mustGrid(t, gridResolutionM)
	for _, r := range []float64{0, -1, math.NaN()} {
		if got := g.Cover(Point{}, r); len(got) != 0 {
			t.Errorf("Cover(radius=%g) boş dönmeli, %d hücre döndü", r, len(got))
		}
	}
}
