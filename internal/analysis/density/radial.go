// T-E03-06 — Radyal ağırlık (ADR-18).
//
//	w_rad(p) = Φ( (P_s(p) − RxSens) / σ_eff ) · w_TA(p)
//
// Birinci çarpan kaydın varlığından türer: kayıt varsa, o noktada serving
// hücrenin alınan gücü alıcı duyarlılığını aşmıştı. Gölgeleme rastgele
// olduğundan bu bir olasılıktır ve tam olarak hesaplanabilir — ADR-03'ün
// komşu kısıtıyla aynı mantık, keyfi katsayı yok.
//
// İkinci çarpan TA penceresidir; geometri katmanında üretilir (ADR-18/3,
// örtüşme oranı) ve buraya hazır gelir.

package density

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Config, kütle üretiminin koşu düzeyindeki parametreleridir.
//
// λ buraya **enjekte edilir** (T-E03-14): motor global durum okumaz,
// kalibrasyon döngüsü aynı motoru farklı λ ile çağırabilir.
type Config struct {
	// RxSensitivityDBm, alıcı duyarlılığıdır (network.rx_sensitivity_dbm).
	RxSensitivityDBm float64
	// SigmaNominalDB, nominal gölgeleme sapmasıdır (radio.shadowing_sigma_db).
	SigmaNominalDB float64
	// Lambda, kalibrasyon parametresidir (ADR-02, [0.5, 3.0]).
	Lambda float64
	// UTHeightM, alıcı yüksekliğidir.
	UTHeightM float64
}

// Validate, parametrelerin kullanılabilir olduğunu denetler.
func (c Config) Validate() error {
	switch {
	case !(c.RxSensitivityDBm < 0):
		return fmt.Errorf("kütle parametreleri: rx duyarlılığı negatif olmalı (%g)", c.RxSensitivityDBm)
	case !(c.SigmaNominalDB > 0):
		return fmt.Errorf("kütle parametreleri: σ_nominal pozitif olmalı (%g)", c.SigmaNominalDB)
	case !(c.Lambda > 0):
		return fmt.Errorf("kütle parametreleri: λ pozitif olmalı (%g)", c.Lambda)
	case !(c.UTHeightM > 0):
		return fmt.Errorf("kütle parametreleri: alıcı yüksekliği pozitif olmalı (%g)", c.UTHeightM)
	}
	return nil
}

// SigmaEffDB, etkin gölgeleme sapmasıdır: σ_eff = λ · σ_nominal (ADR-19/3).
//
// Analiz, yayılım modelinin kendi LOS/NLOS'a bağlı σ'sını **kullanmaz**;
// kalibrasyonun ölçeklediği terim tektir.
func (c Config) SigmaEffDB() float64 { return c.Lambda * c.SigmaNominalDB }

// CoverageProbability, kaydın var olmasının ima ettiği kapsama olasılığıdır.
//
//	Φ( (P_s(p) − RxSens) / σ_eff )
//
// Marj büyükse 1'e, duyarlılığın altındaysa 0'a gider. Sert bir "r_max içinde
// mi" testi yerine bu kullanılır: r_max zaten bu eşiğin deterministik
// karşılığıdır ve gölgeleme onu her iki yöne kaydırabilir.
func CoverageProbability(cell params.Cell, p geo.Point, cfg Config) float64 {
	margin := ReceivedPowerDBm(cell, p, cfg.UTHeightM) - cfg.RxSensitivityDBm
	return NormalCDF(margin / cfg.SigmaEffDB())
}

// Radial, radyal ağırlıktır [0,1].
//
// taWeight, geometry.Window'dan gelen TA örtüşme oranıdır; TA yoksa 1
// verilmelidir.
func Radial(cell params.Cell, p geo.Point, taWeight float64, cfg Config) float64 {
	if taWeight <= 0 {
		return 0
	}
	w := CoverageProbability(cell, p, cfg) * math.Min(taWeight, 1)
	if w < 0 {
		return 0
	}
	return w
}
