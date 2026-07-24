// T-E02-01 — Senaryo YAML şeması ve yükleyici (ADR-17, O-01).
//
// Yükleme sırası:
//  1. YAML strict decode (bilinmeyen alan → hata; sessiz yazım hatası olmaz)
//  2. morphology → MorphologyProfile factory (ADR-17)
//  3. YAML'daki override alanları profile uygulanır
//  4. Bütünsel doğrulama (profil + senaryo)
package config

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ─── YAML şeması ──────────────────────────────────────────────────────────────

// RunSection, koşu kimliği ve determinizm ayarları (ADR-05, K10).
type RunSection struct {
	Scenario string `yaml:"scenario"` // A/B/C/D — run_config.scenario kısıtı
	Name     string `yaml:"name"`
	Seed     int64  `yaml:"seed"` // deterministik tekrarlanabilirlik
}

// AreaSection, kapsama alanı tanımı (ADR-08).
type AreaSection struct {
	OriginLat float64 `yaml:"origin_lat"`
	OriginLon float64 `yaml:"origin_lon"`
	RadiusKM  float64 `yaml:"radius_km"` // profil AreaRadiusKM'yi ezer
}

// SimulationSection, ajan ve zaman ayarları.
type SimulationSection struct {
	Agents       int `yaml:"agents"`
	DurationDays int `yaml:"duration_days"`
	TickMinutes  int `yaml:"tick_minutes"`
}

// NetworkSection, şebeke envanteri ayarları.
//
// İşaretçi alanlar opsiyonel profil override'larıdır (ADR-17): YAML'da yoksa
// MorphologyProfile varsayılanı geçerlidir.
type NetworkSection struct {
	SitesMin         int     `yaml:"sites_min"`
	SitesMax         int     `yaml:"sites_max"`
	RxSensitivityDBm float64 `yaml:"rx_sensitivity_dbm"`

	// ── Opsiyonel profil override'ları ──
	InterSiteDistanceM *float64   `yaml:"inter_site_distance_m"`
	AntHeightM         *float64   `yaml:"ant_height_m"`
	PropagationModel   *string    `yaml:"propagation_model"`
	FreqBands          []FreqBand `yaml:"freq_bands"`
	BeamWidthDeg       *float64   `yaml:"beam_width_deg"`
	TiltDeg            *float64   `yaml:"tilt_deg"`
	EIRPdBm            *float64   `yaml:"eirp_dbm"`
	RMaxM              *float64   `yaml:"r_max_m"` // T-E02-05: RF modeli gelene kadar sabit
}

// RadioSection, yayılım ve gölgeleme ayarları.
type RadioSection struct {
	ShadowingSigmaDB float64 `yaml:"shadowing_sigma_db"` // σ_nominal; σ_eff = λ·σ_nominal
	DMaxKM           float64 `yaml:"d_max_km"`           // profil DMaxKM'yi ezer
}

// TimingAdvanceSection, TA ayarları (senaryo A/C açık, B/D kapalı).
type TimingAdvanceSection struct {
	Enabled    bool   `yaml:"enabled"`
	Technology string `yaml:"technology"` // LTE | GSM
}

// SampleSection, kalibrasyon/doğrulama örneklem büyüklükleri (ADR-14).
type SampleSection struct {
	CalibrationEvents int `yaml:"calibration_events"`
	ValidationEvents  int `yaml:"validation_events"` // 0 = 'V' kümesinin tamamı
}

// AnalysisSection, analiz motoru ayarları (ADR-07, ADR-03, ADR-14).
type AnalysisSection struct {
	GridResolutionM  float64       `yaml:"grid_resolution_m"` // hex merkez-merkez (ADR-07)
	ContourLevels    []float64     `yaml:"contour_levels"`
	NeighborMaxCount int           `yaml:"neighbor_max_count"`
	Sample           SampleSection `yaml:"sample"`
}

// CalibrationSection, λ kalibrasyonu ayarları (ADR-02).
type CalibrationSection struct {
	SplitRatio     float64 `yaml:"split_ratio"` // olay bazlı C/V ayrımı
	LambdaMin      float64 `yaml:"lambda_min"`
	LambdaMax      float64 `yaml:"lambda_max"`
	TargetCoverage float64 `yaml:"target_coverage"`
	Tolerance      float64 `yaml:"tolerance"`
	MaxIterations  int     `yaml:"max_iterations"`
}

// IntegritySection, manipülasyon enjeksiyonu ve tespit ayarları (ADR-09).
type IntegritySection struct {
	MaxVelocityKMH float64         `yaml:"max_velocity_kmh"`
	InjectionRate  float64         `yaml:"injection_rate"`
	RuleWeights    map[int]float64 `yaml:"rule_weights"`
}

// PrivacySection, mahremiyet ayarları (ADR-15).
type PrivacySection struct {
	KAnonymity  int    `yaml:"k_anonymity"`
	HMACSaltEnv string `yaml:"hmac_salt_env"` // .env'den okunur; config'de değer tutulmaz
}

// APISection, S5 gateway sınırları (ADR-13).
type APISection struct {
	MaxGeometriesPerRequest int `yaml:"max_geometries_per_request"`
}

// Scenario, tam senaryo yapılandırmasıdır.
//
// Profile alanı YAML'dan okunmaz; Load sırasında morphology anahtarından
// türetilir ve override'lar uygulanır (ADR-17).
type Scenario struct {
	Run           RunSection           `yaml:"run"`
	Morphology    Morphology           `yaml:"morphology"`
	Area          AreaSection          `yaml:"area"`
	Simulation    SimulationSection    `yaml:"simulation"`
	Network       NetworkSection       `yaml:"network"`
	Radio         RadioSection         `yaml:"radio"`
	TimingAdvance TimingAdvanceSection `yaml:"timing_advance"`
	Analysis      AnalysisSection      `yaml:"analysis"`
	Calibration   CalibrationSection   `yaml:"calibration"`
	Integrity     IntegritySection     `yaml:"integrity"`
	Privacy       PrivacySection       `yaml:"privacy"`
	API           APISection           `yaml:"api"`

	// Profile, çözümlenmiş morfoloji profilidir (ADR-17 factory çıktısı).
	Profile MorphologyProfile `yaml:"-"`
}

// ─── Yükleme ──────────────────────────────────────────────────────────────────

// Load, senaryo YAML dosyasını okur, profili çözümler ve doğrular.
// Doğrulamadan geçmeyen config ile koşu başlatılmaz (fail-fast, ADR-08).
func Load(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("senaryo config okunamadı (%s): %w", path, err)
	}
	return Parse(data)
}

// Parse, YAML içeriğini çözümler. Load'un dosyasız karşılığıdır (test edilebilirlik).
func Parse(data []byte) (*Scenario, error) {
	var s Scenario

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // bilinmeyen/yanlış yazılmış alan → hata
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("senaryo YAML çözümlenemedi: %w", err)
	}

	if err := s.resolveProfile(); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// resolveProfile, ADR-17 factory'sini çalıştırır ve YAML override'larını uygular.
func (s *Scenario) resolveProfile() error {
	p, err := ProfileFor(s.Morphology)
	if err != nil {
		return err
	}

	// Zorunlu YAML alanları — her senaryo dosyasında bulunur, profili ezer.
	if s.Area.RadiusKM > 0 {
		p.AreaRadiusKM = s.Area.RadiusKM
	}
	if s.Radio.DMaxKM > 0 {
		p.DMaxKM = s.Radio.DMaxKM
	}

	// Opsiyonel override'lar.
	if v := s.Network.InterSiteDistanceM; v != nil {
		p.InterSiteDistanceM = *v
	}
	if v := s.Network.AntHeightM; v != nil {
		p.AntHeightM = *v
	}
	if v := s.Network.PropagationModel; v != nil {
		p.PropagationModel = PropagationModel(*v)
	}
	if len(s.Network.FreqBands) > 0 {
		p.FreqDistribution = s.Network.FreqBands
	}
	if v := s.Network.BeamWidthDeg; v != nil {
		p.BeamWidthDeg = *v
	}
	if v := s.Network.TiltDeg; v != nil {
		p.TiltDeg = *v
	}
	if v := s.Network.EIRPdBm; v != nil {
		p.EIRPdBm = *v
	}
	if v := s.Network.RMaxM; v != nil {
		p.RMaxM = *v
	}

	s.Profile = p
	return nil
}

// ─── Doğrulama ────────────────────────────────────────────────────────────────

// validScenarios, run_config.scenario CHECK kısıtının koddaki karşılığı.
var validScenarios = map[string]bool{"A": true, "B": true, "C": true, "D": true}

// validTATechnologies, desteklenen TA teknolojileri.
var validTATechnologies = map[string]bool{"LTE": true, "GSM": true}

// Validate, senaryonun ve çözümlenmiş profilin tutarlılığını denetler.
func (s *Scenario) Validate() error {
	if !validScenarios[s.Run.Scenario] {
		return fmt.Errorf("run.scenario A/B/C/D olmalı (bulunan: %q)", s.Run.Scenario)
	}
	if s.Run.Name == "" {
		return fmt.Errorf("run.name boş olamaz")
	}

	if err := s.Profile.Validate(); err != nil {
		return err
	}

	// Alan (ADR-08)
	if s.Area.OriginLat < -90 || s.Area.OriginLat > 90 {
		return fmt.Errorf("area.origin_lat [-90,90] aralığında olmalı (%g)", s.Area.OriginLat)
	}
	if s.Area.OriginLon < -180 || s.Area.OriginLon > 180 {
		return fmt.Errorf("area.origin_lon [-180,180] aralığında olmalı (%g)", s.Area.OriginLon)
	}

	// Simülasyon
	if s.Simulation.Agents <= 0 {
		return fmt.Errorf("simulation.agents pozitif olmalı (%d)", s.Simulation.Agents)
	}
	if s.Simulation.DurationDays <= 0 {
		return fmt.Errorf("simulation.duration_days pozitif olmalı (%d)", s.Simulation.DurationDays)
	}
	if s.Simulation.TickMinutes <= 0 {
		return fmt.Errorf("simulation.tick_minutes pozitif olmalı (%d)", s.Simulation.TickMinutes)
	}

	// Şebeke
	if s.Network.SitesMin <= 0 {
		return fmt.Errorf("network.sites_min pozitif olmalı (%d)", s.Network.SitesMin)
	}
	if s.Network.SitesMax < s.Network.SitesMin {
		return fmt.Errorf("network.sites_max ≥ sites_min olmalı (%d < %d)",
			s.Network.SitesMax, s.Network.SitesMin)
	}
	if s.Network.RxSensitivityDBm >= 0 {
		return fmt.Errorf("network.rx_sensitivity_dbm negatif olmalı (%g)", s.Network.RxSensitivityDBm)
	}

	// Radyo
	if s.Radio.ShadowingSigmaDB <= 0 {
		return fmt.Errorf("radio.shadowing_sigma_db pozitif olmalı (%g)", s.Radio.ShadowingSigmaDB)
	}

	// TA
	if s.TimingAdvance.Enabled && !validTATechnologies[s.TimingAdvance.Technology] {
		return fmt.Errorf("timing_advance.technology LTE veya GSM olmalı (bulunan: %q)",
			s.TimingAdvance.Technology)
	}

	// Analiz (ADR-07, ADR-03)
	if s.Analysis.GridResolutionM <= 0 {
		return fmt.Errorf("analysis.grid_resolution_m pozitif olmalı (%g)", s.Analysis.GridResolutionM)
	}
	if len(s.Analysis.ContourLevels) == 0 {
		return fmt.Errorf("analysis.contour_levels boş olamaz")
	}
	for _, c := range s.Analysis.ContourLevels {
		if c <= 0 || c >= 1 {
			return fmt.Errorf("analysis.contour_levels (0,1) aralığında olmalı (%g)", c)
		}
	}
	if s.Analysis.NeighborMaxCount <= 0 {
		return fmt.Errorf("analysis.neighbor_max_count pozitif olmalı (%d)", s.Analysis.NeighborMaxCount)
	}
	if s.Analysis.Sample.CalibrationEvents <= 0 {
		return fmt.Errorf("analysis.sample.calibration_events pozitif olmalı (%d)",
			s.Analysis.Sample.CalibrationEvents)
	}
	if s.Analysis.Sample.ValidationEvents < 0 {
		return fmt.Errorf("analysis.sample.validation_events negatif olamaz (%d)",
			s.Analysis.Sample.ValidationEvents)
	}

	// Kalibrasyon (ADR-02)
	if s.Calibration.SplitRatio <= 0 || s.Calibration.SplitRatio >= 1 {
		return fmt.Errorf("calibration.split_ratio (0,1) aralığında olmalı (%g)", s.Calibration.SplitRatio)
	}
	if s.Calibration.LambdaMin <= 0 || s.Calibration.LambdaMax <= s.Calibration.LambdaMin {
		return fmt.Errorf("calibration: 0 < lambda_min < lambda_max olmalı (%g, %g)",
			s.Calibration.LambdaMin, s.Calibration.LambdaMax)
	}
	if s.Calibration.TargetCoverage <= 0 || s.Calibration.TargetCoverage >= 1 {
		return fmt.Errorf("calibration.target_coverage (0,1) aralığında olmalı (%g)",
			s.Calibration.TargetCoverage)
	}
	if s.Calibration.Tolerance <= 0 {
		return fmt.Errorf("calibration.tolerance pozitif olmalı (%g)", s.Calibration.Tolerance)
	}
	if s.Calibration.MaxIterations <= 0 {
		return fmt.Errorf("calibration.max_iterations pozitif olmalı (%d)", s.Calibration.MaxIterations)
	}

	// Bütünlük (ADR-09)
	if s.Integrity.MaxVelocityKMH <= 0 {
		return fmt.Errorf("integrity.max_velocity_kmh pozitif olmalı (%g)", s.Integrity.MaxVelocityKMH)
	}
	if s.Integrity.InjectionRate < 0 || s.Integrity.InjectionRate >= 1 {
		return fmt.Errorf("integrity.injection_rate [0,1) aralığında olmalı (%g)", s.Integrity.InjectionRate)
	}
	if err := validateRuleWeights(s.Integrity.RuleWeights); err != nil {
		return err
	}

	// Mahremiyet (ADR-15)
	if s.Privacy.KAnonymity < 1 {
		return fmt.Errorf("privacy.k_anonymity ≥ 1 olmalı (%d)", s.Privacy.KAnonymity)
	}
	if s.Privacy.HMACSaltEnv == "" {
		return fmt.Errorf("privacy.hmac_salt_env boş olamaz")
	}

	// API (ADR-13)
	if s.API.MaxGeometriesPerRequest <= 0 {
		return fmt.Errorf("api.max_geometries_per_request pozitif olmalı (%d)",
			s.API.MaxGeometriesPerRequest)
	}

	return nil
}

// validateRuleWeights, 5 bütünlük kuralının ağırlıklarını denetler (ADR-09).
func validateRuleWeights(w map[int]float64) error {
	if len(w) != 5 {
		return fmt.Errorf("integrity.rule_weights 5 kural içermeli (bulunan: %d)", len(w))
	}
	var sum float64
	for id, weight := range w {
		// integrity_findings.rule_id CHECK kısıtı: 1..5
		if id < 1 || id > 5 {
			return fmt.Errorf("integrity.rule_weights: kural id 1..5 olmalı (bulunan: %d)", id)
		}
		if weight < 0 {
			return fmt.Errorf("integrity.rule_weights[%d]: ağırlık negatif olamaz (%g)", id, weight)
		}
		sum += weight
	}
	if sum <= 0 {
		return fmt.Errorf("integrity.rule_weights toplamı pozitif olmalı (%g)", sum)
	}
	return nil
}
