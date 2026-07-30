package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configsDir, depo kökündeki senaryo config dizini (test paketine göre göreli).
const configsDir = "../../configs"

// TestLoad_AllScenarioConfigs, Sprint 0'da yazılmış dört senaryo dosyasının
// şemaya uyduğunu ve doğrulamadan geçtiğini garanti eder.
func TestLoad_AllScenarioConfigs(t *testing.T) {
	tests := []struct {
		file       string
		scenario   string
		morphology Morphology
		taEnabled  bool
	}{
		{"urban_ta.yaml", "A", MorphologyUrban, true},
		{"urban_no_ta.yaml", "B", MorphologyUrban, false},
		{"rural_ta.yaml", "C", MorphologyRural, true},
		{"rural_no_ta.yaml", "D", MorphologyRural, false},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			s, err := Load(filepath.Join(configsDir, tc.file))
			if err != nil {
				t.Fatalf("Load(%s): %v", tc.file, err)
			}
			if s.Run.Scenario != tc.scenario {
				t.Errorf("run.scenario = %q, beklenen %q", s.Run.Scenario, tc.scenario)
			}
			if s.Morphology != tc.morphology {
				t.Errorf("morphology = %q, beklenen %q", s.Morphology, tc.morphology)
			}
			if s.TimingAdvance.Enabled != tc.taEnabled {
				t.Errorf("timing_advance.enabled = %v, beklenen %v",
					s.TimingAdvance.Enabled, tc.taEnabled)
			}
			// ADR-17: profil morfolojiden çözümlenmiş olmalı
			if s.Profile.Morphology != tc.morphology {
				t.Errorf("profil çözümlenmedi: %q", s.Profile.Morphology)
			}
		})
	}
}

// TestLoad_ProfileAppliedPerMorphology, ADR-17'nin ana vaadini sınar:
// YAML'da yalnızca `morphology` değişince sekiz alan birlikte değişir.
func TestLoad_ProfileAppliedPerMorphology(t *testing.T) {
	urban, err := Load(filepath.Join(configsDir, "urban_ta.yaml"))
	if err != nil {
		t.Fatalf("urban_ta.yaml: %v", err)
	}
	rural, err := Load(filepath.Join(configsDir, "rural_ta.yaml"))
	if err != nil {
		t.Fatalf("rural_ta.yaml: %v", err)
	}

	checks := []struct {
		field       string
		urban, rral float64
	}{
		{"AreaRadiusKM", urban.Profile.AreaRadiusKM, rural.Profile.AreaRadiusKM},
		{"InterSiteDistanceM", urban.Profile.InterSiteDistanceM, rural.Profile.InterSiteDistanceM},
		{"AntHeightM", urban.Profile.AntHeightM, rural.Profile.AntHeightM},
		{"CommuteSpeedKMH", urban.Profile.CommuteSpeedKMH, rural.Profile.CommuteSpeedKMH},
		{"DMaxKM", urban.Profile.DMaxKM, rural.Profile.DMaxKM},
		{"RMaxM", urban.Profile.RMaxM, rural.Profile.RMaxM},
	}
	for _, c := range checks {
		if c.urban == c.rral {
			t.Errorf("%s kentsel ve kırsalda aynı (%g): morfoloji profili uygulanmamış",
				c.field, c.urban)
		}
	}

	// Karar: r_max urban 5 km / rural 20 km
	if urban.Profile.RMaxM != 5000 {
		t.Errorf("kentsel r_max = %g m, beklenen 5000", urban.Profile.RMaxM)
	}
	if rural.Profile.RMaxM != 20000 {
		t.Errorf("kırsal r_max = %g m, beklenen 20000", rural.Profile.RMaxM)
	}
	if urban.Profile.PropagationModel != ModelUMa {
		t.Errorf("kentsel model = %q, beklenen UMa", urban.Profile.PropagationModel)
	}
	if rural.Profile.PropagationModel != ModelRMa {
		t.Errorf("kırsal model = %q, beklenen RMa", rural.Profile.PropagationModel)
	}
}

// minimalYAML, testlerde temel alınan geçerli senaryo gövdesidir.
const minimalYAML = `
run:
  scenario: "A"
  name: "test"
  seed: 42
morphology: "urban"
area:
  origin_lat: 38.6748
  origin_lon: 39.2225
  radius_km: 5
simulation:
  agents: 10
  duration_days: 1
  tick_minutes: 5
network:
  sites_min: 10
  sites_max: 20
  rx_sensitivity_dbm: -110
radio:
  shadowing_sigma_db: 7
  d_max_km: 5
timing_advance:
  enabled: true
  technology: "LTE"
analysis:
  grid_resolution_m: 100
  contour_levels: [0.50, 0.90]
  neighbor_max_count: 8
  sample:
    calibration_events: 100
    validation_events: 0
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
privacy:
  k_anonymity: 5
  hmac_salt_env: "HMAC_SALT"
api:
  max_geometries_per_request: 500
`

// TestParse_Overrides, ADR-17'nin "profil YAML'dan ezilebilir" kuralını sınar.
func TestParse_Overrides(t *testing.T) {
	yamlWithOverrides := strings.Replace(minimalYAML,
		"  rx_sensitivity_dbm: -110",
		`  rx_sensitivity_dbm: -110
  r_max_m: 4200
  beam_width_deg: 90
  tilt_deg: 4
  eirp_dbm: 55
  ant_height_m: 30
  inter_site_distance_m: 750
  propagation_model: "UMi"
  freq_bands:
    - {mhz: 700, weight: 0.6}
    - {mhz: 900, weight: 0.4}`, 1)

	s, err := Parse([]byte(yamlWithOverrides))
	if err != nil {
		t.Fatalf("override'lı config çözümlenemedi: %v", err)
	}

	if s.Profile.RMaxM != 4200 {
		t.Errorf("RMaxM = %g, beklenen 4200 (override)", s.Profile.RMaxM)
	}
	if s.Profile.BeamWidthDeg != 90 {
		t.Errorf("BeamWidthDeg = %g, beklenen 90 (override)", s.Profile.BeamWidthDeg)
	}
	if s.Profile.TiltDeg != 4 {
		t.Errorf("TiltDeg = %g, beklenen 4 (override)", s.Profile.TiltDeg)
	}
	if s.Profile.EIRPdBm != 55 {
		t.Errorf("EIRPdBm = %g, beklenen 55 (override)", s.Profile.EIRPdBm)
	}
	if s.Profile.AntHeightM != 30 {
		t.Errorf("AntHeightM = %g, beklenen 30 (override)", s.Profile.AntHeightM)
	}
	if s.Profile.InterSiteDistanceM != 750 {
		t.Errorf("InterSiteDistanceM = %g, beklenen 750 (override)", s.Profile.InterSiteDistanceM)
	}
	if s.Profile.PropagationModel != ModelUMi {
		t.Errorf("PropagationModel = %q, beklenen UMi (override)", s.Profile.PropagationModel)
	}
	if len(s.Profile.FreqDistribution) != 2 || s.Profile.FreqDistribution[0].MHz != 700 {
		t.Errorf("frekans dağılımı override edilmedi: %+v", s.Profile.FreqDistribution)
	}

	// Override edilmeyen alan profil varsayılanında kalmalı
	if s.Profile.CommuteSpeedKMH != 30 {
		t.Errorf("CommuteSpeedKMH = %g, beklenen 30 (profil varsayılanı)", s.Profile.CommuteSpeedKMH)
	}
}

// TestParse_NoOverrideKeepsProfileDefaults, override yokken varsayılanların
// bozulmadığını sınar.
func TestParse_NoOverrideKeepsProfileDefaults(t *testing.T) {
	s, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("minimal config çözümlenemedi: %v", err)
	}
	def, _ := ProfileFor(MorphologyUrban)
	if s.Profile.RMaxM != def.RMaxM || s.Profile.BeamWidthDeg != def.BeamWidthDeg {
		t.Errorf("override yokken profil varsayılanı korunmalı: %+v", s.Profile)
	}
}

// TestParse_UnknownFieldRejected, strict decode'un yazım hatalarını yakaladığını
// sınar — sessizce yok sayılan alan, gözden kaçan senaryo farkı demektir.
func TestParse_UnknownFieldRejected(t *testing.T) {
	bad := minimalYAML + "\nunknown_top_level: 1\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("bilinmeyen alan için hata bekleniyordu, nil döndü")
	}
}

// TestParse_ValidationRules, doğrulama kurallarının fail-fast davrandığını sınar.
func TestParse_ValidationRules(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{"geçersiz senaryo", `scenario: "A"`, `scenario: "X"`},
		{"geçersiz morfoloji", `morphology: "urban"`, `morphology: "suburban"`},
		{"pozitif rx duyarlılığı", "rx_sensitivity_dbm: -110", "rx_sensitivity_dbm: 110"},
		{"sites_max < sites_min", "sites_max: 20", "sites_max: 5"},
		{"ajan sayısı 0", "agents: 10", "agents: 0"},
		{"tick 0", "tick_minutes: 5", "tick_minutes: 0"},
		{"geçersiz TA teknolojisi", `technology: "LTE"`, `technology: "NR"`},
		{"kontur seviyesi 1", "contour_levels: [0.50, 0.90]", "contour_levels: [0.50, 1.0]"},
		{"boş kontur listesi", "contour_levels: [0.50, 0.90]", "contour_levels: []"},
		{"split_ratio 1", "split_ratio: 0.80", "split_ratio: 1.0"},
		{"lambda aralığı ters", "lambda_max: 3.0", "lambda_max: 0.4"},
		{"enjeksiyon oranı 1", "injection_rate: 0.02", "injection_rate: 1.0"},
		{"eksik kural ağırlığı", "rule_weights: {1: 0.2, 2: 0.2, 3: 0.2, 4: 0.2, 5: 0.2}",
			"rule_weights: {1: 0.5, 2: 0.5}"},
		{"kural id 6", "rule_weights: {1: 0.2, 2: 0.2, 3: 0.2, 4: 0.2, 5: 0.2}",
			"rule_weights: {1: 0.2, 2: 0.2, 3: 0.2, 4: 0.2, 6: 0.2}"},
		{"k_anonymity 0", "k_anonymity: 5", "k_anonymity: 0"},
		{"boş hmac env", `hmac_salt_env: "HMAC_SALT"`, `hmac_salt_env: ""`},
		{"geçersiz enlem", "origin_lat: 38.6748", "origin_lat: 95.0"},
		{"geçersiz boylam", "origin_lon: 39.2225", "origin_lon: 200.0"},
		{"grid çözünürlüğü 0", "grid_resolution_m: 100", "grid_resolution_m: 0"},
		{"api limiti 0", "max_geometries_per_request: 500", "max_geometries_per_request: 0"},
		{"geçersiz override modeli", "rx_sensitivity_dbm: -110",
			"rx_sensitivity_dbm: -110\n  propagation_model: \"COST231\""},
		{"geçersiz override r_max", "rx_sensitivity_dbm: -110",
			"rx_sensitivity_dbm: -110\n  r_max_m: -5"},
		// Tespit bölümü (ADR-27..31). Sıfır bırakılan alan varsayılan alır,
		// bu yüzden geçersizlik ancak açıkça yazılmış kötü değerle sınanır.
		{"margin üst sınırı 1'in altında", "injection_rate: 0.02",
			"injection_rate: 0.02\n  detection:\n    velocity_margin_cap: 0.5"},
		{"negatif zaman toleransı", "injection_rate: 0.02",
			"injection_rate: 0.02\n  detection:\n    time_backstep_tolerance_s: -1"},
		{"aktivite desteği 1'in altında", "injection_rate: 0.02",
			"injection_rate: 0.02\n  detection:\n    activity_min_support: -3"},
		{"asgari bulgu sayısı negatif", "injection_rate: 0.02",
			"injection_rate: 0.02\n  detection:\n    min_findings_for_threshold: -1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(minimalYAML, tc.old, tc.new, 1)
			if mutated == minimalYAML {
				t.Fatalf("test kurulumu hatalı: %q bulunamadı", tc.old)
			}
			if _, err := Parse([]byte(mutated)); err == nil {
				t.Errorf("%s: hata bekleniyordu, nil döndü", tc.name)
			}
		})
	}
}

// TestParse_DetectionDefaults, tespit bölümü yazılmadığında ADR sabitlerinin
// uygulandığını sınar (ADR-29, ADR-31).
//
// Varsayılanların sessizce yanlış olması, kural 2'nin `margin` alanının
// veritabanı CHECK kısıtına takılması veya K7 ölçülebilirlik eşiğinin
// kaybolması demek olurdu; ikisi de ancak tam koşuda fark edilirdi.
func TestParse_DetectionDefaults(t *testing.T) {
	s, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	d := s.Integrity.Detection
	if d.VelocityMarginCap != DefaultVelocityMarginCap {
		t.Errorf("velocity_margin_cap = %g, beklenen %g", d.VelocityMarginCap, DefaultVelocityMarginCap)
	}
	if d.TimeBackstepToleranceS != 0 {
		t.Errorf("time_backstep_tolerance_s = %g, beklenen 0 (ADR-29 katı karşılaştırma)",
			d.TimeBackstepToleranceS)
	}
	if d.ActivityMinSupport != DefaultActivityMinSupport {
		t.Errorf("activity_min_support = %d, beklenen %d", d.ActivityMinSupport, DefaultActivityMinSupport)
	}
	if d.MinFindingsForThreshold != DefaultMinFindingsForThreshold {
		t.Errorf("min_findings_for_threshold = %d, beklenen %d",
			d.MinFindingsForThreshold, DefaultMinFindingsForThreshold)
	}
}

// TestLoad_DetectionDeclaredInEveryScenario, dört senaryo config'inin ve
// duman testi config'inin tespit parametrelerini **açıkça** beyan ettiğini
// sınar.
//
// Varsayılana güvenmek yeterli olurdu; ama ADR-29 spesifikasyonun ölçümden önce
// yazılı olmasını şart koşuyor. Config'de görünmeyen bir eşik, raporda
// "önceden beyan edildi" diye savunulamaz.
func TestLoad_DetectionDeclaredInEveryScenario(t *testing.T) {
	files := []string{
		"urban_ta.yaml", "urban_no_ta.yaml",
		"rural_ta.yaml", "rural_no_ta.yaml", "smoke.yaml",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(configsDir, file)

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("okunamadı: %v", err)
			}
			for _, key := range []string{
				"velocity_margin_cap", "time_backstep_tolerance_s",
				"activity_min_support", "min_findings_for_threshold",
			} {
				if !strings.Contains(string(raw), key) {
					t.Errorf("%s config'de beyan edilmemiş (ADR-29)", key)
				}
			}

			s, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := s.Integrity.Detection.MinFindingsForThreshold; got != 30 {
				t.Errorf("min_findings_for_threshold = %d, ADR-31 30 diyor", got)
			}
			if got := s.Integrity.MaxVelocityKMH; got != 300 {
				t.Errorf("max_velocity_kmh = %g, BÖLÜM C.1 300 diyor", got)
			}
		})
	}
}

// TestLoad_MissingFile, dosya yoksa açıklayıcı hata döndüğünü sınar.
func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "yok.yaml"))
	if err == nil {
		t.Fatal("olmayan dosya için hata bekleniyordu")
	}
	if !strings.Contains(err.Error(), "okunamadı") {
		t.Errorf("beklenmeyen hata mesajı: %v", err)
	}
}

// TestLoad_MalformedYAML, bozuk YAML'ın çözümleme hatası verdiğini sınar.
func TestLoad_MalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bozuk.yaml")
	if err := os.WriteFile(path, []byte("run:\n  scenario: [unclosed\n"), 0o600); err != nil {
		t.Fatalf("test dosyası yazılamadı: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("bozuk YAML için hata bekleniyordu")
	}
}
