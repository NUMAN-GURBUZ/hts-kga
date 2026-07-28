// T-E02-10 — 3GPP TR 38.901 UMi (Urban Microcell, Street Canyon) yol kaybı modeli.
//
// Kaynak: TR 38.901 v17.0.0 — Tablo 7.4.1-1 (yol kaybı), Tablo 7.4.2-1 (LOS olasılığı).
//
// Kentsel mikro hücre: anten çatı seviyesinin altında (h_BS = 10 m nominal),
// kapsama birkaç yüz metre. Projede varsayılan olarak seçilmez; kentsel profil
// UMa kullanır (ADR-17). Senaryo YAML'ında `network.propagation_model: "UMi"`
// ile açıkça istenebilir ve yoğun kentsel katman çalışmalarına açıktır.
package rf

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// UMi yol kaybı katsayıları — TR 38.901 Tablo 7.4.1-1.
const (
	// LOS, kırılma mesafesinin altı:  PL = 32.4 + 21·log10(d_3D) + 20·log10(f_c)
	umiLOSInterceptDB = 32.4
	umiLOSSlopeNear   = 21.0

	// LOS, kırılma mesafesinin üstü:  ... + 40·log10(d_3D) − 9.5·log10(d'_BP² + Δh²)
	umiLOSSlopeFar       = 40.0
	umiLOSBreakpointCoef = 9.5

	// NLOS taban ifadesi:
	//   PL' = 35.3·log10(d_3D) + 22.4 + 21.3·log10(f_c) − 0.3·(h_UT − 1.5)
	umiNLOSSlope       = 35.3
	umiNLOSInterceptDB = 22.4
	umiNLOSFreqCoef    = 21.3
	umiNLOSHUTCoef     = 0.3
	umiNLOSRefHUTm     = 1.5
)

// UMi LOS olasılığı katsayıları — TR 38.901 Tablo 7.4.2-1.
// UMa ile aynı biçim, farklı sönüm ölçeği (sokak kanyonu daha hızlı kapanır).
const (
	umiLOSProbCertainM = 18.0
	umiLOSProbDecayM   = 36.0
)

// UMi gölgeleme standart sapmaları — TR 38.901 Tablo 7.4.1-1 (σ_SF).
const (
	umiShadowingLOSdB  = 4.0
	umiShadowingNLOSdB = 7.82
)

// UMi gölgeleme dekorelasyon mesafeleri — TR 38.901 Tablo 7.5-6.
// Sokak kanyonunda ölçek en kısadır: bir köşeyi dönmek gölgeleme
// koşullarını tamamen değiştirir.
const (
	umiDecorrelationLOSm  = 10.0
	umiDecorrelationNLOSm = 13.0
)

// UMi uygulanabilirlik aralığı — TR 38.901 Tablo 7.4.1-1.
const (
	umiMaxD2DM    = 5000.0
	umiMinFreqMHz = 500
	umiMaxFreqMHz = 100_000
	umiMinHBSm    = 10.0
	umiMaxHBSm    = 10.0
	umiMinHUTm    = 1.5
	umiMaxHUTm    = 22.5
)

// UMi, kentsel mikro hücre (sokak kanyonu) yayılım modelidir (TR 38.901).
// Durumsuzdur; eşzamanlı çağrılabilir.
type UMi struct{}

// Name, envanterdeki model etiketini döndürür.
func (UMi) Name() config.PropagationModel { return config.ModelUMi }

// Range, TR 38.901'de tanımlı uygulanabilirlik aralığını döndürür.
func (UMi) Range() ValidityRange {
	return ValidityRange{
		MinD2DM: minLinkDistanceM, MaxD2DM: umiMaxD2DM,
		MinFreqMHz: umiMinFreqMHz, MaxFreqMHz: umiMaxFreqMHz,
		MinHBSm: umiMinHBSm, MaxHBSm: umiMaxHBSm,
		MinHUTm: umiMinHUTm, MaxHUTm: umiMaxHUTm,
	}
}

// ShadowingSigmaDB, standardın öngördüğü σ_SF değerini döndürür (dB).
// UMi NLOS'ta σ belirgin biçimde yüksektir (7,82 dB): sokak kanyonunda
// engellenme çok değişkendir.
func (UMi) ShadowingSigmaDB(los bool) float64 {
	if los {
		return umiShadowingLOSdB
	}
	return umiShadowingNLOSdB
}

// DecorrelationM, gölgelemenin dekorelasyon mesafesini döndürür (metre).
func (UMi) DecorrelationM(los bool) float64 {
	if los {
		return umiDecorrelationLOSm
	}
	return umiDecorrelationNLOSm
}

// PathLossDB, UMi yol kaybını döndürür (dB).
// NLOS'ta LOS ile büyüğü alınır (TR 38.901 max() sözleşmesi).
func (m UMi) PathLossDB(l Link) float64 {
	d2D := clampDistance(l.D2DM)
	d3D := Link{D2DM: d2D, HBSm: l.HBSm, HUTm: l.HUTm}.D3DM()
	fGHz := l.FreqGHz()

	los := m.losPathLossDB(d2D, d3D, l.HBSm, l.HUTm, fGHz, l.FreqHz())
	if l.LOS {
		return los
	}
	return math.Max(los, m.nlosBasePathLossDB(d3D, fGHz, l.HUTm))
}

// losPathLossDB, iki parçalı LOS yol kaybıdır.
//
// −9,5·log10(d'_BP² + Δh²) düzeltmesi, 40 − 2·9,5 = 21 → kırılma noktasında
// yakın alan üsteliyle (21) tam örtüşme sağlar; eğri süreklidir.
func (UMi) losPathLossDB(d2D, d3D, hBSm, hUTm, fGHz, fHz float64) float64 {
	freqTerm := freqTermCoefDB * log10(fGHz)
	dBP := breakpointDistanceUrbanM(hBSm, hUTm, fHz)

	if d2D <= dBP {
		return umiLOSInterceptDB + umiLOSSlopeNear*log10(d3D) + freqTerm
	}

	dh := hBSm - hUTm
	return umiLOSInterceptDB +
		umiLOSSlopeFar*log10(d3D) +
		freqTerm -
		umiLOSBreakpointCoef*log10(dBP*dBP+dh*dh)
}

// nlosBasePathLossDB, NLOS taban ifadesidir.
//
// UMa'dan farklı olarak frekans terimi 21,3 katsayılıdır (20 değil): mikro
// hücrede frekansa bağımlılık biraz daha güçlü ölçülmüştür.
func (UMi) nlosBasePathLossDB(d3D, fGHz, hUTm float64) float64 {
	return umiNLOSSlope*log10(d3D) +
		umiNLOSInterceptDB +
		umiNLOSFreqCoef*log10(fGHz) -
		umiNLOSHUTCoef*(hUTm-umiNLOSRefHUTm)
}

// LOSProbability, UMi görüş hattı olasılığını döndürür — TR 38.901 Tablo 7.4.2-1:
//
//	d_2D ≤ 18 m : 1
//	aksi        : 18/d + e^(−d/36)·(1 − 18/d)
//
// UMa'daki yüksek UT düzeltmesi UMi'de yoktur.
func (UMi) LOSProbability(d2DM, _ float64) float64 {
	d := clampDistance(d2DM)
	if d <= umiLOSProbCertainM {
		return 1
	}
	ratio := umiLOSProbCertainM / d
	return clampProbability(ratio + math.Exp(-d/umiLOSProbDecayM)*(1-ratio))
}
