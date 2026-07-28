package geometry

import (
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// square, verilen köşelerden saat yönünün tersine bir dikdörtgen üretir.
func square(minX, minY, maxX, maxY float64) []geo.Point {
	return []geo.Point{
		{X: minX, Y: minY}, {X: maxX, Y: minY},
		{X: maxX, Y: maxY}, {X: minX, Y: maxY},
	}
}

// TestDiskPolygonArea_AnalyticReferences, kesişim alanını **kapalı formülle
// bilinen** durumlara karşı sınar.
//
// Bu değerler uygulamadan bağımsızdır: πr², πr²/4 ve πr²/2 doğrudan dairenin
// alanından gelir. Örtüşme oranının tamamı bu fonksiyona dayandığı için burada
// bir hata, ADR-18'in TA penceresini doğrudan bozardı.
func TestDiskPolygonArea_AnalyticReferences(t *testing.T) {
	const r = 300.0
	full := math.Pi * r * r

	cases := []struct {
		name string
		poly []geo.Point
		want float64
	}{
		{"disk kareye tamamen giriyor", square(-1000, -1000, 1000, 1000), full},
		{"çeyrek disk", square(0, 0, 1000, 1000), full / 4},
		{"yarım disk", square(0, -1000, 1000, 1000), full / 2},
		{"disk kareyi tamamen kaplıyor", square(-10, -10, 10, 10), 400},
		{"disk çokgenden uzakta", square(5000, 5000, 6000, 6000), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiskPolygonArea(tc.poly, geo.Point{}, r)
			if math.Abs(got-tc.want) > 1e-6*math.Max(1, tc.want) {
				t.Errorf("alan = %.6f, beklenen %.6f", got, tc.want)
			}
		})
	}
}

// TestDiskPolygonArea_WindingIndependent, çokgenin dolanım yönünün sonucu
// değiştirmediğini sınar.
func TestDiskPolygonArea_WindingIndependent(t *testing.T) {
	poly := square(-100, -100, 400, 400)
	rev := make([]geo.Point, len(poly))
	for i, p := range poly {
		rev[len(poly)-1-i] = p
	}
	ccw := DiskPolygonArea(poly, geo.Point{}, 300)
	cw := DiskPolygonArea(rev, geo.Point{}, 300)
	if math.Abs(ccw-cw) > 1e-9 {
		t.Errorf("dolanım yönü sonucu değiştirdi: %.9f vs %.9f", ccw, cw)
	}
}

// TestDiskPolygonArea_OffsetCenter, merkezin öteleneceğini sınar: kesişim
// alanı yalnızca göreli konuma bağlıdır.
func TestDiskPolygonArea_OffsetCenter(t *testing.T) {
	const r = 300.0
	at0 := DiskPolygonArea(square(0, 0, 1000, 1000), geo.Point{}, r)
	off := DiskPolygonArea(square(5000, -2000, 6000, -1000), geo.Point{X: 5000, Y: -2000}, r)
	if math.Abs(at0-off) > 1e-9 {
		t.Errorf("öteleme sonucu değiştirdi: %.9f vs %.9f", at0, off)
	}
}

// quadratureOverlap, örtüşme oranını **bağımsız yöntemle** hesaplar: hücreyi
// ince bir ızgarayla tarar ve halkaya düşen noktaların oranını alır.
//
// Analitik formülle aynı sonucu vermesi, iki ayrı yöntemin birbirini
// doğrulaması demektir; kendi kodunu kendi koduyla sınama tuzağına düşülmez.
func quadratureOverlap(poly []geo.Point, center geo.Point, inner, outer float64, n int) float64 {
	minX, minY := poly[0].X, poly[0].Y
	maxX, maxY := minX, minY
	for _, p := range poly[1:] {
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
		minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
	}
	dx, dy := (maxX-minX)/float64(n), (maxY-minY)/float64(n)

	inPoly, inRing := 0, 0
	for i := 0; i < n; i++ {
		px := minX + (float64(i)+0.5)*dx
		for j := 0; j < n; j++ {
			py := minY + (float64(j)+0.5)*dy
			if !pointInPolygon(px, py, poly) {
				continue
			}
			inPoly++
			d := math.Hypot(px-center.X, py-center.Y)
			if d >= inner && d < outer {
				inRing++
			}
		}
	}
	if inPoly == 0 {
		return 0
	}
	return float64(inRing) / float64(inPoly)
}

func pointInPolygon(px, py float64, poly []geo.Point) bool {
	in := false
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		if (a.Y > py) != (b.Y > py) && px < (b.X-a.X)*(py-a.Y)/(b.Y-a.Y)+a.X {
			in = !in
		}
	}
	return in
}

// TestOverlapRatio_HexAgainstQuadrature, gerçek ızgara hücreleri üzerinde
// analitik örtüşme oranını bağımsız kareleme ile karşılaştırır.
//
// Halka LTE'nin ta = 19 değerine karşılık gelir: [1484,28 , 1562,40) —
// kalınlığı 78,12 m, yani 100 m'lik ızgara adımından **ince**. İkili
// merkez-içinde-mi testinin neden yetmediği tam olarak burada görülür.
func TestOverlapRatio_HexAgainstQuadrature(t *testing.T) {
	grid, err := geo.NewHexGrid(100)
	if err != nil {
		t.Fatalf("NewHexGrid: %v", err)
	}
	inner, outer := 19*78.12, 20*78.12
	site := geo.Point{}

	for _, center := range []geo.Point{
		{X: 1500, Y: 0}, {X: 1450, Y: 0}, {X: 1560, Y: 0},
		{X: 1520, Y: 300}, {X: 1200, Y: 0}, {X: 0, Y: 1500},
	} {
		cell := grid.Corners(grid.At(center))
		got := OverlapRatio(cell, site, inner, outer)
		want := quadratureOverlap(cell, site, inner, outer, 900)

		if math.Abs(got-want) > 5e-3 {
			t.Errorf("hücre %v: analitik %.6f, kareleme %.6f (fark %.6f)",
				center, got, want, math.Abs(got-want))
		}
		t.Logf("hücre %-14v analitik %.6f · kareleme %.6f", center, got, want)
	}
}

// TestOverlapRatio_Bounds, sınır durumlarını sınar.
func TestOverlapRatio_Bounds(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)
	cell := grid.Corners(geo.Axial{Q: 15, R: 0})
	site := geo.Point{}
	d := geo.Distance(site, grid.Center(geo.Axial{Q: 15, R: 0}))

	// Hücreyi tamamen içine alan halka → 1
	if got := OverlapRatio(cell, site, d-500, d+500); math.Abs(got-1) > 1e-9 {
		t.Errorf("kapsayan halka için oran %.9f, 1 beklenir", got)
	}
	// Hücreden tamamen uzak halka → 0
	if got := OverlapRatio(cell, site, d+500, d+600); got != 0 {
		t.Errorf("uzak halka için oran %.9f, 0 beklenir", got)
	}
	// Dejenere halka → 0
	if got := OverlapRatio(cell, site, 100, 100); got != 0 {
		t.Errorf("dejenere halka için oran %.9f, 0 beklenir", got)
	}
}

// TestPolygonArea, ayakkabı bağı formülünü hex hücresinin bilinen alanıyla
// karşılaştırır: A = 1,5·√3·R².
func TestPolygonArea(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)
	got := PolygonArea(grid.Corners(geo.Axial{}))
	want := grid.CellAreaM2()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("hex alanı %.6f, beklenen %.6f", got, want)
	}
	// 100 m merkez-merkez için hücre alanı ≈ 8.660 m² (plan BÖLÜM C.1)
	if math.Abs(got-8660.254) > 0.01 {
		t.Errorf("hex alanı %.3f m², plandaki ≈8660 m² ile uyuşmuyor", got)
	}
}

// TestPBT_OverlapRatioInUnitInterval, oranın her zaman [0,1] aralığında
// kaldığını sınar.
func TestPBT_OverlapRatioInUnitInterval(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)

	rapid.Check(t, func(rt *rapid.T) {
		q := rapid.IntRange(-200, 200).Draw(rt, "q")
		r := rapid.IntRange(-200, 200).Draw(rt, "r")
		inner := rapid.Float64Range(0, 20000).Draw(rt, "inner")
		width := rapid.Float64Range(1, 1000).Draw(rt, "width")

		cell := grid.Corners(geo.Axial{Q: q, R: r})
		got := OverlapRatio(cell, geo.Point{}, inner, inner+width)

		if math.IsNaN(got) || got < 0 || got > 1 {
			rt.Fatalf("oran = %v — [0,1] dışında (hücre %d,%d halka [%g,%g))",
				got, q, r, inner, inner+width)
		}
	})
}

// TestPBT_AnnulusAdditivity, ardışık halkaların toplamının birleşik halkaya
// eşit olduğunu sınar: kesişim hesabı toplanabilir olmalıdır.
func TestPBT_AnnulusAdditivity(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)

	rapid.Check(t, func(rt *rapid.T) {
		q := rapid.IntRange(-100, 100).Draw(rt, "q")
		r := rapid.IntRange(-100, 100).Draw(rt, "r")
		a := rapid.Float64Range(0, 10000).Draw(rt, "a")
		b := a + rapid.Float64Range(1, 500).Draw(rt, "ab")
		c := b + rapid.Float64Range(1, 500).Draw(rt, "bc")

		cell := grid.Corners(geo.Axial{Q: q, R: r})
		site := geo.Point{}

		parts := AnnulusPolygonArea(cell, site, a, b) + AnnulusPolygonArea(cell, site, b, c)
		whole := AnnulusPolygonArea(cell, site, a, c)

		if math.Abs(parts-whole) > 1e-6*math.Max(1, whole) {
			rt.Fatalf("toplanabilirlik bozuldu: %.9f + parça ≠ %.9f", parts, whole)
		}
	})
}
