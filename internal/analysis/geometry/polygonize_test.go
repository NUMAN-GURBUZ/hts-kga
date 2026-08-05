package geometry

import (
	"math"
	"math/rand"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// testHex, 100 m çözünürlüklü ızgaradır (kentsel senaryo — ADR-07).
func testHex(t testing.TB) *geo.HexGrid {
	t.Helper()
	hex, err := geo.NewHexGrid(100)
	if err != nil {
		t.Fatalf("NewHexGrid: %v", err)
	}
	return hex
}

// neighbours, hücrenin altı komşusunu dilim olarak döndürür.
func neighbours(a geo.Axial) []geo.Axial {
	n := a.Neighbors()
	return n[:]
}

// flower, merkez hücre ve altı komşusudur (7 hücre).
func flower(center geo.Axial) []geo.Axial {
	return append([]geo.Axial{center}, neighbours(center)...)
}

// TestPolygonize_SingleCell, tek hücrenin sınırı altıgenin kendisidir.
func TestPolygonize_SingleCell(t *testing.T) {
	hex := testHex(t)

	mp, err := Polygonize([]geo.Axial{{Q: 0, R: 0}}, hex)
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}

	if mp.PartCount() != 1 {
		t.Fatalf("part_count %d, 1 beklenir", mp.PartCount())
	}
	if len(mp[0].Holes) != 0 {
		t.Errorf("delik sayısı %d, 0 beklenir", len(mp[0].Holes))
	}
	// Altı köşe + kapanış köşesi
	if got := len(mp[0].Exterior); got != 7 {
		t.Errorf("köşe sayısı %d, 7 beklenir (6 + kapanış)", got)
	}
	// Dış halka saat yönünün tersi → pozitif işaretli alan (OGC yönelimi)
	if a := mp[0].Exterior.SignedAreaM2(); a <= 0 {
		t.Errorf("dış halka işaretli alanı %.3f, pozitif (CCW) beklenir", a)
	}
	if got, want := mp.AreaM2(), hex.CellAreaM2(); math.Abs(got-want) > 1e-6 {
		t.Errorf("alan %.6f m², hücre alanı %.6f m² beklenir", got, want)
	}

	// Köşeler, ızgaranın kendi köşe tanımıyla aynı olmalıdır.
	corners := hex.Corners(geo.Axial{Q: 0, R: 0})
	for _, c := range corners {
		found := false
		for _, v := range mp[0].Exterior {
			if math.Abs(v.X-c.X) < 1e-9 && math.Abs(v.Y-c.Y) < 1e-9 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ızgara köşesi (%.3f, %.3f) halkada yok", c.X, c.Y)
		}
	}
}

// TestPolygonize_FlowerIsSinglePart, bitişik hücrelerin tek parça olduğunu ve
// iç kenarların sınıra karışmadığını doğrular.
func TestPolygonize_FlowerIsSinglePart(t *testing.T) {
	hex := testHex(t)

	mp, err := Polygonize(flower(geo.Axial{Q: 0, R: 0}), hex)
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}

	if mp.PartCount() != 1 {
		t.Fatalf("part_count %d, 1 beklenir", mp.PartCount())
	}
	// 7 hücre · 6 kenar = 42; 12 iç kenar çifti olarak sayılır → 42 − 24 = 18
	if got := len(mp[0].Exterior) - 1; got != 18 {
		t.Errorf("sınır kenarı %d, 18 beklenir", got)
	}
	if got, want := mp.AreaM2(), 7*hex.CellAreaM2(); math.Abs(got-want)/want > 1e-12 {
		t.Errorf("alan %.3f m², %.3f m² beklenir", got, want)
	}
}

// TestPolygonize_Hole, ortası boş halkanın delik ürettiğini doğrular.
//
// Delik, dış halkadan **çıkarılır**: alan 6 hücredir, 7 değil.
func TestPolygonize_Hole(t *testing.T) {
	hex := testHex(t)
	center := geo.Axial{Q: 0, R: 0}

	mp, err := Polygonize(neighbours(center), hex) // merkez yok
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}

	if mp.PartCount() != 1 {
		t.Fatalf("part_count %d, 1 beklenir", mp.PartCount())
	}
	if len(mp[0].Holes) != 1 {
		t.Fatalf("delik sayısı %d, 1 beklenir", len(mp[0].Holes))
	}
	// Delik saat yönünde → negatif işaretli alan
	if a := mp[0].Holes[0].SignedAreaM2(); a >= 0 {
		t.Errorf("delik işaretli alanı %.3f, negatif (CW) beklenir", a)
	}
	if got := len(mp[0].Holes[0]) - 1; got != 6 {
		t.Errorf("delik kenarı %d, 6 beklenir", got)
	}
	if got, want := mp.AreaM2(), 6*hex.CellAreaM2(); math.Abs(got-want)/want > 1e-12 {
		t.Errorf("alan %.3f m², %.3f m² beklenir (delik düşülmüş)", got, want)
	}
}

// TestPolygonize_DisjointParts, ayrık kümelerin ayrı bileşen olduğunu doğrular.
func TestPolygonize_DisjointParts(t *testing.T) {
	hex := testHex(t)

	cells := append(flower(geo.Axial{Q: 0, R: 0}), geo.Axial{Q: 20, R: 0}, geo.Axial{Q: 21, R: 0})
	mp, err := Polygonize(cells, hex)
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}

	if mp.PartCount() != 2 {
		t.Fatalf("part_count %d, 2 beklenir", mp.PartCount())
	}
	// Kararlı sıra: büyük bileşen önce
	if mp[0].AreaM2() < mp[1].AreaM2() {
		t.Errorf("bileşenler alana göre azalan sırada değil")
	}
	if got, want := mp.AreaM2(), 9*hex.CellAreaM2(); math.Abs(got-want)/want > 1e-12 {
		t.Errorf("alan %.3f m², %.3f m² beklenir", got, want)
	}
}

// TestPolygonize_IslandInHole, delik içindeki adanın ayrı bileşen olduğunu ve
// deliğin **en küçük** kapsayan halkaya atandığını doğrular.
func TestPolygonize_IslandInHole(t *testing.T) {
	hex := testHex(t)

	// 2 hücre yarıçaplı dolu disk, sonra birinci halka boşaltılır:
	// dış halka (yarıçap 2) + delik (yarıçap 1 boşluğu) + merkez ada.
	var cells []geo.Axial
	center := geo.Axial{Q: 0, R: 0}
	for q := -3; q <= 3; q++ {
		for r := -3; r <= 3; r++ {
			a := geo.Axial{Q: q, R: r}
			switch geo.HexDistance(a, center) {
			case 0, 2, 3:
				cells = append(cells, a)
			}
		}
	}

	mp, err := Polygonize(cells, hex)
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}

	if mp.PartCount() != 2 {
		t.Fatalf("part_count %d, 2 beklenir (halka + ada)", mp.PartCount())
	}
	if len(mp[0].Holes) != 1 {
		t.Errorf("dış bileşende delik sayısı %d, 1 beklenir", len(mp[0].Holes))
	}
	if len(mp[1].Holes) != 0 {
		t.Errorf("ada deliksiz olmalı, %d delik var", len(mp[1].Holes))
	}
	if got, want := mp.AreaM2(), float64(len(cells))*hex.CellAreaM2(); math.Abs(got-want)/want > 1e-12 {
		t.Errorf("alan %.3f m², %.3f m² beklenir", got, want)
	}
}

// TestPolygonize_AreaEqualsCellCount, rastgele bitişik kümelerde alanın
// hücre sayısıyla tam orantılı olduğunu doğrular.
//
// Bu, kenar izlemesinin en güçlü tek testidir: fazladan ya da eksik tek bir
// sınır kenarı alanı bozar.
func TestPolygonize_AreaEqualsCellCount(t *testing.T) {
	hex := testHex(t)
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 50; iter++ {
		set := map[geo.Axial]struct{}{{Q: 0, R: 0}: {}}
		frontier := []geo.Axial{{Q: 0, R: 0}}
		for len(set) < 60 {
			base := frontier[rng.Intn(len(frontier))]
			nb := base.Neighbors()[rng.Intn(6)]
			if _, ok := set[nb]; !ok {
				set[nb] = struct{}{}
				frontier = append(frontier, nb)
			}
		}

		cells := make([]geo.Axial, 0, len(set))
		for a := range set {
			cells = append(cells, a)
		}

		mp, err := Polygonize(cells, hex)
		if err != nil {
			t.Fatalf("iter %d: Polygonize: %v", iter, err)
		}
		want := float64(len(cells)) * hex.CellAreaM2()
		if got := mp.AreaM2(); math.Abs(got-want)/want > 1e-12 {
			t.Fatalf("iter %d: alan %.6f m², %.6f m² beklenir (%d hücre)",
				iter, got, want, len(cells))
		}
	}
}

// TestPolygonize_OrderIndependent, girdi sırasının çıktıyı etkilemediğini
// doğrular — K10'un poligonlaştırma karşılığı.
func TestPolygonize_OrderIndependent(t *testing.T) {
	hex := testHex(t)
	rng := rand.New(rand.NewSource(7))

	cells := flower(geo.Axial{Q: 0, R: 0})
	cells = append(cells, geo.Axial{Q: 2, R: 0}, geo.Axial{Q: 3, R: 0}, geo.Axial{Q: 3, R: 1})

	want, err := Polygonize(cells, hex)
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}

	for iter := 0; iter < 20; iter++ {
		shuffled := append([]geo.Axial(nil), cells...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		got, err := Polygonize(shuffled, hex)
		if err != nil {
			t.Fatalf("iter %d: Polygonize: %v", iter, err)
		}
		if got.PartCount() != want.PartCount() {
			t.Fatalf("iter %d: part_count %d, %d beklenir", iter, got.PartCount(), want.PartCount())
		}
		for p := range got {
			if len(got[p].Exterior) != len(want[p].Exterior) {
				t.Fatalf("iter %d: bileşen %d köşe sayısı %d, %d beklenir",
					iter, p, len(got[p].Exterior), len(want[p].Exterior))
			}
			for i := range got[p].Exterior {
				// Bit düzeyinde aynı olmalı — kafes kimliğinden üretiliyorlar.
				if got[p].Exterior[i] != want[p].Exterior[i] {
					t.Fatalf("iter %d: bileşen %d köşe %d ayrıştı: %v ≠ %v",
						iter, p, i, got[p].Exterior[i], want[p].Exterior[i])
				}
			}
		}
	}
}

// TestPolygonize_SharedVerticesAreIdentical, komşu hücrelerin paylaştığı
// köşenin bit düzeyinde tek bir sayı olduğunu doğrular.
//
// Köşeler kayan noktada iki farklı merkezden hesaplansaydı halkalar
// birleşmez, izleme kopardı.
func TestPolygonize_SharedVerticesAreIdentical(t *testing.T) {
	hex := testHex(t)

	mp, err := Polygonize([]geo.Axial{{Q: 0, R: 0}, {Q: 1, R: 0}}, hex)
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}
	if mp.PartCount() != 1 {
		t.Fatalf("part_count %d, 1 beklenir — halkalar birleşmemiş", mp.PartCount())
	}
	// İki hücre · 6 kenar − 2 (paylaşılan kenar) = 10
	if got := len(mp[0].Exterior) - 1; got != 10 {
		t.Errorf("sınır kenarı %d, 10 beklenir", got)
	}
}

// TestPolygonize_Rejects, geçersiz girdileri reddeder.
func TestPolygonize_Rejects(t *testing.T) {
	hex := testHex(t)

	if _, err := Polygonize(nil, hex); err == nil {
		t.Error("boş küme kabul edildi")
	}
	if _, err := Polygonize([]geo.Axial{{Q: 0, R: 0}}, nil); err == nil {
		t.Error("nil ızgara kabul edildi")
	}
	// Yinelenen hücre tekilleştirilmelidir, hata değil.
	mp, err := Polygonize([]geo.Axial{{Q: 0, R: 0}, {Q: 0, R: 0}}, hex)
	if err != nil {
		t.Fatalf("yinelenen hücre: %v", err)
	}
	if got, want := mp.AreaM2(), hex.CellAreaM2(); math.Abs(got-want) > 1e-6 {
		t.Errorf("yinelenen hücre alanı %.3f, %.3f beklenir", got, want)
	}
}
