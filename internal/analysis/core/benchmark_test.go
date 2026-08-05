package core

import (
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// Gün-1 kapasite ölçümü (İ-2).
//
// Kırsalda r_max ≈ 29 km ve 65°'lik dilim, 100 m ızgarada on binlerce hücre
// demektir. Her hücrede serving + komşular için yol kaybı, her yol kaybında
// LOS/NLOS karışımı (iki model çağrısı) hesaplanır. Bu ölçüm, ADR-18/6'nın
// "çözünürlük benchmark sonrasında sabitlenir" kararının girdisidir.

// scenarioCost, tek bir yapılandırmanın olay başına maliyetini ölçer.
type scenarioCost struct {
	profile       profileSpec
	resolutionM   float64
	cellsPerEvent int
	perEvent      time.Duration
}

// measureCost, verilen profil ve çözünürlükte olay başına maliyeti ölçer.
func measureCost(t testing.TB, p profileSpec, resolutionM float64, samples int, taValue *int) scenarioCost {
	t.Helper()

	inv := buildNetwork(t, p)
	grid := mustGrid(t, resolutionM)
	opts := DefaultOptions(testConfig(p), 8)

	// Isınma: ilk çağrı ayırmaları yapar.
	if _, err := Estimate(record(inv, 0, taValue), inv, grid, opts); err != nil {
		t.Fatalf("ısınma: %v", err)
	}

	cells := 0
	start := time.Now()
	for i := 0; i < samples; i++ {
		res, err := Estimate(record(inv, i*13, taValue), inv, grid, opts)
		if err != nil {
			t.Fatalf("Estimate: %v", err)
		}
		cells += res.CellCount
	}
	elapsed := time.Since(start)

	return scenarioCost{
		profile:       p,
		resolutionM:   resolutionM,
		cellsPerEvent: cells / samples,
		perEvent:      elapsed / time.Duration(samples),
	}
}

// TestCapacityBudget, gün-1 kapasite tablosunu üretir.
//
// Eşik iddiası yoktur; ölçüm ADR-18/6 kararının girdisidir. Yalnızca açık
// bir tutarsızlık (sıfır hücre, ölçülemeyen süre) hata sayılır.
func TestCapacityBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("kapasite ölçümü -short modda atlanır")
	}

	const validationEvents = 60000 // ADR-14: 'V' kümesinin tamamı
	const calibrationCost = 12 * 5000

	configs := []struct {
		profile     profileSpec
		resolutionM float64
		withTA      bool
	}{
		{urbanProfile(), 100, false},
		{urbanProfile(), 100, true},
		{ruralProfile(), 100, false},
		{ruralProfile(), 250, false},
		{ruralProfile(), 500, false},
		{ruralProfile(), 250, true},
	}

	t.Logf("%-8s %6s %5s %10s %12s %14s %14s",
		"profil", "ızgara", "TA", "hücre/olay", "süre/olay", "'V' (60k olay)", "kalibrasyon")

	for _, c := range configs {
		var taValue *int
		if c.withTA {
			taValue = intPtr(20)
		}
		samples := 40
		if c.profile.name == "kırsal" && c.resolutionM <= 100 {
			samples = 8 // pahalı yapılandırma
		}

		cost := measureCost(t, c.profile, c.resolutionM, samples, taValue)
		if cost.cellsPerEvent == 0 || cost.perEvent == 0 {
			t.Fatalf("%s @%.0f m: ölçüm geçersiz", c.profile.name, c.resolutionM)
		}

		t.Logf("%-8s %5.0fm %5v %10d %12s %14s %14s",
			cost.profile.name, cost.resolutionM, c.withTA, cost.cellsPerEvent,
			cost.perEvent.Round(time.Microsecond),
			(cost.perEvent * validationEvents).Round(time.Second),
			(cost.perEvent * calibrationCost).Round(time.Second))
	}
}

// TestAngularDoubleCounting, ADR-18'in "Ölçülecek" bölümünü koşar (A kararı).
//
// İki varyant aynı olaylar üzerinde koşulur:
//
//	üç çarpanlı      : w_ang · w_rad · w_nbr   (plan E.1)
//	w_ang düşürülmüş : w_rad · w_nbr           (desen yalnız P_s içinden)
//
// Ölçüt, %90 kütleyi taşıyan bölgenin alanıdır — kontur çıkarımının (S4)
// alan vekilidir: aynı hücre kümesi, poligonlaştırma öncesi.
//
// Eşik ölçümden **önce** beyan edilmiştir: medyan fark < %5 ise plan korunur.
func TestAngularDoubleCounting(t *testing.T) {
	if testing.Short() {
		t.Skip("varyant ölçümü -short modda atlanır")
	}

	const samples = 120

	for _, c := range []struct {
		profile     profileSpec
		resolutionM float64
		withTA      bool
	}{
		{urbanProfile(), 100, false},
		{urbanProfile(), 100, true},
	} {
		name := c.profile.name
		if c.withTA {
			name += " + TA"
		}

		t.Run(name, func(t *testing.T) {
			inv := buildNetwork(t, c.profile)
			grid := mustGrid(t, c.resolutionM)
			cellArea := grid.CellAreaM2()

			three := DefaultOptions(testConfig(c.profile), 8)
			two := three
			two.IncludeAngular = false

			var taValue *int
			if c.withTA {
				taValue = intPtr(20)
			}

			ratios := make([]float64, 0, samples)
			areasThree := make([]float64, 0, samples)
			areasTwo := make([]float64, 0, samples)

			for i := 0; i < samples; i++ {
				rec := record(inv, i*7, taValue)

				a, err := Estimate(rec, inv, grid, three)
				if err != nil {
					t.Fatalf("üç çarpanlı: %v", err)
				}
				b, err := Estimate(rec, inv, grid, two)
				if err != nil {
					t.Fatalf("iki çarpanlı: %v", err)
				}

				areaA := float64(cellsForMass(a.Mass, 0.90)) * cellArea
				areaB := float64(cellsForMass(b.Mass, 0.90)) * cellArea
				areasThree = append(areasThree, areaA)
				areasTwo = append(areasTwo, areaB)
				if areaB > 0 {
					ratios = append(ratios, areaA/areaB)
				}
			}

			medThree := median(areasThree)
			medTwo := median(areasTwo)
			medRatio := median(ratios)
			diffPct := 100 * (medTwo - medThree) / medTwo

			t.Logf("örneklem %d olay, ızgara %.0f m", samples, c.resolutionM)
			t.Logf("  medyan alan@90  üç çarpanlı : %s", km2(medThree))
			t.Logf("  medyan alan@90  w_ang'sız   : %s", km2(medTwo))
			t.Logf("  medyan oran (üç/iki)        : %.4f", medRatio)
			t.Logf("  → w_ang'ın yarattığı daralma: %%%.2f  (eşik %%5)", diffPct)

			if math.IsNaN(diffPct) {
				t.Fatal("ölçüm geçersiz")
			}
		})
	}
}

// BenchmarkEstimate, olay başına maliyeti standart Go benchmark'ı olarak ölçer.
func BenchmarkEstimate(b *testing.B) {
	cases := []struct {
		name        string
		profile     profileSpec
		resolutionM float64
		taValue     *int
	}{
		{"kentsel/100m", urbanProfile(), 100, nil},
		{"kentsel/100m+TA", urbanProfile(), 100, intPtr(20)},
		{"kırsal/250m", ruralProfile(), 250, nil},
		{"kırsal/500m", ruralProfile(), 500, nil},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			inv := buildNetwork(b, c.profile)
			grid := mustGrid(b, c.resolutionM)
			opts := DefaultOptions(testConfig(c.profile), 8)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Estimate(record(inv, i, c.taValue), inv, grid, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkComponents, tek bir ızgara hücresindeki bileşen maliyetlerini ayırır.
func BenchmarkComponents(b *testing.B) {
	p := urbanProfile()
	inv := buildNetwork(b, p)
	cfg := testConfig(p)
	cell := inv.Cells()[0]
	point := cell.Site
	point.Y += 1500

	neighbours := density.SelectNeighbours(inv, cell, point, 8, cfg.UTHeightM)
	grid := mustGrid(b, 100)
	poly := grid.Corners(geo.Axial{Q: 15, R: 3})
	ring, _ := ta.NewRing(20, ta.LTE)

	b.Run("w_ang", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = density.Angular(cell, point, cfg.UTHeightM)
		}
	})
	b.Run("w_rad", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = density.Radial(cell, point, 1, cfg)
		}
	})
	b.Run("w_nbr", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = density.Neighbour(cell, neighbours, point, cfg)
		}
	})
	b.Run("örtüşme", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = geometry.OverlapRatio(poly, cell.Site, ring.InnerM, ring.OuterM)
		}
	})
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func km2(m2 float64) string { return fmt.Sprintf("%.4f km²", m2/1e6) }
