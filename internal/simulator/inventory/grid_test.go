package inventory

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

const configsDir = "../../../configs"

// testRunID, testlerde kullanılan sabit koşu kimliği (determinizm için).
var testRunID = uuid.MustParse("00000000-0000-5000-8000-000000000001")

// loadScenario, gerçek senaryo config'ini yükler.
func loadScenario(t *testing.T, file string) *config.Scenario {
	t.Helper()
	scn, err := config.Load(filepath.Join(configsDir, file))
	if err != nil {
		t.Fatalf("config.Load(%s): %v", file, err)
	}
	return scn
}

// newProjector, senaryo origin'i için izdüşüm kurar.
func newProjector(t *testing.T, scn *config.Scenario) *geo.Projector {
	t.Helper()
	p, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("geo.NewProjector: %v", err)
	}
	return p
}

// mustPlace, site yerleşimini çalıştırır.
func mustPlace(t *testing.T, file string) (*config.Scenario, *geo.Projector, *Layout) {
	t.Helper()
	scn := loadScenario(t, file)
	proj := newProjector(t, scn)
	layout, err := PlaceSites(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("PlaceSites(%s): %v", file, err)
	}
	return scn, proj, layout
}

// TestPlaceSites_CountWithinConfigRange, kararın ana gereksinimini sınar:
// site sayısı her senaryoda [sites_min, sites_max] aralığında olmalı.
func TestPlaceSites_CountWithinConfigRange(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "urban_no_ta.yaml", "rural_ta.yaml", "rural_no_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			scn, _, layout := mustPlace(t, file)

			n := len(layout.Sites)
			if n < scn.Network.SitesMin || n > scn.Network.SitesMax {
				t.Errorf("site sayısı = %d, beklenen aralık [%d, %d]",
					n, scn.Network.SitesMin, scn.Network.SitesMax)
			}
			t.Logf("%s: %d site, efektif ISD = %.1f m (nominal %.0f m), yarıçap %.0f m",
				file, n, layout.EffectiveISDM, layout.NominalISDM, layout.AreaRadiusM)
		})
	}
}

// TestPlaceSites_AllInsideArea, her sitenin kapsama alanı diski içinde
// kaldığını sınar (ADR-08 bounding box).
func TestPlaceSites_AllInsideArea(t *testing.T) {
	_, _, layout := mustPlace(t, "rural_ta.yaml")

	for _, s := range layout.Sites {
		if d := s.ENU.Norm(); d > layout.AreaRadiusM {
			t.Errorf("site %s alan dışında: d=%.1f m > %.0f m", s.ID, d, layout.AreaRadiusM)
		}
	}
}

// TestPlaceSites_FullAreaCoverage, dış halkanın boş bırakılmadığını sınar:
// en uzak site, alan yarıçapına bir ISD'den daha yakın olmalı.
func TestPlaceSites_FullAreaCoverage(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			_, _, layout := mustPlace(t, file)

			var maxDist float64
			for _, s := range layout.Sites {
				maxDist = math.Max(maxDist, s.ENU.Norm())
			}
			if gap := layout.AreaRadiusM - maxDist; gap > layout.EffectiveISDM {
				t.Errorf("dış halka boş: en uzak site %.0f m, alan yarıçapı %.0f m "+
					"(boşluk %.0f m > ISD %.0f m)",
					maxDist, layout.AreaRadiusM, gap, layout.EffectiveISDM)
			}
		})
	}
}

// TestPlaceSites_HexLatticeSpacing, kafesin gerçekten hex olduğunu sınar:
// her sitenin en yakın komşusu efektif ISD kadar uzakta olmalı ve iç
// bölgedeki sitelerin tam 6 komşusu bulunmalı.
func TestPlaceSites_HexLatticeSpacing(t *testing.T) {
	_, _, layout := mustPlace(t, "urban_ta.yaml")
	isd := layout.EffectiveISDM

	const tol = 1e-6
	for i, s := range layout.Sites {
		nearest := math.Inf(1)
		neighbors := 0
		for j, o := range layout.Sites {
			if i == j {
				continue
			}
			d := geo.Distance(s.ENU, o.ENU)
			nearest = math.Min(nearest, d)
			if math.Abs(d-isd) < tol {
				neighbors++
			}
		}

		if math.Abs(nearest-isd) > tol {
			t.Fatalf("site %d: en yakın komşu %.6f m, beklenen %.6f m", i, nearest, isd)
		}
		// İç bölge sitelerinin 6 komşusu olmalı (kenardakiler hariç)
		if s.ENU.Norm() < layout.AreaRadiusM-2*isd && neighbors != 6 {
			t.Errorf("iç bölge sitesi %d: %d komşu, beklenen 6", i, neighbors)
		}
	}
}

// TestPlaceSites_NoDuplicates, çakışan site veya kimlik olmadığını sınar.
func TestPlaceSites_NoDuplicates(t *testing.T) {
	_, _, layout := mustPlace(t, "rural_ta.yaml")

	seenID := make(map[uuid.UUID]bool, len(layout.Sites))
	seenAxial := make(map[geo.Axial]bool, len(layout.Sites))

	for _, s := range layout.Sites {
		if seenID[s.ID] {
			t.Errorf("site kimliği tekrarlanmış: %s", s.ID)
		}
		if seenAxial[s.Axial] {
			t.Errorf("axial koordinat tekrarlanmış: %+v", s.Axial)
		}
		if s.ID == uuid.Nil {
			t.Error("boş site kimliği üretildi")
		}
		seenID[s.ID] = true
		seenAxial[s.Axial] = true
	}
}

// TestPlaceSites_Deterministic, K10 gereksinimini sınar: aynı tohum + aynı
// run_id → birebir aynı yerleşim.
func TestPlaceSites_Deterministic(t *testing.T) {
	scn := loadScenario(t, "urban_ta.yaml")
	proj := newProjector(t, scn)

	a, err := PlaceSites(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("ilk yerleşim: %v", err)
	}
	b, err := PlaceSites(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("ikinci yerleşim: %v", err)
	}

	if a.EffectiveISDM != b.EffectiveISDM {
		t.Errorf("efektif ISD deterministik değil: %g vs %g", a.EffectiveISDM, b.EffectiveISDM)
	}
	if len(a.Sites) != len(b.Sites) {
		t.Fatalf("site sayısı deterministik değil: %d vs %d", len(a.Sites), len(b.Sites))
	}
	for i := range a.Sites {
		if a.Sites[i] != b.Sites[i] {
			t.Fatalf("site[%d] deterministik değil:\n  %+v\n  %+v", i, a.Sites[i], b.Sites[i])
		}
	}
}

// TestPlaceSites_SeedChangesLayout, farklı tohumun farklı hedef site sayısı
// (dolayısıyla farklı kafes) ürettiğini sınar.
func TestPlaceSites_SeedChangesLayout(t *testing.T) {
	scn := loadScenario(t, "urban_ta.yaml")
	proj := newProjector(t, scn)

	base, err := PlaceSites(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("temel yerleşim: %v", err)
	}

	differs := false
	for _, seed := range []int64{1, 7, 99, 12345, 777001} {
		scn.Run.Seed = seed
		other, err := PlaceSites(testRunID, scn, proj)
		if err != nil {
			t.Fatalf("seed=%d: %v", seed, err)
		}
		if len(other.Sites) != len(base.Sites) {
			differs = true
		}
		if n := len(other.Sites); n < scn.Network.SitesMin || n > scn.Network.SitesMax {
			t.Errorf("seed=%d: site sayısı %d aralık dışı", seed, n)
		}
	}
	if !differs {
		t.Error("hiçbir tohum farklı site sayısı üretmedi: tohum yerleşime bağlanmamış")
	}
}

// TestPlaceSites_RunIDScopesSiteIDs, farklı koşuların kimlik çakışmadığını
// ama aynı koşunun tekrarlanabilir olduğunu sınar (ADR-05).
func TestPlaceSites_RunIDScopesSiteIDs(t *testing.T) {
	scn := loadScenario(t, "urban_ta.yaml")
	proj := newProjector(t, scn)

	runA := uuid.MustParse("11111111-1111-5111-8111-111111111111")
	runB := uuid.MustParse("22222222-2222-5222-8222-222222222222")

	a, err := PlaceSites(runA, scn, proj)
	if err != nil {
		t.Fatalf("runA: %v", err)
	}
	b, err := PlaceSites(runB, scn, proj)
	if err != nil {
		t.Fatalf("runB: %v", err)
	}

	ids := make(map[uuid.UUID]bool, len(a.Sites))
	for _, s := range a.Sites {
		ids[s.ID] = true
	}
	for _, s := range b.Sites {
		if ids[s.ID] {
			t.Fatalf("farklı koşularda aynı site kimliği üretildi: %s", s.ID)
		}
	}

	// Aynı koordinat + aynı koşu → aynı kimlik
	if got := siteID(runA, a.Sites[0].Axial); got != a.Sites[0].ID {
		t.Errorf("siteID deterministik değil: %s vs %s", got, a.Sites[0].ID)
	}
}

// TestPlaceSites_WGS84Consistency, ENU ve coğrafi konumların aynı noktayı
// gösterdiğini round-trip ile sınar.
func TestPlaceSites_WGS84Consistency(t *testing.T) {
	_, proj, layout := mustPlace(t, "rural_ta.yaml")

	for _, s := range layout.Sites {
		back := proj.Forward(s.WGS84)
		if math.Abs(back.X-s.ENU.X) > 1e-6 || math.Abs(back.Y-s.ENU.Y) > 1e-6 {
			t.Errorf("site %s: ENU/WGS84 tutarsız %+v vs %+v", s.ID, s.ENU, back)
		}
		if s.WGS84.Lat < -90 || s.WGS84.Lat > 90 || s.WGS84.Lon < -180 || s.WGS84.Lon > 180 {
			t.Errorf("site %s: geçersiz WGS84 %+v", s.ID, s.WGS84)
		}
	}
}

// TestPlaceSites_EffectiveISDMatchesDensity, efektif ISD ile ortaya çıkan
// yoğunluğun analitik formülle uyuştuğunu sınar (kenar etkisi payıyla).
func TestPlaceSites_EffectiveISDMatchesDensity(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			_, _, layout := mustPlace(t, file)

			area := math.Pi * layout.AreaRadiusM * layout.AreaRadiusM
			expected := area / (hexPackingFactor * layout.EffectiveISDM * layout.EffectiveISDM)
			ratio := float64(len(layout.Sites)) / expected

			if math.Abs(ratio-1) > 0.10 {
				t.Errorf("site yoğunluğu formülden %.1f%% sapıyor (%d gerçek / %.1f beklenen)",
					(ratio-1)*100, len(layout.Sites), expected)
			}
		})
	}
}

// TestPlaceSites_NilArgs, eksik bağımlılıkların fail-fast reddedildiğini sınar.
func TestPlaceSites_NilArgs(t *testing.T) {
	scn := loadScenario(t, "urban_ta.yaml")
	proj := newProjector(t, scn)

	if _, err := PlaceSites(testRunID, nil, proj); err == nil {
		t.Error("nil senaryo için hata bekleniyordu")
	}
	if _, err := PlaceSites(testRunID, scn, nil); err == nil {
		t.Error("nil izdüşüm için hata bekleniyordu")
	}
}

// TestSolveEffectiveISD_Monotonic, ikiye bölmenin dayandığı varsayımı sınar:
// site sayısı ISD'ye göre monoton azalır.
func TestSolveEffectiveISD_Monotonic(t *testing.T) {
	const radius = 5000.0

	prev := math.MaxInt32
	for isd := 200.0; isd <= 3000; isd += 100 {
		g, err := geo.NewHexGrid(isd)
		if err != nil {
			t.Fatalf("NewHexGrid(%g): %v", isd, err)
		}
		n := len(g.Cover(geo.Point{}, radius))
		if n > prev {
			t.Errorf("ISD=%g: site sayısı arttı (%d > %d) — monotonluk bozuk", isd, n, prev)
		}
		prev = n
	}
}

// TestSolveEffectiveISD_WideRangeOfTargets, çözücünün farklı alan/hedef
// bileşimlerinde aralığa ulaştığını sınar.
func TestSolveEffectiveISD_WideRangeOfTargets(t *testing.T) {
	tests := []struct {
		radius     float64
		minN, maxN int
	}{
		{5000, 100, 150},
		{20000, 100, 150},
		{1000, 10, 20},
		{50000, 200, 400},
		{5000, 1, 5},     // çok seyrek
		{2000, 500, 900}, // çok yoğun
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("r=%.0f_n=%d..%d", tc.radius, tc.minN, tc.maxN), func(t *testing.T) {
			target := (tc.minN + tc.maxN) / 2
			isd, cells, err := solveEffectiveISD(tc.radius, target, tc.minN, tc.maxN)
			if err != nil {
				t.Fatalf("solveEffectiveISD: %v", err)
			}
			if len(cells) < tc.minN || len(cells) > tc.maxN {
				t.Errorf("site sayısı %d, aralık [%d, %d]", len(cells), tc.minN, tc.maxN)
			}
			if !(isd > 0) {
				t.Errorf("ISD pozitif olmalı: %g", isd)
			}
		})
	}
}

// TestSolveEffectiveISD_InvalidInput, geçersiz girdilerin reddedildiğini sınar.
func TestSolveEffectiveISD_InvalidInput(t *testing.T) {
	tests := []struct {
		name             string
		radius           float64
		target, min, max int
	}{
		{"yarıçap 0", 0, 100, 100, 150},
		{"yarıçap negatif", -5000, 100, 100, 150},
		{"min 0", 5000, 100, 0, 150},
		{"max < min", 5000, 100, 150, 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := solveEffectiveISD(tc.radius, tc.target, tc.min, tc.max); err == nil {
				t.Error("hata bekleniyordu, nil döndü")
			}
		})
	}
}
