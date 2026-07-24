package inventory

import (
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// mustBuild, tam envanter üretim boru hattını çalıştırır.
func mustBuild(t *testing.T, file string) (*config.Scenario, *Inventory) {
	t.Helper()
	scn := loadScenario(t, file)
	proj := newProjector(t, scn)
	inv, err := Build(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("Build(%s): %v", file, err)
	}
	return scn, inv
}

// TestRMax_DecisionValues, kesin kararı sınar: kentsel 5 km, kırsal 20 km.
func TestRMax_DecisionValues(t *testing.T) {
	tests := []struct {
		file string
		want float64
	}{
		{"urban_ta.yaml", 5000},
		{"urban_no_ta.yaml", 5000},
		{"rural_ta.yaml", 20000},
		{"rural_no_ta.yaml", 20000},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			scn, inv := mustBuild(t, tc.file)

			if got := RMax(scn.Profile); got != tc.want {
				t.Errorf("RMax(profil) = %g m, beklenen %g m", got, tc.want)
			}
			for _, c := range inv.Cells {
				if c.RMaxM != tc.want {
					t.Fatalf("hücre %s: r_max = %g m, beklenen %g m", c.ID, c.RMaxM, tc.want)
				}
			}
			if got := inv.MaxRMaxM(); got != tc.want {
				t.Errorf("MaxRMaxM() = %g, beklenen %g", got, tc.want)
			}
		})
	}
}

// TestRMax_ConfigOverride, kararın "şimdilik config'den okunacak" kısmını
// sınar: YAML'daki network.r_max_m profil değerini ezer.
func TestRMax_ConfigOverride(t *testing.T) {
	const override = 3750.0

	reparsed, err := config.Parse([]byte(overriddenYAML(override)))
	if err != nil {
		t.Fatalf("override'lı config: %v", err)
	}
	if got := RMax(reparsed.Profile); got != override {
		t.Errorf("RMax = %g, beklenen %g (config override)", got, override)
	}

	proj := newProjector(t, reparsed)
	inv, err := Build(testRunID, reparsed, proj)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, c := range inv.Cells {
		if c.RMaxM != override {
			t.Fatalf("hücre %s: r_max = %g, beklenen %g", c.ID, c.RMaxM, override)
		}
	}
}

// overriddenYAML, r_max_m override'ı içeren minimal senaryo üretir.
func overriddenYAML(rMax float64) string {
	return `
run: {scenario: "A", name: "override testi", seed: 42}
morphology: "urban"
area: {origin_lat: 38.6748, origin_lon: 39.2225, radius_km: 5}
simulation: {agents: 10, duration_days: 1, tick_minutes: 5}
network:
  sites_min: 100
  sites_max: 150
  rx_sensitivity_dbm: -110
  r_max_m: ` + strconv.FormatFloat(rMax, 'f', -1, 64) + `
radio: {shadowing_sigma_db: 7, d_max_km: 5}
timing_advance: {enabled: true, technology: "LTE"}
analysis:
  grid_resolution_m: 100
  contour_levels: [0.50, 0.90]
  neighbor_max_count: 8
  sample: {calibration_events: 100, validation_events: 0}
calibration:
  split_ratio: 0.80
  lambda_min: 0.5
  lambda_max: 3.0
  target_coverage: 0.90
  tolerance: 0.005
  max_iterations: 12
integrity:
  max_velocity_kmh: 300
  injection_rate: 0.02
  rule_weights: {1: 0.2, 2: 0.2, 3: 0.2, 4: 0.2, 5: 0.2}
privacy: {k_anonymity: 5, hmac_salt_env: "HMAC_SALT"}
api: {max_geometries_per_request: 500}
`
}

// TestRMax_CoversWholeArea, Sprint 1 sadeleştirmesinin sonucunu belgeler:
// r_max alan yarıçapına eşit olduğu için kapsama boşluğu oluşmaz (ADR-08).
func TestRMax_CoversWholeArea(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			_, inv := mustBuild(t, file)

			// Alanın herhangi bir noktası en az bir hücrenin r_max'i içinde olmalı
			radius := inv.Layout.AreaRadiusM
			probes := []geo.Point{
				{X: 0, Y: 0},
				{X: radius * 0.99, Y: 0},
				{X: 0, Y: -radius * 0.99},
				{X: radius * 0.7, Y: radius * 0.7},
			}

			for _, p := range probes {
				covered := false
				for _, c := range inv.Cells {
					if geo.Distance(p, c.ENU) <= c.RMaxM {
						covered = true
						break
					}
				}
				if !covered {
					t.Errorf("nokta %+v hiçbir hücrenin r_max'i içinde değil", p)
				}
			}
		})
	}
}

// TestAssignRMax_InvalidInput, geçersiz girdilerin reddedildiğini sınar.
func TestAssignRMax_InvalidInput(t *testing.T) {
	scn := loadScenario(t, "urban_ta.yaml")

	if err := AssignRMax(nil, scn.Profile); err == nil {
		t.Error("boş hücre listesi için hata bekleniyordu")
	}

	bad := scn.Profile
	bad.RMaxM = 0
	if err := AssignRMax([]Cell{{}}, bad); err == nil {
		t.Error("r_max=0 için hata bekleniyordu")
	}
}

// TestAssignRMax_InPlace, atamanın yerinde yapıldığını sınar.
func TestAssignRMax_InPlace(t *testing.T) {
	scn := loadScenario(t, "rural_ta.yaml")
	cells := []Cell{{}, {}, {}}

	if err := AssignRMax(cells, scn.Profile); err != nil {
		t.Fatalf("AssignRMax: %v", err)
	}
	for i, c := range cells {
		if c.RMaxM != 20000 {
			t.Errorf("hücre[%d]: r_max = %g, beklenen 20000", i, c.RMaxM)
		}
	}
}

// TestBuild_Pipeline, uçtan uca envanter üretimini sınar.
func TestBuild_Pipeline(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "urban_no_ta.yaml", "rural_ta.yaml", "rural_no_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			scn, inv := mustBuild(t, file)

			if inv.RunID != testRunID {
				t.Errorf("run_id = %s, beklenen %s", inv.RunID, testRunID)
			}
			if inv.CellCount() != inv.SiteCount()*3 {
				t.Errorf("%d hücre / %d site — 3 katı olmalı", inv.CellCount(), inv.SiteCount())
			}
			if n := inv.SiteCount(); n < scn.Network.SitesMin || n > scn.Network.SitesMax {
				t.Errorf("site sayısı %d aralık dışı", n)
			}
			// Build sonrası her hücre şema kısıtlarını sağlamalı
			for _, c := range inv.Cells {
				if err := c.Validate(); err != nil {
					t.Fatalf("Build çıktısı doğrulamadan geçemedi: %v", err)
				}
			}
			t.Logf("%s: %d site, %d hücre, r_max %.0f m",
				file, inv.SiteCount(), inv.CellCount(), inv.MaxRMaxM())
		})
	}
}

// TestBuild_Deterministic, uçtan uca determinizmi sınar (K10).
func TestBuild_Deterministic(t *testing.T) {
	scn := loadScenario(t, "rural_ta.yaml")
	proj := newProjector(t, scn)

	a, err := Build(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("ilk üretim: %v", err)
	}
	b, err := Build(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("ikinci üretim: %v", err)
	}

	if len(a.Cells) != len(b.Cells) {
		t.Fatalf("hücre sayısı farklı: %d vs %d", len(a.Cells), len(b.Cells))
	}
	for i := range a.Cells {
		if a.Cells[i] != b.Cells[i] {
			t.Fatalf("hücre[%d] deterministik değil", i)
		}
	}
}

// TestBuild_RequiresRunID, ADR-05 gereksinimini sınar.
func TestBuild_RequiresRunID(t *testing.T) {
	scn := loadScenario(t, "urban_ta.yaml")
	proj := newProjector(t, scn)

	if _, err := Build(uuid.Nil, scn, proj); err == nil {
		t.Error("boş run_id için hata bekleniyordu")
	}
}
