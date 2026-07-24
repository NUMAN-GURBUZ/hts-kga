// Package config, senaryo YAML şemasını ve morfoloji profillerini sağlar.
//
// T-E02-01 — ADR-17 (O-01): `morphology` alanı tek başına bir parametre değil,
// bir **profil anahtarıdır**. Bu dosyadaki MorphologyProfile, senaryo farklarının
// koda bağlandığı tek noktadır: YAML'da yalnızca `morphology: rural` yazmak,
// aşağıdaki alanların tamamını tutarlı biçimde değiştirir.
//
// Profil değerleri YAML'dan geçersiz kılınabilir (override); varsayılan tutarlıdır.
package config

import (
	"fmt"
	"math"
)

// ─── Numaralandırmalar ────────────────────────────────────────────────────────

// Morphology, senaryo morfoloji anahtarıdır (ADR-17).
// Veritabanı kısıtı: cells.morphology ∈ {'urban','rural'} (001_schema.sql).
type Morphology string

const (
	MorphologyUrban Morphology = "urban"
	MorphologyRural Morphology = "rural"
)

// Valid, morfoloji değerinin tanımlı olup olmadığını bildirir.
func (m Morphology) Valid() bool {
	return m == MorphologyUrban || m == MorphologyRural
}

// PropagationModel, 3GPP TR 38.901 yayılım modeli seçimidir.
// Veritabanı kısıtı: cells.model_type ∈ {'UMa','UMi','RMa'} (001_schema.sql).
//
// Sprint 1'de yalnızca envanter etiketi olarak kullanılır; formüller T-E02-10.
type PropagationModel string

const (
	ModelUMa PropagationModel = "UMa" // Urban Macrocell
	ModelUMi PropagationModel = "UMi" // Urban Microcell
	ModelRMa PropagationModel = "RMa" // Rural Macrocell
)

// Valid, model değerinin tanımlı olup olmadığını bildirir.
func (p PropagationModel) Valid() bool {
	return p == ModelUMa || p == ModelUMi || p == ModelRMa
}

// ─── Frekans dağılımı ─────────────────────────────────────────────────────────

// FreqBand, bir taşıyıcı frekans bandı ve o bandın seçilme ağırlığıdır.
// Ağırlıklar profil içinde toplam 1.0 olmak zorundadır (ValidateFreqBands).
type FreqBand struct {
	MHz    int     `yaml:"mhz"`
	Weight float64 `yaml:"weight"`
}

// freqWeightTolerance, ağırlık toplamı için kabul edilen kayan nokta toleransı.
const freqWeightTolerance = 1e-9

// isPositive, değerin pozitif ve sonlu olduğunu bildirir.
// NaN ve ±Inf reddedilir: `v <= 0` karşılaştırması NaN'ı sessizce geçirir.
func isPositive(v float64) bool {
	return v > 0 && !math.IsInf(v, 1)
}

// ValidateFreqBands, band listesinin geçerliliğini doğrular:
// boş olmamalı, frekanslar pozitif, ağırlıklar pozitif ve toplamı 1.0.
func ValidateFreqBands(bands []FreqBand) error {
	if len(bands) == 0 {
		return fmt.Errorf("frekans dağılımı boş")
	}
	var sum float64
	for i, b := range bands {
		if b.MHz <= 0 {
			return fmt.Errorf("band[%d]: frekans pozitif olmalı (mhz=%d)", i, b.MHz)
		}
		if b.Weight <= 0 {
			return fmt.Errorf("band[%d] (%d MHz): ağırlık pozitif olmalı (weight=%g)", i, b.MHz, b.Weight)
		}
		sum += b.Weight
	}
	if math.Abs(sum-1.0) > freqWeightTolerance {
		return fmt.Errorf("frekans ağırlıkları toplamı 1.0 olmalı (bulunan: %g)", sum)
	}
	return nil
}

// ─── Profil ───────────────────────────────────────────────────────────────────

// MorphologyProfile, ADR-17'de tanımlı senaryo profilidir.
//
// İlk sekiz alan ADR-17 tablosunun birebir karşılığıdır. Sonrasındaki
// sektör/kapsama alanları Sprint 1'de (T-E02-04, T-E02-05) eklenmiştir:
// planda sayısal karşılıkları verilmediğinden morfolojiye bağlı varsayılan
// olarak burada tutulur ve YAML'dan ezilebilir.
type MorphologyProfile struct {
	Morphology Morphology

	// ── ADR-17 tablosu ──
	AreaRadiusKM       float64          // kapsama alanı yarıçapı (ADR-08)
	InterSiteDistanceM float64          // nominal site arası mesafe
	PropagationModel   PropagationModel // 3GPP model seçimi (T-E02-10)
	AntHeightM         float64          // anten yüksekliği
	FreqDistribution   []FreqBand       // taşıyıcı band dağılımı
	AgentHomeWorkMinKM float64          // ev–iş mesafesi alt sınırı (T-E02-07)
	AgentHomeWorkMaxKM float64          // ev–iş mesafesi üst sınırı (T-E02-07)
	CommuteSpeedKMH    float64          // yolculuk hızı (T-E02-09)
	DMaxKM             float64          // yayılım modeli geçerlilik sınırı

	// ── Sektör varsayılanları (T-E02-04) ──
	BeamWidthDeg float64 // yatay 3 dB hüzme genişliği
	TiltDeg      float64 // aşağı eğim (downtilt), pozitif = aşağı
	EIRPdBm      float64 // sektör başına yayılan izotropik güç

	// ── Kapsama (T-E02-05) ──
	// RMaxM, link budget önhesabının Sprint 1 karşılığıdır. RF modeli (T-E02-10)
	// yazılana kadar profilden/config'ten sabit okunur.
	RMaxM float64
}

// urbanProfile, kentsel morfoloji varsayılanları (ADR-17).
//
// Frekans dağılımı "yüksek bantlar ağırlıklı": kapasite odaklı kentsel şebeke.
func urbanProfile() MorphologyProfile {
	return MorphologyProfile{
		Morphology:         MorphologyUrban,
		AreaRadiusKM:       5,
		InterSiteDistanceM: 500,
		PropagationModel:   ModelUMa,
		AntHeightM:         25,
		FreqDistribution: []FreqBand{
			{MHz: 1800, Weight: 0.30},
			{MHz: 2100, Weight: 0.40},
			{MHz: 2600, Weight: 0.30},
		},
		AgentHomeWorkMinKM: 1,
		AgentHomeWorkMaxKM: 8,
		CommuteSpeedKMH:    30,
		DMaxKM:             5,
		BeamWidthDeg:       65,
		TiltDeg:            6,
		EIRPdBm:            58,
		RMaxM:              5000,
	}
}

// ruralProfile, kırsal morfoloji varsayılanları (ADR-17).
//
// Frekans dağılımı "düşük bantlar ağırlıklı": kapsama odaklı kırsal şebeke.
func ruralProfile() MorphologyProfile {
	return MorphologyProfile{
		Morphology:         MorphologyRural,
		AreaRadiusKM:       20,
		InterSiteDistanceM: 2000,
		PropagationModel:   ModelRMa,
		AntHeightM:         45,
		FreqDistribution: []FreqBand{
			{MHz: 800, Weight: 0.50},
			{MHz: 900, Weight: 0.35},
			{MHz: 1800, Weight: 0.15},
		},
		AgentHomeWorkMinKM: 5,
		AgentHomeWorkMaxKM: 25,
		CommuteSpeedKMH:    70,
		DMaxKM:             10,
		BeamWidthDeg:       65,
		TiltDeg:            3,
		EIRPdBm:            62,
		RMaxM:              20000,
	}
}

// ProfileFor, morfoloji anahtarına karşılık gelen varsayılan profili döndürür.
// Dönen profil bir kopyadır; çağıran üzerinde değişiklik yapabilir (override).
func ProfileFor(m Morphology) (MorphologyProfile, error) {
	switch m {
	case MorphologyUrban:
		return urbanProfile(), nil
	case MorphologyRural:
		return ruralProfile(), nil
	default:
		return MorphologyProfile{}, fmt.Errorf("bilinmeyen morfoloji %q (geçerli: urban, rural)", m)
	}
}

// AreaRadiusM, kapsama yarıçapını metre cinsinden döndürür (ENU hesapları için).
func (p MorphologyProfile) AreaRadiusM() float64 {
	return p.AreaRadiusKM * 1000
}

// Validate, profilin fiziksel olarak tutarlı olup olmadığını denetler.
// Factory çıktısı ve YAML override sonrası aynı kural kümesiyle doğrulanır.
func (p MorphologyProfile) Validate() error {
	if !p.Morphology.Valid() {
		return fmt.Errorf("profil: geçersiz morfoloji %q", p.Morphology)
	}
	if !p.PropagationModel.Valid() {
		return fmt.Errorf("profil: geçersiz yayılım modeli %q", p.PropagationModel)
	}
	if !isPositive(p.AreaRadiusKM) {
		return fmt.Errorf("profil: area_radius_km pozitif olmalı (%g)", p.AreaRadiusKM)
	}
	if !isPositive(p.InterSiteDistanceM) {
		return fmt.Errorf("profil: inter_site_distance_m pozitif olmalı (%g)", p.InterSiteDistanceM)
	}
	if !isPositive(p.AntHeightM) {
		return fmt.Errorf("profil: ant_height_m pozitif olmalı (%g)", p.AntHeightM)
	}
	if err := ValidateFreqBands(p.FreqDistribution); err != nil {
		return fmt.Errorf("profil: %w", err)
	}
	if !isPositive(p.AgentHomeWorkMinKM) || !(p.AgentHomeWorkMaxKM > p.AgentHomeWorkMinKM) {
		return fmt.Errorf("profil: ev–iş mesafe aralığı geçersiz (%g–%g km)",
			p.AgentHomeWorkMinKM, p.AgentHomeWorkMaxKM)
	}
	if !isPositive(p.CommuteSpeedKMH) {
		return fmt.Errorf("profil: commute_speed_kmh pozitif olmalı (%g)", p.CommuteSpeedKMH)
	}
	if !isPositive(p.DMaxKM) {
		return fmt.Errorf("profil: d_max_km pozitif olmalı (%g)", p.DMaxKM)
	}
	// cells.beam_width kısıtı: 0 < beam_width <= 360 (001_schema.sql)
	if !isPositive(p.BeamWidthDeg) || p.BeamWidthDeg > 360 {
		return fmt.Errorf("profil: beam_width_deg (0,360] aralığında olmalı (%g)", p.BeamWidthDeg)
	}
	if !(p.TiltDeg >= 0 && p.TiltDeg < 90) {
		return fmt.Errorf("profil: tilt_deg [0,90) aralığında olmalı (%g)", p.TiltDeg)
	}
	// cells.r_max_m kısıtı: r_max_m > 0 (001_schema.sql)
	if !isPositive(p.RMaxM) {
		return fmt.Errorf("profil: r_max_m pozitif olmalı (%g)", p.RMaxM)
	}
	return nil
}
