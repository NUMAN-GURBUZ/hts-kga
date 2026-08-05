package inventory

import (
	"math"
	"sort"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
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

// TestRMax_SolvedFromLinkBudget, r_max'in artık link budget'tan çözüldüğünü
// sınar (T-E02-05 revizyonu).
//
// Eski sürüm profil sabitini (kentsel 5 km / kırsal 20 km) döndürüyordu.
// Yeni sürümün doğruluk ölçütü sabit bir sayı değil, **tanımın kendisidir**:
// r_max mesafesinde alınan güç tam olarak alıcı duyarlılığına eşit olmalıdır.
func TestRMax_SolvedFromLinkBudget(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "urban_no_ta.yaml", "rural_ta.yaml", "rural_no_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			scn, inv := mustBuild(t, file)

			model, err := rf.ModelFor(scn.Profile.PropagationModel)
			if err != nil {
				t.Fatalf("rf.ModelFor: %v", err)
			}

			byFreq := map[int]float64{}
			for _, c := range inv.Cells {
				if !(c.RMaxM > 0) {
					t.Fatalf("hücre %s: r_max pozitif değil (%g)", c.ID, c.RMaxM)
				}
				byFreq[c.FreqMHz] = c.RMaxM

				spec := rf.RangeSpec{
					EIRPdBm:          c.EIRPdBm,
					RxSensitivityDBm: scn.Network.RxSensitivityDBm,
					HBSm:             c.AntHeightM,
					HUTm:             rf.UTHeightM,
					FreqMHz:          c.FreqMHz,
					BeamWidthDeg:     c.BeamWidth,
					TiltDeg:          c.TiltDeg,
				}
				// Tanımın testi: sınırda alınan güç = duyarlılık.
				got := spec.ReceivedPowerDBm(model, c.RMaxM)
				if math.Abs(got-scn.Network.RxSensitivityDBm) > 1e-6 {
					t.Errorf("hücre %s: r_max = %.1f m'de alınan güç %.6f dBm, duyarlılık %.1f dBm",
						c.ID, c.RMaxM, got, scn.Network.RxSensitivityDBm)
				}
			}

			for freq, r := range byFreq {
				t.Logf("%-18s %5d MHz → r_max = %8.1f m  (eski sabit %.0f m)",
					file, freq, r, scn.Profile.RMaxM)
			}
		})
	}
}

// TestRMax_DecreasesWithFrequency, aynı profilde yüksek bandın daha kısa
// eriştiğini sınar.
//
// Sabit r_max bunu yapamıyordu: 800 MHz ile 2600 MHz aynı yarıçapı görüyordu.
// Serbest uzay bağıntısı (20·log10 f) gereği yüksek frekans daha çok
// zayıflar, dolayısıyla daha erken duyarlılığa iner.
func TestRMax_DecreasesWithFrequency(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")

	byFreq := map[int]float64{}
	for _, c := range inv.Cells {
		byFreq[c.FreqMHz] = c.RMaxM
	}
	if len(byFreq) < 2 {
		t.Skipf("senaryoda tek band var (%v)", byFreq)
	}

	freqs := make([]int, 0, len(byFreq))
	for f := range byFreq {
		freqs = append(freqs, f)
	}
	sort.Ints(freqs)

	for i := 1; i < len(freqs); i++ {
		lo, hi := freqs[i-1], freqs[i]
		if byFreq[hi] >= byFreq[lo] {
			t.Errorf("%d MHz (%.1f m) ≥ %d MHz (%.1f m) — yüksek band daha kısa erişmeli",
				hi, byFreq[hi], lo, byFreq[lo])
		}
	}
	_ = scn
}

// TestRMax_ConfigOverride, kararın "şimdilik config'den okunacak" kısmını
// sınar: YAML'daki network.r_max_m profil değerini ezer.
func TestRMax_ConfigOverride(t *testing.T) {
	const override = 3750.0

	reparsed, err := config.Parse([]byte(overriddenYAML(override)))
	if err != nil {
		t.Fatalf("override'lı config: %v", err)
	}
	if got := RMaxOverride(reparsed); got == nil || *got != override {
		t.Errorf("RMaxOverride = %v, beklenen %g (config override)", got, override)
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

	if err := AssignRMax(nil, scn); err == nil {
		t.Error("boş hücre listesi için hata bekleniyordu")
	}
	if err := AssignRMax([]Cell{{}}, nil); err == nil {
		t.Error("nil senaryo için hata bekleniyordu")
	}
	// Modelsiz/parametresiz hücre link budget'tan çözülemez.
	if err := AssignRMax([]Cell{{}}, scn); err == nil {
		t.Error("boş hücre için hata bekleniyordu")
	}
	// Geçersiz açık override reddedilir.
	zero := 0.0
	bad := *scn
	bad.Network.RMaxM = &zero
	if err := AssignRMax([]Cell{{}}, &bad); err == nil {
		t.Error("r_max_m=0 override için hata bekleniyordu")
	}
}

// TestAssignRMax_InPlace, atamanın yerinde yapıldığını ve açık override'ın
// link budget'i ezdiğini sınar.
func TestAssignRMax_InPlace(t *testing.T) {
	const override = 12345.0
	scn := loadScenario(t, "rural_ta.yaml")
	value := override
	scn.Network.RMaxM = &value

	cells := []Cell{{}, {}, {}}
	if err := AssignRMax(cells, scn); err != nil {
		t.Fatalf("AssignRMax: %v", err)
	}
	for i, c := range cells {
		if c.RMaxM != override {
			t.Errorf("hücre[%d]: r_max = %g, beklenen %g", i, c.RMaxM, override)
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
