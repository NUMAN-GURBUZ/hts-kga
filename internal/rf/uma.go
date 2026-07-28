// T-E02-10 — 3GPP TR 38.901 UMa (Urban Macrocell) yol kaybı modeli.
//
// Kaynak: TR 38.901 v17.0.0
//   - Yol kaybı  : Tablo 7.4.1-1, "UMa" satırları
//   - LOS olasılığı: Tablo 7.4.2-1
//
// Kentsel makro hücre: anten çatı seviyesinin üzerinde (h_BS = 25 m nominal),
// kapsama birkaç kilometre. Projede kentsel profilin varsayılan modelidir
// (ADR-17: urban → UMa).
package rf

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// UMa yol kaybı katsayıları — TR 38.901 Tablo 7.4.1-1.
const (
	// LOS, kırılma mesafesinin altı:  PL = 28.0 + 22·log10(d_3D) + 20·log10(f_c)
	umaLOSInterceptDB = 28.0
	umaLOSSlopeNear   = 22.0

	// LOS, kırılma mesafesinin üstü:  ... + 40·log10(d_3D) − 9·log10(d'_BP² + Δh²)
	umaLOSSlopeFar       = 40.0
	umaLOSBreakpointCoef = 9.0

	// NLOS taban ifadesi:
	//   PL' = 13.54 + 39.08·log10(d_3D) + 20·log10(f_c) − 0.6·(h_UT − 1.5)
	umaNLOSInterceptDB = 13.54
	umaNLOSSlope       = 39.08
	umaNLOSHUTCoef     = 0.6
	umaNLOSRefHUTm     = 1.5
)

// UMa LOS olasılığı katsayıları — TR 38.901 Tablo 7.4.2-1.
const (
	// Bu mesafenin altında görüş hattı kesin kabul edilir.
	umaLOSProbCertainM = 18.0
	// Üstel sönüm ölçeği.
	umaLOSProbDecayM = 63.0

	// Yüksek UT düzeltmesi C'(h_UT) yalnızca h_UT > 13 m iken devreye girer.
	umaLOSProbHUTThresholdM = 13.0
	umaLOSProbHUTScaleM     = 10.0
	umaLOSProbHUTExponent   = 1.5
	umaLOSProbCorrCoef      = 1.25 // 5/4
	umaLOSProbCorrDistM     = 100.0
	umaLOSProbCorrDecayM    = 150.0
)

// UMa gölgeleme standart sapmaları — TR 38.901 Tablo 7.4.1-1 (σ_SF).
const (
	umaShadowingLOSdB  = 4.0
	umaShadowingNLOSdB = 6.0
)

// UMa gölgeleme dekorelasyon mesafeleri — TR 38.901 Tablo 7.5-6.
// Makro hücrede engeller (bina blokları) mikro hücreye göre daha büyük
// ölçeklidir; dekorelasyon mesafesi de buna paralel olarak daha uzundur.
const (
	umaDecorrelationLOSm  = 37.0
	umaDecorrelationNLOSm = 50.0
)

// UMa uygulanabilirlik aralığı — TR 38.901 Tablo 7.4.1-1.
const (
	umaMaxD2DM    = 5000.0
	umaMinFreqMHz = 500
	umaMaxFreqMHz = 100_000
	umaMinHBSm    = 25.0
	umaMaxHBSm    = 25.0
	umaMinHUTm    = 1.5
	umaMaxHUTm    = 22.5
)

// UMa, kentsel makro hücre yayılım modelidir (TR 38.901).
// Durumsuzdur; değer tipi olarak kullanılır ve eşzamanlı çağrılabilir.
type UMa struct{}

// Name, envanterdeki model etiketini döndürür.
func (UMa) Name() config.PropagationModel { return config.ModelUMa }

// Range, TR 38.901'de tanımlı uygulanabilirlik aralığını döndürür.
//
// h_BS aralığı tek noktadır (25 m): UMa standartta yalnızca bu yükseklik için
// kalibre edilmiştir. Kentsel profil zaten 25 m kullanır (ADR-17).
func (UMa) Range() ValidityRange {
	return ValidityRange{
		MinD2DM: minLinkDistanceM, MaxD2DM: umaMaxD2DM,
		MinFreqMHz: umaMinFreqMHz, MaxFreqMHz: umaMaxFreqMHz,
		MinHBSm: umaMinHBSm, MaxHBSm: umaMaxHBSm,
		MinHUTm: umaMinHUTm, MaxHUTm: umaMaxHUTm,
	}
}

// ShadowingSigmaDB, standardın öngördüğü σ_SF değerini döndürür (dB).
// Meta veridir; simülatörün kullandığı σ config'ten gelir (T-E02-11).
func (UMa) ShadowingSigmaDB(los bool) float64 {
	if los {
		return umaShadowingLOSdB
	}
	return umaShadowingNLOSdB
}

// DecorrelationM, gölgelemenin dekorelasyon mesafesini döndürür (metre).
func (UMa) DecorrelationM(los bool) float64 {
	if los {
		return umaDecorrelationLOSm
	}
	return umaDecorrelationNLOSm
}

// PathLossDB, UMa yol kaybını döndürür (dB).
//
// NLOS durumunda TR 38.901, NLOS taban ifadesi ile LOS değerinin **büyüğünü**
// alır: engellenmiş bir yol, serbest görüş hattından daha az kayıplı olamaz.
func (m UMa) PathLossDB(l Link) float64 {
	d2D := clampDistance(l.D2DM)
	d3D := Link{D2DM: d2D, HBSm: l.HBSm, HUTm: l.HUTm}.D3DM()
	fGHz := l.FreqGHz()

	los := m.losPathLossDB(d2D, d3D, l.HBSm, l.HUTm, fGHz, l.FreqHz())
	if l.LOS {
		return los
	}
	return math.Max(los, m.nlosBasePathLossDB(d3D, fGHz, l.HUTm))
}

// losPathLossDB, LOS yol kaybını iki parçalı (dual-slope) modelle hesaplar.
//
// Kırılma mesafesi d'_BP'nin altında üstel 2,2; üstünde 4,0'dır. Düzeltme
// terimi −9·log10(d'_BP² + Δh²), iki parçanın kırılma noktasında **sürekli**
// olmasını sağlar (bkz. TestUMa_BreakpointContinuity).
func (UMa) losPathLossDB(d2D, d3D, hBSm, hUTm, fGHz, fHz float64) float64 {
	freqTerm := freqTermCoefDB * log10(fGHz)
	dBP := breakpointDistanceUrbanM(hBSm, hUTm, fHz)

	if d2D <= dBP {
		return umaLOSInterceptDB + umaLOSSlopeNear*log10(d3D) + freqTerm
	}

	dh := hBSm - hUTm
	return umaLOSInterceptDB +
		umaLOSSlopeFar*log10(d3D) +
		freqTerm -
		umaLOSBreakpointCoef*log10(dBP*dBP+dh*dh)
}

// nlosBasePathLossDB, NLOS taban ifadesidir (henüz LOS ile karşılaştırılmamış).
func (UMa) nlosBasePathLossDB(d3D, fGHz, hUTm float64) float64 {
	return umaNLOSInterceptDB +
		umaNLOSSlope*log10(d3D) +
		freqTermCoefDB*log10(fGHz) -
		umaNLOSHUTCoef*(hUTm-umaNLOSRefHUTm)
}

// LOSProbability, UMa görüş hattı olasılığını döndürür — TR 38.901 Tablo 7.4.2-1:
//
//	d_2D ≤ 18 m : 1
//	aksi        : [18/d + e^(−d/63)·(1 − 18/d)] · [1 + C'(h_UT)·(5/4)·(d/100)³·e^(−d/150)]
//
// C'(h_UT) = 0 (h_UT ≤ 13 m) — projede h_UT = 1,5 m olduğundan ikinci çarpan
// daima 1'dir; yüksek UT dalı ileride kullanılabilsin diye korunmuştur.
func (UMa) LOSProbability(d2DM, hUTm float64) float64 {
	d := clampDistance(d2DM)
	if d <= umaLOSProbCertainM {
		return 1
	}

	ratio := umaLOSProbCertainM / d
	base := ratio + math.Exp(-d/umaLOSProbDecayM)*(1-ratio)

	c := umaHUTCorrection(hUTm)
	if c == 0 {
		return clampProbability(base)
	}

	correction := 1 + c*umaLOSProbCorrCoef*
		math.Pow(d/umaLOSProbCorrDistM, 3)*
		math.Exp(-d/umaLOSProbCorrDecayM)

	return clampProbability(base * correction)
}

// umaHUTCorrection, C'(h_UT) düzeltme katsayısıdır (TR 38.901 Tablo 7.4.2-1):
//
//	h_UT ≤ 13 m       : 0
//	13 < h_UT ≤ 23 m  : ((h_UT − 13)/10)^1.5
func umaHUTCorrection(hUTm float64) float64 {
	if hUTm <= umaLOSProbHUTThresholdM {
		return 0
	}
	return math.Pow((hUTm-umaLOSProbHUTThresholdM)/umaLOSProbHUTScaleM, umaLOSProbHUTExponent)
}
