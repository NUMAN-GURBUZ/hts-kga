// T-E02-10 — 3GPP TR 38.901 RMa (Rural Macrocell) yol kaybı modeli.
//
// Kaynak: TR 38.901 v17.0.0 — Tablo 7.4.1-1 (yol kaybı), Tablo 7.4.2-1 (LOS olasılığı).
//
// Kırsal makro hücre: yüksek anten (h_BS 10–150 m), geniş kapsama, seyrek ve
// alçak yapılaşma. Projede kırsal profilin varsayılan modelidir
// (ADR-17: rural → RMa, h_BS = 45 m).
//
// UMa/UMi'den farkı: model, ortalama bina yüksekliği (h) ve sokak genişliği (W)
// gibi **çevresel** parametreler alır; bunlar senaryo config'inde tutulmaz,
// standardın varsayılan kırsal değerleri kullanılır.
package rf

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// RMa çevre parametreleri — TR 38.901 Tablo 7.4.1-1 kırsal varsayılanları.
const (
	// rmaAvgBuildingHeightM (h), ortalama bina yüksekliği.
	rmaAvgBuildingHeightM = 5.0
	// rmaAvgStreetWidthM (W), ortalama sokak genişliği.
	rmaAvgStreetWidthM = 20.0
)

// RMa LOS yol kaybı katsayıları — TR 38.901 Tablo 7.4.1-1.
//
//	PL1 = 20·log10(40π·d_3D·f_c/3)
//	      + min(0.03·h^1.72, 10)·log10(d_3D)
//	      − min(0.044·h^1.72, 14.77)
//	      + 0.002·log10(h)·d_3D
const (
	rmaFreeSpaceCoef    = 40.0 * math.Pi / 3.0 // 40π/3
	rmaBuildingExponent = 1.72
	rmaSlopeCoef        = 0.03
	rmaSlopeCap         = 10.0
	rmaInterceptCoef    = 0.044
	rmaInterceptCap     = 14.77
	rmaLinearCoef       = 0.002
	rmaFarSlope         = 40.0 // kırılma sonrası ek eğim
)

// RMa NLOS yol kaybı katsayıları — TR 38.901 Tablo 7.4.1-1.
//
//	PL' = 161.04 − 7.1·log10(W) + 7.5·log10(h)
//	      − (24.37 − 3.7·(h/h_BS)²)·log10(h_BS)
//	      + (43.42 − 3.1·log10(h_BS))·(log10(d_3D) − 3)
//	      + 20·log10(f_c)
//	      − (3.2·(log10(11.75·h_UT))² − 4.97)
const (
	rmaNLOSInterceptDB  = 161.04
	rmaNLOSStreetCoef   = 7.1
	rmaNLOSBuildingCoef = 7.5
	rmaNLOSHBSCoefA     = 24.37
	rmaNLOSHBSCoefB     = 3.7
	rmaNLOSDistCoefA    = 43.42
	rmaNLOSDistCoefB    = 3.1
	rmaNLOSDistOffset   = 3.0
	rmaNLOSHUTCoefA     = 3.2
	rmaNLOSHUTScale     = 11.75
	rmaNLOSHUTCoefB     = 4.97
)

// RMa LOS olasılığı katsayıları — TR 38.901 Tablo 7.4.2-1.
const (
	rmaLOSProbCertainM = 10.0
	rmaLOSProbDecayM   = 1000.0
)

// RMa gölgeleme standart sapmaları — TR 38.901 Tablo 7.4.1-1 (σ_SF).
// LOS'ta kırılma mesafesinin altı ve üstü farklıdır (4 / 6 dB); burada
// muhafazakâr üst değer raporlanır.
const (
	rmaShadowingLOSdB  = 6.0
	rmaShadowingNLOSdB = 8.0
)

// RMa gölgeleme dekorelasyon mesafeleri — TR 38.901 Tablo 7.5-6.
// NLOS'ta 120 m ile tüm modeller içinde en uzun ölçek: kırsalda engeller
// (tepeler, ağaçlıklar) seyrek ve büyüktür, gölgeleme yavaş değişir.
const (
	rmaDecorrelationLOSm  = 37.0
	rmaDecorrelationNLOSm = 120.0
)

// RMa uygulanabilirlik aralığı — TR 38.901 Tablo 7.4.1-1.
//
// NOT: MaxD2DM = 10 km'dir, ancak kırsal profilde r_max = 20 km'dir
// (Sprint 1 teknik borcu #3). 10–20 km aralığında model **dışdeğerleme**
// yapar; CheckValidity bunu görünür kılar.
const (
	rmaMaxD2DM    = 10_000.0
	rmaMinFreqMHz = 500
	rmaMaxFreqMHz = 30_000
	rmaMinHBSm    = 10.0
	rmaMaxHBSm    = 150.0
	rmaMinHUTm    = 1.0
	rmaMaxHUTm    = 10.0
)

// RMa, kırsal makro hücre yayılım modelidir (TR 38.901).
// Durumsuzdur; eşzamanlı çağrılabilir.
type RMa struct{}

// Name, envanterdeki model etiketini döndürür.
func (RMa) Name() config.PropagationModel { return config.ModelRMa }

// Range, TR 38.901'de tanımlı uygulanabilirlik aralığını döndürür.
func (RMa) Range() ValidityRange {
	return ValidityRange{
		MinD2DM: minLinkDistanceM, MaxD2DM: rmaMaxD2DM,
		MinFreqMHz: rmaMinFreqMHz, MaxFreqMHz: rmaMaxFreqMHz,
		MinHBSm: rmaMinHBSm, MaxHBSm: rmaMaxHBSm,
		MinHUTm: rmaMinHUTm, MaxHUTm: rmaMaxHUTm,
	}
}

// ShadowingSigmaDB, standardın öngördüğü σ_SF değerini döndürür (dB).
func (RMa) ShadowingSigmaDB(los bool) float64 {
	if los {
		return rmaShadowingLOSdB
	}
	return rmaShadowingNLOSdB
}

// DecorrelationM, gölgelemenin dekorelasyon mesafesini döndürür (metre).
func (RMa) DecorrelationM(los bool) float64 {
	if los {
		return rmaDecorrelationLOSm
	}
	return rmaDecorrelationNLOSm
}

// PathLossDB, RMa yol kaybını döndürür (dB).
// NLOS'ta LOS ile büyüğü alınır (TR 38.901 max() sözleşmesi).
func (m RMa) PathLossDB(l Link) float64 {
	d2D := clampDistance(l.D2DM)
	d3D := Link{D2DM: d2D, HBSm: l.HBSm, HUTm: l.HUTm}.D3DM()
	fGHz := l.FreqGHz()

	los := m.losPathLossDB(d2D, d3D, l.HBSm, l.HUTm, fGHz, l.FreqHz())
	if l.LOS {
		return los
	}
	return math.Max(los, m.nlosBasePathLossDB(d3D, fGHz, l.HBSm, l.HUTm))
}

// losPathLossDB, iki parçalı RMa LOS yol kaybıdır.
//
// UMa/UMi'den farklı olarak kırılma sonrası ifade, kırılma noktasındaki PL1
// değerine 40·log10(d_3D/d_BP) eklenerek kurulur.
//
// STANDART ARTEFAKTI (bilinçli olarak korunmuştur):
// TR 38.901, PL2 = PL1(d_BP) + 40·log10(d_3D/d_BP) yazarken **yatay** d_BP'yi,
// argümanı **eğik** d_3D olan PL1'e geçirir. Bu tutarsızlık kırılma noktasında
// O(Δh²/d_BP²) mertebesinde küçük bir süreksizlik bırakır:
//
//	800 MHz → −0,0051 dB      900 MHz → −0,0039 dB      1800 MHz → −0,0007 dB
//
// Standardın harfi izlenmiştir (yaygın referans uygulamalarla da uyumlu);
// büyüklük gölgeleme standart sapmasının (7 dB) binde biri mertebesinde olup
// fiziksel olarak anlamsızdır. Sürekliliği zorlamak formülü standarttan
// uzaklaştırırdı. Bkz. TestBreakpointContinuity.
func (m RMa) losPathLossDB(d2D, d3D, hBSm, hUTm, fGHz, fHz float64) float64 {
	dBP := rmaBreakpointDistanceM(hBSm, hUTm, fHz)

	if d2D <= dBP {
		return m.losNearPathLossDB(d3D, fGHz)
	}

	// PL2 = PL1(d_BP) + 40·log10(d_3D / d_BP)
	return m.losNearPathLossDB(dBP, fGHz) + rmaFarSlope*log10(d3D/dBP)
}

// losNearPathLossDB, kırılma mesafesinin altındaki LOS ifadesidir (PL1).
//
// Üç bileşen: serbest uzay tabanı, bina yüksekliğine bağlı ek eğim ve
// mesafeyle doğrusal artan atmosferik/bitki örtüsü terimi.
func (RMa) losNearPathLossDB(d3D, fGHz float64) float64 {
	h := rmaAvgBuildingHeightM
	hPow := math.Pow(h, rmaBuildingExponent)

	freeSpace := freqTermCoefDB * log10(rmaFreeSpaceCoef*d3D*fGHz)
	slopeTerm := math.Min(rmaSlopeCoef*hPow, rmaSlopeCap) * log10(d3D)
	interceptTerm := math.Min(rmaInterceptCoef*hPow, rmaInterceptCap)
	linearTerm := rmaLinearCoef * log10(h) * d3D

	return freeSpace + slopeTerm - interceptTerm + linearTerm
}

// nlosBasePathLossDB, RMa NLOS taban ifadesidir.
func (RMa) nlosBasePathLossDB(d3D, fGHz, hBSm, hUTm float64) float64 {
	h := rmaAvgBuildingHeightM
	w := rmaAvgStreetWidthM

	hbRatio := h / hBSm
	logHBS := log10(hBSm)

	return rmaNLOSInterceptDB -
		rmaNLOSStreetCoef*log10(w) +
		rmaNLOSBuildingCoef*log10(h) -
		(rmaNLOSHBSCoefA-rmaNLOSHBSCoefB*hbRatio*hbRatio)*logHBS +
		(rmaNLOSDistCoefA-rmaNLOSDistCoefB*logHBS)*(log10(d3D)-rmaNLOSDistOffset) +
		freqTermCoefDB*log10(fGHz) -
		(rmaNLOSHUTCoefA*square(log10(rmaNLOSHUTScale*hUTm)) - rmaNLOSHUTCoefB)
}

// rmaBreakpointDistanceM, RMa kırılma mesafesidir (metre):
//
//	d_BP = 2π · h_BS · h_UT · f_c / c        (f_c: Hz)
//
// UMa/UMi'deki 4·h'·h' biçiminden farklıdır: kırsalda etkin çevre yüksekliği
// düzeltmesi (h_E) uygulanmaz, gerçek anten yükseklikleri kullanılır.
func rmaBreakpointDistanceM(hBSm, hUTm, freqHz float64) float64 {
	return 2 * math.Pi * hBSm * hUTm * freqHz / speedOfLightMPS
}

// LOSProbability, RMa görüş hattı olasılığını döndürür — TR 38.901 Tablo 7.4.2-1:
//
//	d_2D ≤ 10 m : 1
//	aksi        : e^(−(d_2D − 10)/1000)
//
// Kırsalda engeller seyrek olduğundan sönüm ölçeği kentselden çok daha
// büyüktür (1000 m'ye karşı 63 m / 36 m).
func (RMa) LOSProbability(d2DM, _ float64) float64 {
	d := clampDistance(d2DM)
	if d <= rmaLOSProbCertainM {
		return 1
	}
	return clampProbability(math.Exp(-(d - rmaLOSProbCertainM) / rmaLOSProbDecayM))
}

// square, v²'yi döndürür — uzun formüllerin okunabilirliği için.
func square(v float64) float64 { return v * v }
