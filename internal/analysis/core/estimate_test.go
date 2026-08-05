package core

import (
	"math"
	"sort"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// testConfig, profilden kütle parametrelerini kurar (λ = 1: kalibrasyon öncesi).
func testConfig(p profileSpec) density.Config {
	return density.Config{
		RxSensitivityDBm: p.rxSensDBm,
		SigmaNominalDB:   p.sigmaNominal,
		Lambda:           1,
		UTHeightM:        utHeight,
	}
}

// mustGrid, verilen çözünürlükte ızgara kurar.
func mustGrid(t testing.TB, resolutionM float64) *density.Grid {
	t.Helper()
	g, err := density.NewGrid(resolutionM)
	if err != nil {
		t.Fatalf("NewGrid: %v", err)
	}
	return g
}

// record, envanterdeki n. hücre için bir kayıt üretir.
func record(inv *params.Inventory, n int, taValue *int) Record {
	cell := inv.Cells()[n%inv.Len()]
	return Record{CellID: cell.ID, TAValue: taValue, Technology: ta.LTE}
}

func intPtr(v int) *int { return &v }

// TestEstimate_MassSumsToOne, PBT değişmezi 1'i sınar: |Σ mass − 1| < 1e-9.
func TestEstimate_MassSumsToOne(t *testing.T) {
	for _, p := range []profileSpec{urbanProfile(), ruralProfile()} {
		t.Run(p.name, func(t *testing.T) {
			inv := buildNetwork(t, p)
			grid := mustGrid(t, 250) // testlerde kaba ızgara: değişmez çözünürlükten bağımsız
			opts := DefaultOptions(testConfig(p), 8)

			for _, taValue := range []*int{nil, intPtr(12), intPtr(40)} {
				for n := 0; n < 12; n++ {
					res, err := Estimate(record(inv, n*7, taValue), inv, grid, opts)
					if err != nil {
						t.Fatalf("Estimate: %v", err)
					}
					total := density.TotalMass(res.Mass)
					if math.Abs(total-1) > density.MassTolerance {
						t.Fatalf("Σ mass = %.15f (hücre %d, TA %v)", total, n, taValue)
					}
					if len(res.Mass) == 0 {
						t.Fatal("kütle haritası boş")
					}
				}
			}
		})
	}
}

// TestEstimate_Deterministic, aynı girdinin bit düzeyinde aynı kütleyi
// verdiğini sınar — K10'un S3 karşılığı ve iki sürücünün (consumer / replay)
// aynı sonucu üretmesinin güvencesi.
func TestEstimate_Deterministic(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 250)
	opts := DefaultOptions(testConfig(p), 8)
	rec := record(inv, 5, intPtr(20))

	first, err := Estimate(rec, inv, grid, opts)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	for i := 0; i < 25; i++ {
		got, err := Estimate(rec, inv, grid, opts)
		if err != nil {
			t.Fatalf("Estimate: %v", err)
		}
		if len(got.Mass) != len(first.Mass) {
			t.Fatalf("%d. çağrı %d hücre, ilk çağrı %d", i, len(got.Mass), len(first.Mass))
		}
		for a, m := range first.Mass {
			if got.Mass[a] != m {
				t.Fatalf("%d. çağrı hücre %v: %.17g ≠ %.17g", i, a, got.Mass[a], m)
			}
		}
	}
}

// TestEstimate_LambdaChangesMass, λ enjeksiyonunun (T-E03-14) gerçekten
// motora ulaştığını sınar: farklı λ farklı kütle vermelidir.
func TestEstimate_LambdaChangesMass(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 250)
	rec := record(inv, 3, nil)

	prevSpread := -1.0
	for _, lambda := range []float64{0.5, 1.0, 2.0, 3.0} {
		cfg := testConfig(p)
		cfg.Lambda = lambda
		res, err := Estimate(rec, inv, grid, DefaultOptions(cfg, 8))
		if err != nil {
			t.Fatalf("λ=%.1f: %v", lambda, err)
		}

		// Yayılım ölçüsü: %90 kütleyi taşıyan hücre sayısı.
		spread := float64(cellsForMass(res.Mass, 0.90))
		t.Logf("λ=%.1f (σ_eff=%.1f dB) → %d hücre, %%90 kütle %d hücrede",
			lambda, cfg.SigmaEffDB(), res.CellCount, int(spread))

		if prevSpread >= 0 && spread < prevSpread {
			t.Errorf("λ büyüdükçe kütle yayılmalı (%.0f < %.0f)", spread, prevSpread)
		}
		prevSpread = spread
	}
}

// TestEstimate_TAConcentratesMass, TA bilgisinin kütleyi daralttığını sınar —
// K3 iddiasının motor düzeyindeki ön göstergesi.
func TestEstimate_TAConcentratesMass(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 100)
	opts := DefaultOptions(testConfig(p), 8)
	cell := inv.Cells()[0]

	withoutTA, err := Estimate(Record{CellID: cell.ID}, inv, grid, opts)
	if err != nil {
		t.Fatalf("TA'sız: %v", err)
	}
	withTA, err := Estimate(Record{CellID: cell.ID, TAValue: intPtr(20), Technology: ta.LTE}, inv, grid, opts)
	if err != nil {
		t.Fatalf("TA'lı: %v", err)
	}

	n90, t90 := cellsForMass(withoutTA.Mass, 0.90), cellsForMass(withTA.Mass, 0.90)
	t.Logf("%%90 kütle: TA'sız %d hücre, TA'lı %d hücre (daralma %%%.1f)",
		n90, t90, 100*(1-float64(t90)/float64(n90)))

	if !withTA.TAUsed {
		t.Error("ta_used false — halka bölgeyle kesişmeliydi")
	}
	if t90 >= n90 {
		t.Errorf("TA kütleyi daraltmalı (%d ≥ %d)", t90, n90)
	}
}

// TestEstimate_TAFallback, imkânsız TA değerinde geri düşüşü sınar
// (T-E03-04): halka dilimin tamamen dışındaysa TA yok sayılır.
func TestEstimate_TAFallback(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 250)
	opts := DefaultOptions(testConfig(p), 8)
	cell := inv.Cells()[0]

	// ta = 400 → halka [31.248 , 31.326) m; kentsel r_max ≈ 6.126 m.
	res, err := Estimate(Record{CellID: cell.ID, TAValue: intPtr(400), Technology: ta.LTE}, inv, grid, opts)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if res.TAUsed {
		t.Error("imkânsız TA'da ta_used true kaldı")
	}
	if total := density.TotalMass(res.Mass); math.Abs(total-1) > density.MassTolerance {
		t.Errorf("geri düşüşte Σ mass = %.15f", total)
	}

	// Geri düşüş, TA'sız kayıtla aynı kütleyi vermelidir.
	plain, err := Estimate(Record{CellID: cell.ID}, inv, grid, opts)
	if err != nil {
		t.Fatalf("TA'sız: %v", err)
	}
	for a, m := range plain.Mass {
		if res.Mass[a] != m {
			t.Fatalf("geri düşüş TA'sız kayıttan farklı (hücre %v: %.17g ≠ %.17g)", a, res.Mass[a], m)
		}
	}
}

// TestEstimate_NeighbourConstraintApplied, komşu kısıtının kütleyi
// değiştirdiğini sınar (ADR-03).
func TestEstimate_NeighbourConstraintApplied(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 250)
	rec := record(inv, 11, nil)

	off, err := Estimate(rec, inv, grid, Options{Config: testConfig(p), NeighbourMaxCount: 0, IncludeAngular: true})
	if err != nil {
		t.Fatalf("kısıtsız: %v", err)
	}
	on, err := Estimate(rec, inv, grid, DefaultOptions(testConfig(p), 8))
	if err != nil {
		t.Fatalf("kısıtlı: %v", err)
	}

	if on.NeighbourCount == 0 {
		t.Fatal("komşu seçilmedi — ön filtre fazla eliyor")
	}
	n90off, n90on := cellsForMass(off.Mass, 0.90), cellsForMass(on.Mass, 0.90)
	t.Logf("komşu kısıtı: %d komşu, %%90 kütle %d → %d hücre", on.NeighbourCount, n90off, n90on)

	if n90on >= n90off {
		t.Errorf("komşu kısıtı kütleyi daraltmalı (%d ≥ %d)", n90on, n90off)
	}
}

// TestEstimate_Rejects, hatalı girdileri kapsar.
func TestEstimate_Rejects(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 250)
	opts := DefaultOptions(testConfig(p), 8)

	if _, err := Estimate(Record{}, inv, grid, opts); err == nil {
		t.Error("bilinmeyen hücre kabul edildi")
	}
	if _, err := Estimate(record(inv, 0, nil), nil, grid, opts); err == nil {
		t.Error("nil envanter kabul edildi")
	}
	if _, err := Estimate(record(inv, 0, nil), inv, nil, opts); err == nil {
		t.Error("nil ızgara kabul edildi")
	}
	bad := opts
	bad.Config.Lambda = 0
	if _, err := Estimate(record(inv, 0, nil), inv, grid, bad); err == nil {
		t.Error("geçersiz λ kabul edildi")
	}
	if _, err := Estimate(Record{CellID: inv.Cells()[0].ID, TAValue: intPtr(-1), Technology: ta.LTE},
		inv, grid, opts); err == nil {
		t.Error("negatif TA kabul edildi")
	}
}

// TestPBT_MassInvariants, PBT değişmezleri 1 ve 8'i rastgele girdilerde sınar.
//
//	#1  |Σ mass − 1| < 1e-9
//	#8  w_nbr(p) ∈ [0,1] ve Δ→+∞ iken →1
func TestPBT_MassInvariants(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 300)

	rapid.Check(t, func(rt *rapid.T) {
		cfg := testConfig(p)
		cfg.Lambda = rapid.Float64Range(0.5, 3.0).Draw(rt, "lambda")

		var taValue *int
		if rapid.Bool().Draw(rt, "hasTA") {
			taValue = intPtr(rapid.IntRange(0, 78).Draw(rt, "ta"))
		}

		rec := record(inv, rapid.IntRange(0, inv.Len()-1).Draw(rt, "cell"), taValue)
		opts := Options{
			Config:            cfg,
			NeighbourMaxCount: rapid.IntRange(0, 8).Draw(rt, "neighbours"),
			IncludeAngular:    rapid.Bool().Draw(rt, "angular"),
		}

		res, err := Estimate(rec, inv, grid, opts)
		if err != nil {
			rt.Fatalf("Estimate: %v", err)
		}
		if total := density.TotalMass(res.Mass); math.Abs(total-1) > density.MassTolerance {
			rt.Fatalf("PBT #1 bozuldu: Σ mass = %.15f", total)
		}
		for a, m := range res.Mass {
			if m < 0 || m > 1 || math.IsNaN(m) {
				rt.Fatalf("hücre %v kütlesi [0,1] dışında: %v", a, m)
			}
		}
	})
}

// TestPBT_NeighbourWeightBounds, PBT değişmezi 8'i doğrudan sınar.
func TestPBT_NeighbourWeightBounds(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	cells := inv.Cells()

	rapid.Check(t, func(rt *rapid.T) {
		cfg := testConfig(p)
		cfg.Lambda = rapid.Float64Range(0.5, 3.0).Draw(rt, "lambda")

		serving := cells[rapid.IntRange(0, len(cells)-1).Draw(rt, "serving")]
		point := geo.Point{
			X: rapid.Float64Range(-6000, 6000).Draw(rt, "x"),
			Y: rapid.Float64Range(-6000, 6000).Draw(rt, "y"),
		}

		neighbours := density.SelectNeighbours(inv, serving, point, 8, cfg.UTHeightM)
		w := density.Neighbour(serving, neighbours, point, cfg)
		if math.IsNaN(w) || w < 0 || w > 1 {
			rt.Fatalf("w_nbr = %v — [0,1] dışında", w)
		}

		// Komşu yoksa kısıt uygulanmaz.
		if got := density.Neighbour(serving, nil, point, cfg); got != 1 {
			rt.Fatalf("komşusuz w_nbr = %v, 1 beklenir", got)
		}
	})
}

// cellsForMass, kümülatif kütlenin hedefe ulaşması için gereken hücre sayısını
// döndürür (kontur alanının S4 öncesi vekili).
func cellsForMass(mass map[geo.Axial]float64, target float64) int {
	values := make([]float64, 0, len(mass))
	for _, m := range mass {
		values = append(values, m)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(values)))

	cum := 0.0
	for i, v := range values {
		cum += v
		if cum >= target {
			return i + 1
		}
	}
	return len(values)
}
