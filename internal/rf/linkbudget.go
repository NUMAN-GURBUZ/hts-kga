// T-E02-05 (revizyon) — Link budget ve kapsama yarıçapı çözümü.
//
// Bu dosya iki tarafın **ortak** hesabıdır:
//
//	simülatör (T-E02-05) : hücre başına r_max önhesabı
//	analiz    (T-E03-06) : kapsama olasılığının içindeki alınan güç
//
// Ayrı yazılsalardı, simülatörün "bu hücre buraya kadar ulaşır" dediği sınır
// ile analizin "kayıt varsa sinyal şu kadardı" hesabı ayrışırdı. Sınır
// analizin hesabından **dar** olsaydı kütlenin kuyruğu kırpılır ve K2/K3
// daralma oranları iyimser yanlı çıkardı; **geniş** olsaydı analiz simülatörün
// hiç üretmediği bölgeye kütle dağıtırdı. Tanım bu yüzden tektir.
//
// # LOS bilinmez
//
// r_max, ortada bir kullanıcı yokken hesaplanır; LOS durumu tanımlı değildir.
// Analiz de LOS'u bilmez (ADR-19). İkisi de aynı log-alanı karışımını kullanır,
// böylece r_max mesafesinde kapsama olasılığı tam olarak 0,5 çıkar — sınır,
// modelin kendi orta noktasıdır.

package rf

import (
	"fmt"
	"math"
)

// MixedPathLossDB, LOS durumu bilinmeyen bir bağ için yol kaybını döndürür
// (ADR-19).
//
//	PL = p_LOS · PL_LOS + (1 − p_LOS) · PL_NLOS
//
// Bir **log-alanı karışımıdır**, beklenen değer değildir: lineer güç
// alanındaki beklenti farklı bir sayı verirdi. TR 38.901 modelleri zaten dB
// medyan olarak tanımlı olduğundan karışım aynı alanda yapılır.
//
// Simülatörün tick başına best-server hesabı bunu kullanmaz; orada LOS
// gerçekleşmiş bir olaydır. Burada yalnızca kullanıcıdan bağımsız
// önhesaplar (r_max) ve analiz kullanır.
func MixedPathLossDB(model PathLossModel, link Link, utHeightM float64) float64 {
	pLOS := model.LOSProbability(link.D2DM, utHeightM)

	losLink := link
	losLink.LOS = true
	nlosLink := link
	nlosLink.LOS = false

	return pLOS*model.PathLossDB(losLink) + (1-pLOS)*model.PathLossDB(nlosLink)
}

// RangeSpec, kapsama yarıçapı çözümünün girdisidir.
type RangeSpec struct {
	// EIRPdBm, sektörün yayılan izotropik gücüdür.
	EIRPdBm float64
	// RxSensitivityDBm, alıcı duyarlılığıdır (negatif).
	RxSensitivityDBm float64
	// HBSm, direk anten yüksekliğidir.
	HBSm float64
	// HUTm, alıcı yüksekliğidir.
	HUTm float64
	// FreqMHz, taşıyıcı frekanstır.
	FreqMHz int
	// BeamWidthDeg, yatay 3 dB hüzme genişliğidir.
	BeamWidthDeg float64
	// TiltDeg, elektriksel aşağı eğimdir.
	TiltDeg float64
}

// maxSearchRangeM, çözümün arandığı üst sınırdır.
//
// Fiziksel bir sınır değil, ıraksama koruyucusudur: hiçbir hücre 200 km'ye
// ulaşmaz. Çözüm bu sınıra dayanırsa yapılandırma hatalıdır.
const maxSearchRangeM = 200000.0

// bisectionSteps, ikili aramanın adım sayısıdır.
//
// 200 km aralık 60 adımda 10⁻¹³ m'ye iner; metre altı hassasiyet fazlasıyla
// yeterlidir ama sabit adım sayısı sonucu **deterministik** kılar (K10):
// tolerans tabanlı durdurma, kayan nokta sırasına göre bir adım oynayabilirdi.
const bisectionSteps = 60

// Validate, link budget girdilerini denetler.
func (s RangeSpec) Validate() error {
	switch {
	case !(s.RxSensitivityDBm < 0):
		return fmt.Errorf("link budget: rx duyarlılığı negatif olmalı (%g dBm)", s.RxSensitivityDBm)
	case !isPositiveFinite(s.HBSm) || !isPositiveFinite(s.HUTm):
		return fmt.Errorf("link budget: anten yükseklikleri pozitif olmalı (h_BS=%g, h_UT=%g)",
			s.HBSm, s.HUTm)
	case s.HBSm <= s.HUTm:
		return fmt.Errorf("link budget: h_BS > h_UT olmalı (%g ≤ %g)", s.HBSm, s.HUTm)
	case s.FreqMHz <= 0:
		return fmt.Errorf("link budget: frekans pozitif olmalı (%d MHz)", s.FreqMHz)
	case !isPositiveFinite(s.BeamWidthDeg):
		return fmt.Errorf("link budget: hüzme genişliği pozitif olmalı (%g)", s.BeamWidthDeg)
	case math.IsNaN(s.TiltDeg) || s.TiltDeg < 0:
		return fmt.Errorf("link budget: eğim negatif olmayan bir sayı olmalı (%g)", s.TiltDeg)
	case math.IsNaN(s.EIRPdBm) || math.IsInf(s.EIRPdBm, 0):
		return fmt.Errorf("link budget: EIRP sonlu olmalı (%g)", s.EIRPdBm)
	}
	return nil
}

// ReceivedPowerDBm, hüzme ekseni üzerindeki alınan gücü döndürür (gölgelemesiz).
//
//	P(d) = EIRP − PL_karışım(d) − A_hüzme(0, θ(d))
//
// Azimut sapması sıfırdır: r_max, sektörün **en uzağa ulaştığı** yön olan
// hüzme ekseni boyunca tanımlıdır. Kenar yönlerde erişim doğal olarak daha
// kısadır ve bunu anten deseni zaten modelller.
func (s RangeSpec) ReceivedPowerDBm(model PathLossModel, d2DM float64) float64 {
	link := Link{
		D2DM:    d2DM,
		HBSm:    s.HBSm,
		HUTm:    s.HUTm,
		FreqMHz: s.FreqMHz,
	}
	pathLoss := MixedPathLossDB(model, link, s.HUTm)
	beamLoss := AntennaAttenuationDB(0, ElevationDeg(s.HBSm-s.HUTm, d2DM), s.BeamWidthDeg, s.TiltDeg)
	return s.EIRPdBm - pathLoss - beamLoss
}

// BeamPeakDistanceM, düşey desenin tepe noktasının mesafesidir.
//
// Eğim, hüzme merkezini yataydan aşağı kaydırır: düşey açı tam eğim kadar
// olduğunda zayıflama sıfırdır. Bu noktadan **önce** alınan güç mesafeyle
// artar (anten yere değil ufka bakar), sonra azalır. Kapsama yarıçapı arayışı
// bu yüzden burada başlar; daha erken başlarsa ikili arama tek yönlü olmayan
// bir eğri üzerinde çalışırdı.
//
// Eğim sıfıra yaklaştıkça tepe ufka kayar ve mesafe taşar; bu durumda 0
// döner ve arama 1 m'den başlar. Sıfır eğimde de tek bir tepe vardır (yakın
// mesafede düşey zayıflama baskındır, uzakta yol kaybı), yalnızca yeri kapalı
// formülle verilemez.
func (s RangeSpec) BeamPeakDistanceM() float64 {
	if !(s.TiltDeg > 0) {
		return 0
	}
	peak := (s.HBSm - s.HUTm) / math.Tan(s.TiltDeg*math.Pi/180)
	if math.IsInf(peak, 0) || math.IsNaN(peak) || peak >= maxSearchRangeM {
		return 0
	}
	return peak
}

// CoverageRangeM, alınan gücün alıcı duyarlılığına düştüğü mesafeyi döndürür.
//
// Çözüm ikili aramadır: P(d) hüzme tepesinden sonra kesin azalandır, bu yüzden
// kök tektir. Sabit adım sayısı sonucu tekrarlanabilir kılar (K10).
//
// Sönümleme payı (fade margin) **eklenmez**. Eklenseydi hangi yüzdeliğin
// seçileceği yeni bir serbest parametre olurdu; paysız çözümde sınır, modelin
// medyanıdır ve analizdeki kapsama olasılığı orada tam 0,5 çıkar — iki taraf
// aynı noktada buluşur.
func CoverageRangeM(model PathLossModel, s RangeSpec) (float64, error) {
	if model == nil {
		return 0, fmt.Errorf("link budget: yayılım modeli zorunlu")
	}
	if err := s.Validate(); err != nil {
		return 0, err
	}

	lo := s.BeamPeakDistanceM()
	if !(lo > 0) || math.IsInf(lo, 0) || math.IsNaN(lo) {
		lo = 1 // sıfır mesafede yol kaybı tanımsızdır
	}
	if s.ReceivedPowerDBm(model, lo) < s.RxSensitivityDBm {
		return 0, fmt.Errorf(
			"link budget: hüzme tepesinde (%.1f m) bile sinyal duyarlılığın altında "+
				"(%.2f < %.2f dBm) — EIRP, anten yüksekliği veya duyarlılık tutarsız",
			lo, s.ReceivedPowerDBm(model, lo), s.RxSensitivityDBm)
	}

	hi := maxSearchRangeM
	if s.ReceivedPowerDBm(model, hi) >= s.RxSensitivityDBm {
		return 0, fmt.Errorf(
			"link budget: %.0f km'de bile sinyal duyarlılığın üstünde — "+
				"yapılandırma fiziksel değil (EIRP=%.1f dBm, rx=%.1f dBm)",
			maxSearchRangeM/1000, s.EIRPdBm, s.RxSensitivityDBm)
	}

	for i := 0; i < bisectionSteps; i++ {
		mid := (lo + hi) / 2
		if s.ReceivedPowerDBm(model, mid) >= s.RxSensitivityDBm {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2, nil
}
