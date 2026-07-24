// T-E02-05 — r_max (maksimum kapsama yarıçapı) ataması.
//
// Karar: Sprint 1'de RF modeli yazılmaz. r_max, morfoloji profilinden sabit
// okunur (kentsel 5 km / kırsal 20 km) ve YAML'da `network.r_max_m` varsa
// onunla ezilir. Gerçek link budget hesabı T-E02-10'da (3GPP TR 38.901
// UMa/UMi/RMa) devreye girecek ve bu dosyanın içi değişecektir; imza aynı
// kalacak şekilde tasarlanmıştır.
//
// Plan risk tablosu bu sırayı zaten öngörür: "Önce sabit r_max ile uçtan uca
// zincir; sonra formülleri zenginleştir."
package inventory

import (
	"fmt"
	"log/slog"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// RMax, profilden geçerli kapsama yarıçapını döndürür (metre).
//
// T-E02-10 sonrasında bu işlev EIRP, frekans, anten yüksekliği ve
// rx_sensitivity_dbm üzerinden link budget çözecektir; şimdilik profil değeri
// doğrudan döner.
func RMax(p config.MorphologyProfile) float64 {
	return p.RMaxM
}

// AssignRMax, envanterdeki her hücreye r_max atar.
//
// Hücreler yerinde (in-place) güncellenir. Atama sonrası her hücre
// Validate()'ten geçebilir duruma gelir (r_max > 0 kısıtı).
func AssignRMax(cells []Cell, p config.MorphologyProfile) error {
	rMax := RMax(p)
	if !(rMax > 0) {
		return fmt.Errorf("r_max ataması: r_max pozitif olmalı (%g m)", rMax)
	}
	if len(cells) == 0 {
		return fmt.Errorf("r_max ataması: hücre listesi boş")
	}

	warnModelValidity(p, rMax)

	for i := range cells {
		cells[i].RMaxM = rMax
	}
	return nil
}

// warnModelValidity, kapsama yarıçapının yayılım modeli geçerlilik sınırını
// aşıp aşmadığını denetler ve aşıyorsa uyarır.
//
// Kırsal profilde r_max = 20 km iken RMa geçerlilik sınırı d_max = 10 km'dir:
// T-E02-10'da model bu aralıkta dışdeğerleme (extrapolation) ile çalışacaktır.
// Sabit r_max aşamasında bu bir hata değildir — koşuyu durdurmak yerine
// görünür kılınır.
func warnModelValidity(p config.MorphologyProfile, rMaxM float64) {
	dMaxM := p.DMaxKM * 1000
	if rMaxM > dMaxM {
		slog.Warn("r_max, yayılım modeli geçerlilik sınırını aşıyor",
			"morphology", string(p.Morphology),
			"model", string(p.PropagationModel),
			"r_max_m", rMaxM,
			"d_max_m", dMaxM,
			"not", "T-E02-10'da model bu aralıkta dışdeğerleme yapacak",
		)
	}
}
