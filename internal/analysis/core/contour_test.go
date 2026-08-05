package core

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// planLevels, senaryo config'indeki güven seviyeleridir (analysis.contour_levels).
var planLevels = []float64{0.50, 0.90, 0.95}

// massFor, verilen profilde tek bir kayıttan kütle üretir.
func massFor(t testing.TB, p profileSpec, resolutionM float64, taValue *int) (map[geo.Axial]float64, *density.Grid) {
	t.Helper()

	inv := buildNetwork(t, p)
	grid := mustGrid(t, resolutionM)
	res, err := Estimate(record(inv, 0, taValue), inv, grid, DefaultOptions(testConfig(p), 8))
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	return res.Mass, grid
}

// TestContours_PrefixNesting, üç seviyenin aynı sıralı listenin ön eki
// olduğunu doğrular: C(%50) ⊆ C(%90) ⊆ C(%95).
func TestContours_PrefixNesting(t *testing.T) {
	mass, grid := massFor(t, urbanProfile(), 100, nil)

	contours, err := Contours(mass, grid, planLevels)
	if err != nil {
		t.Fatalf("Contours: %v", err)
	}
	if len(contours) != 3 {
		t.Fatalf("kontur sayısı %d, 3 beklenir", len(contours))
	}

	for i := 1; i < len(contours); i++ {
		inner, outer := contours[i-1], contours[i]
		if len(inner.Cells) > len(outer.Cells) {
			t.Fatalf("%.2f konturu (%d hücre) %.2f konturundan (%d hücre) büyük",
				inner.Level, len(inner.Cells), outer.Level, len(outer.Cells))
		}
		for j, cell := range inner.Cells {
			if outer.Cells[j] != cell {
				t.Fatalf("%.2f konturu %.2f konturunun ön eki değil (%d. hücre ayrıştı)",
					inner.Level, outer.Level, j)
			}
		}
		if inner.Mass > outer.Mass {
			t.Errorf("kütle azalmış: %.2f → %g, %.2f → %g",
				inner.Level, inner.Mass, outer.Level, outer.Mass)
		}
	}
}

// TestContours_MassReachesLevel, seçilen kütlenin hedefi karşıladığını ve
// gereksiz yere aşmadığını doğrular.
//
// "Gereksiz yere aşmamak" HPD tanımının kendisidir: son hücre çıkarıldığında
// hedefin altına düşülmelidir, aksi hâlde daha küçük bir bölge aynı güveni
// verirdi.
func TestContours_MassReachesLevel(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    profileSpec
		res  float64
	}{
		{"kentsel", urbanProfile(), 100},
		{"kırsal", ruralProfile(), 250},
	} {
		mass, grid := massFor(t, tc.p, tc.res, nil)
		contours, err := Contours(mass, grid, planLevels)
		if err != nil {
			t.Fatalf("%s: Contours: %v", tc.name, err)
		}

		for _, c := range contours {
			if c.Mass < c.Level-density.MassTolerance {
				t.Errorf("%s: %.2f konturu yalnızca %g kütle topluyor", tc.name, c.Level, c.Mass)
			}
			last := mass[c.Cells[len(c.Cells)-1]]
			if c.Mass-last >= c.Level {
				t.Errorf("%s: %.2f konturu gereksiz hücre içeriyor (son hücresiz kütle %g ≥ %g)",
					tc.name, c.Level, c.Mass-last, c.Level)
			}
		}
	}
}

// TestContours_PolygonNesting, iç içeliği **poligon düzeyinde** doğrular.
//
// Küme kapsaması (ön ek) tek başına yetmez: poligonlaştırma hatalı olsaydı
// alt küme daha büyük ya da kayık bir bölge üretebilirdi. Burada iç konturun
// her hücre merkezi, dış konturun geometrisinin içinde aranır — ayrıca alan
// sıralaması sınanır (PBT #2).
func TestContours_PolygonNesting(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    profileSpec
		res  float64
		ta   *int
	}{
		{"kentsel TA'sız", urbanProfile(), 100, nil},
		{"kentsel TA'lı", urbanProfile(), 100, intPtr(12)},
		{"kırsal TA'sız", ruralProfile(), 250, nil},
		{"kırsal TA'lı", ruralProfile(), 250, intPtr(30)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mass, grid := massFor(t, tc.p, tc.res, tc.ta)
			contours, err := Contours(mass, grid, planLevels)
			if err != nil {
				t.Fatalf("Contours: %v", err)
			}

			polys := make([]geometry.MultiPolygon, len(contours))
			areas := make([]float64, len(contours))
			for i, c := range contours {
				mp, err := grid.Polygonize(c.Cells)
				if err != nil {
					t.Fatalf("%.2f: Polygonize: %v", c.Level, err)
				}
				polys[i], areas[i] = mp, mp.AreaM2()

				// PBT #5: her geometri en az bir parçadır.
				if mp.PartCount() < 1 {
					t.Fatalf("%.2f: part_count %d", c.Level, mp.PartCount())
				}
				// Alan, hücre sayısıyla tam orantılı olmalı (kenar izleme denetimi).
				want := float64(len(c.Cells)) * grid.CellAreaM2()
				if math.Abs(areas[i]-want)/want > 1e-12 {
					t.Errorf("%.2f: alan %.3f m², %.3f m² beklenir", c.Level, areas[i], want)
				}
			}

			// PBT #2: area(M@95) ≥ area(M@90) ≥ area(M@50)
			for i := 1; i < len(areas); i++ {
				if areas[i] < areas[i-1] {
					t.Errorf("alan azalmış: %.2f → %.1f m², %.2f → %.1f m²",
						contours[i-1].Level, areas[i-1], contours[i].Level, areas[i])
				}
			}

			// Geometrik kapsama: iç konturun her hücresi dış geometrinin içinde.
			for i := 1; i < len(contours); i++ {
				for _, cell := range contours[i-1].Cells {
					if !polys[i].Contains(grid.Center(cell)) {
						t.Fatalf("%.2f konturunun hücresi %v, %.2f geometrisinin dışında",
							contours[i-1].Level, cell, contours[i].Level)
					}
				}
			}
		})
	}
}

// TestContours_Deterministic, eşit kütleli hücrelerde seçimin koşudan koşuya
// aynı kaldığını doğrular (K10).
//
// Harita yinelemesi Go'da rastgeledir; eşitlik (r, q) ile kırılmasaydı aynı
// kütle haritası farklı hücre kümeleri üretirdi.
func TestContours_Deterministic(t *testing.T) {
	grid := mustGrid(t, 100)

	// 200 hücrenin tamamı bit düzeyinde aynı kütlede — en zor durum.
	mass := make(map[geo.Axial]float64, 200)
	for i := 0; i < 200; i++ {
		mass[geo.Axial{Q: i % 20, R: i / 20}] = 1.0 / 200
	}

	want, err := Contours(mass, grid, planLevels)
	if err != nil {
		t.Fatalf("Contours: %v", err)
	}

	for iter := 0; iter < 20; iter++ {
		got, err := Contours(mass, grid, planLevels)
		if err != nil {
			t.Fatalf("iter %d: Contours: %v", iter, err)
		}
		for i := range got {
			if len(got[i].Cells) != len(want[i].Cells) {
				t.Fatalf("iter %d: %.2f hücre sayısı %d, %d beklenir",
					iter, got[i].Level, len(got[i].Cells), len(want[i].Cells))
			}
			for j := range got[i].Cells {
				if got[i].Cells[j] != want[i].Cells[j] {
					t.Fatalf("iter %d: %.2f konturu %d. hücrede ayrıştı: %v ≠ %v",
						iter, got[i].Level, j, got[i].Cells[j], want[i].Cells[j])
				}
			}
			if got[i].Centroid != want[i].Centroid {
				t.Fatalf("iter %d: %.2f merkezi ayrıştı: %v ≠ %v",
					iter, got[i].Level, got[i].Centroid, want[i].Centroid)
			}
		}
	}
}

// TestContours_LevelOrderIndependent, seviye listesinin sırasının sonucu
// etkilemediğini doğrular: çıktı daima artan sıradadır.
func TestContours_LevelOrderIndependent(t *testing.T) {
	mass, grid := massFor(t, urbanProfile(), 100, nil)

	shuffled := []float64{0.95, 0.50, 0.90}
	got, err := Contours(mass, grid, shuffled)
	if err != nil {
		t.Fatalf("Contours: %v", err)
	}
	for i, c := range got {
		if c.Level != planLevels[i] {
			t.Errorf("%d. kontur seviyesi %.2f, %.2f beklenir (artan sıra)", i, c.Level, planLevels[i])
		}
	}
}

// TestContours_CentroidInsideGeometry, kütle ağırlıklı merkezin kendi
// geometrisinin içinde kaldığını doğrular (ADR-10).
//
// Merkez, tüm dağılımdan değil konturun kendi kütlesinden türetildiği için
// bu beklenir; tek parçalı bir bölgede dışarı düşmesi hesabın bozukluğuna
// işaret ederdi.
func TestContours_CentroidInsideGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    profileSpec
		res  float64
	}{
		{"kentsel", urbanProfile(), 100},
		{"kırsal", ruralProfile(), 250},
	} {
		mass, grid := massFor(t, tc.p, tc.res, nil)
		contours, err := Contours(mass, grid, planLevels)
		if err != nil {
			t.Fatalf("%s: Contours: %v", tc.name, err)
		}

		for _, c := range contours {
			mp, err := grid.Polygonize(c.Cells)
			if err != nil {
				t.Fatalf("%s: Polygonize: %v", tc.name, err)
			}
			if mp.PartCount() != 1 {
				continue // çok parçalı bölgede merkez parçalar arasına düşebilir
			}
			if !mp.Contains(c.Centroid) {
				t.Errorf("%s: %.2f merkezi (%.1f, %.1f) kendi geometrisinin dışında",
					tc.name, c.Level, c.Centroid.X, c.Centroid.Y)
			}
		}
	}
}

// TestContours_CentroidMatchesDefinition, merkezi bağımsız bir toplamla
// karşılaştırır: Σ(mass·center)/Σmass.
func TestContours_CentroidMatchesDefinition(t *testing.T) {
	mass, grid := massFor(t, urbanProfile(), 100, nil)

	contours, err := Contours(mass, grid, planLevels)
	if err != nil {
		t.Fatalf("Contours: %v", err)
	}

	for _, c := range contours {
		var sx, sy, total float64
		for _, cell := range c.Cells {
			p := grid.Center(cell)
			sx += mass[cell] * p.X
			sy += mass[cell] * p.Y
			total += mass[cell]
		}
		wantX, wantY := sx/total, sy/total
		if math.Abs(c.Centroid.X-wantX) > 1e-6 || math.Abs(c.Centroid.Y-wantY) > 1e-6 {
			t.Errorf("%.2f merkezi (%.6f, %.6f), (%.6f, %.6f) beklenir",
				c.Level, c.Centroid.X, c.Centroid.Y, wantX, wantY)
		}
	}
}

// TestContours_Rejects, geçersiz girdileri reddeder.
func TestContours_Rejects(t *testing.T) {
	grid := mustGrid(t, 100)
	mass := map[geo.Axial]float64{{Q: 0, R: 0}: 1}

	cases := []struct {
		name   string
		mass   map[geo.Axial]float64
		grid   *density.Grid
		levels []float64
	}{
		{"nil ızgara", mass, nil, planLevels},
		{"boş kütle", map[geo.Axial]float64{}, grid, planLevels},
		{"boş seviye", mass, grid, nil},
		{"sıfır seviye", mass, grid, []float64{0}},
		{"1'den büyük seviye", mass, grid, []float64{1.5}},
		{"negatif seviye", mass, grid, []float64{-0.5}},
	}
	for _, tc := range cases {
		if _, err := Contours(tc.mass, tc.grid, tc.levels); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}

// TestContours_PartCountDistribution, K8'in erken sinyalidir (ADR-21 girdisi).
//
// Eşik (`p95_part_count ≤ 3`) S4 sonunda gerçek koşuda ölçülür; buradaki amaç
// parçalanmanın büyüklük mertebesini şimdi görmektir: kütle çok tepeliyse
// %50 konturu onlarca parçaya bölünebilir ve bu, çözünürlük kararına girdi
// olur (komşu kısıtı ADR-03 gereği zayıflatılmaz).
func TestContours_PartCountDistribution(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for _, tc := range []struct {
		name string
		p    profileSpec
		res  float64
	}{
		{"kentsel 100 m", urbanProfile(), 100},
		{"kırsal 250 m", ruralProfile(), 250},
	} {
		inv := buildNetwork(t, tc.p)
		grid := mustGrid(t, tc.res)
		opts := DefaultOptions(testConfig(tc.p), 8)

		const events = 40
		stats := map[float64][]int{}

		for i := 0; i < events; i++ {
			var taValue *int
			if i%2 == 0 {
				taValue = intPtr(1 + rng.Intn(40))
			}
			res, err := Estimate(record(inv, i, taValue), inv, grid, opts)
			if err != nil {
				t.Fatalf("%s: Estimate: %v", tc.name, err)
			}
			contours, err := Contours(res.Mass, grid, planLevels)
			if err != nil {
				t.Fatalf("%s: Contours: %v", tc.name, err)
			}
			for _, c := range contours {
				mp, err := grid.Polygonize(c.Cells)
				if err != nil {
					t.Fatalf("%s: Polygonize: %v", tc.name, err)
				}
				if mp.PartCount() < 1 {
					t.Fatalf("%s: part_count %d — PBT #5 ihlali", tc.name, mp.PartCount())
				}
				stats[c.Level] = append(stats[c.Level], mp.PartCount())
			}
		}

		for _, level := range planLevels {
			counts := stats[level]
			sort.Ints(counts)
			p50 := counts[len(counts)/2]
			p95 := counts[(len(counts)*95)/100]
			t.Logf("%s · M@%.0f%%: part_count medyan=%d p95=%d maks=%d (%d olay)",
				tc.name, level*100, p50, p95, counts[len(counts)-1], len(counts))
		}
	}
}
