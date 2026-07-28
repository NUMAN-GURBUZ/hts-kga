// T-E02-05 — r_max (maksimum kapsama yarıçapı) ataması.
//
// # Revizyon (2026-07-27)
//
// İlk sürümde r_max, morfoloji profilinden **sabit** okunuyordu (kentsel 5 km /
// kırsal 20 km); dosyanın kendi yorumu "T-E02-10 sonrasında link budget
// çözecektir" diyordu ama 3GPP modelleri yazıldığında buraya dönülmemişti.
//
// Sonuç ölçüldü ve sessiz değildi: kentsel profilde alınan güç 5 km'de hâlâ
// −106,5 dBm, yani duyarlılığın 3,5 dB üstündeydi. Hücreler fiziksel olarak
// ulaştıkları yerin berisinde kesiliyor, analiz de arama bölgesini aynı yerde
// kırpıyordu. Kütlenin kuyruğu sistematik olarak kesildiğinden K2/K3 daralma
// oranları **iyimser yanlı** çıkardı — ölçülen kazanç modelin değil kırpmanın
// eseri olurdu.
//
// Artık r_max hücre başına gerçek link budget'tan çözülür (internal/rf):
//
//	EIRP − PL_karışım(d) − A_hüzme(0, θ(d)) = rx_sensitivity_dbm
//
// Aynı hesabı analiz de kullanır (ADR-19 log-alanı karışımı), bu yüzden r_max
// mesafesinde kapsama olasılığı tam 0,5 çıkar: sınır iki taraf için de modelin
// orta noktasıdır. `network.r_max_m` verilmişse o kazanır — senaryo yazarının
// açık tercihi hesabı ezer.
package inventory

import (
	"fmt"
	"log/slog"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
)

// RMaxOverride, senaryodaki açık r_max tercihidir; yoksa nil döner.
func RMaxOverride(scn *config.Scenario) *float64 {
	if scn == nil {
		return nil
	}
	return scn.Network.RMaxM
}

// SolveRMax, tek bir hücrenin kapsama yarıçapını link budget'tan çözer.
//
// Hücre kendi frekansını, EIRP'sini, anten yüksekliğini ve eğimini taşır:
// farklı banda düşen sektörler farklı r_max alır. Sabit profil değeri bunu
// yapamıyordu — 800 MHz ile 2600 MHz aynı yarıçapı görüyordu.
func SolveRMax(c Cell, rxSensitivityDBm float64) (float64, error) {
	model, err := rf.ModelFor(c.ModelType)
	if err != nil {
		return 0, fmt.Errorf("r_max çözümü: hücre %s: %w", c.ID, err)
	}

	spec := rf.RangeSpec{
		EIRPdBm:          c.EIRPdBm,
		RxSensitivityDBm: rxSensitivityDBm,
		HBSm:             c.AntHeightM,
		HUTm:             rf.UTHeightM,
		FreqMHz:          c.FreqMHz,
		BeamWidthDeg:     c.BeamWidth,
		TiltDeg:          c.TiltDeg,
	}

	rMax, err := rf.CoverageRangeM(model, spec)
	if err != nil {
		return 0, fmt.Errorf("r_max çözümü: hücre %s (%d MHz): %w", c.ID, c.FreqMHz, err)
	}
	return rMax, nil
}

// AssignRMax, envanterdeki her hücreye r_max atar (yerinde).
//
// Atama sonrası her hücre Validate()'ten geçebilir duruma gelir (r_max > 0).
func AssignRMax(cells []Cell, scn *config.Scenario) error {
	if scn == nil {
		return fmt.Errorf("r_max ataması: senaryo zorunlu")
	}
	if len(cells) == 0 {
		return fmt.Errorf("r_max ataması: hücre listesi boş")
	}

	if override := RMaxOverride(scn); override != nil {
		if !(*override > 0) {
			return fmt.Errorf("r_max ataması: network.r_max_m pozitif olmalı (%g m)", *override)
		}
		for i := range cells {
			cells[i].RMaxM = *override
		}
		slog.Info("r_max senaryo config'inden alındı (link budget atlandı)",
			"r_max_m", *override, "hücre", len(cells))
		warnModelValidity(scn.Profile, *override)
		return nil
	}

	minM, maxM := 0.0, 0.0
	for i := range cells {
		rMax, err := SolveRMax(cells[i], scn.Network.RxSensitivityDBm)
		if err != nil {
			return err
		}
		cells[i].RMaxM = rMax

		if i == 0 || rMax < minM {
			minM = rMax
		}
		if rMax > maxM {
			maxM = rMax
		}
	}

	slog.Info("r_max link budget'tan çözüldü (T-E02-05)",
		"morphology", string(scn.Profile.Morphology),
		"model", string(scn.Profile.PropagationModel),
		"rx_sensitivity_dbm", scn.Network.RxSensitivityDBm,
		"r_max_min_m", minM,
		"r_max_max_m", maxM,
		"hücre", len(cells),
	)
	warnModelValidity(scn.Profile, maxM)
	return nil
}

// warnModelValidity, kapsama yarıçapının yayılım modeli geçerlilik sınırını
// aşıp aşmadığını denetler ve aşıyorsa uyarır.
//
// Kırsal profilde RMa geçerlilik sınırı d_max = 10 km'dir; link budget bunun
// ötesinde bir yarıçap verirse model dışdeğerleme (extrapolation) ile
// çalışıyor demektir. Koşuyu durdurmak yerine görünür kılınır: eşik aşımı
// planın risk tablosunda zaten öngörülmüştür.
func warnModelValidity(p config.MorphologyProfile, rMaxM float64) {
	dMaxM := p.DMaxKM * 1000
	if rMaxM > dMaxM {
		slog.Warn("r_max, yayılım modeli geçerlilik sınırını aşıyor",
			"morphology", string(p.Morphology),
			"model", string(p.PropagationModel),
			"r_max_m", rMaxM,
			"d_max_m", dMaxM,
			"not", "model bu aralıkta dışdeğerleme yapıyor",
		)
	}
}
