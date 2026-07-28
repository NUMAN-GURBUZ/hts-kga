// Package density, olasılık kütlesinin ağırlık bileşenlerini üretir:
// açısal (w_ang), radyal (w_rad) ve komşu kısıtı (w_nbr).
//
// Paketin tamamı **deterministiktir**: gölgelemenin gerçekleşmiş değerini
// bilmez, yalnızca envanterdeki parametrelerden ve σ_eff'ten olasılık
// hesaplar (ADR-03, ADR-19). Bu bilgi asimetrisi çalışmanın ana iddiasıdır;
// ADR-20 import kısıtı onu kod düzeyinde korur.
package density

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// MixedPathLossDB, LOS durumu bilinmeyen bir bağ için yol kaybını döndürür
// (ADR-19).
//
// Hesabın kendisi internal/rf içindedir: aynı karışım, simülatörün r_max
// önhesabında da (T-E02-05) kullanılır. İki taraf ayrı yazsaydı arama bölgesi
// ile kapsama olasılığı ayrışırdı — bu sarmalayıcı, çağrı yerini analiz
// katmanında okunur tutar.
func MixedPathLossDB(model rf.PathLossModel, link rf.Link, utHeightM float64) float64 {
	return rf.MixedPathLossDB(model, link, utHeightM)
}

// ReceivedPowerDBm, gölgelemesiz alınan gücü döndürür:
//
//	P(p) = EIRP − PL_karışım(d) − A_hüzme(φ, θ)
//
// ADR-03'ün Δ(p) marjı ve ADR-18'in kapsama olasılığı bu değerden hesaplanır.
// Gölgeleme kasten dâhil değildir: analiz gerçekleşmiş değeri bilmez, onun
// dağılımını (σ_eff) kullanır.
func ReceivedPowerDBm(cell params.Cell, p geo.Point, utHeightM float64) float64 {
	d := p.Sub(cell.Site)
	d2D := math.Hypot(d.X, d.Y)

	link := rf.Link{
		D2DM:    d2D,
		HBSm:    cell.AntHeightM,
		HUTm:    utHeightM,
		FreqMHz: cell.FreqMHz,
	}
	pathLoss := MixedPathLossDB(cell.Model, link, utHeightM)

	offAxis := 0.0
	if d.X != 0 || d.Y != 0 {
		offAxis = rf.WrapAngleDeg(rf.BearingDeg(d.X, d.Y) - cell.AzimuthDeg)
	}
	elevation := rf.ElevationDeg(cell.AntHeightM-utHeightM, d2D)
	beamLoss := rf.AntennaAttenuationDB(offAxis, elevation, cell.BeamWidthDeg, cell.TiltDeg)

	return cell.EIRPdBm - pathLoss - beamLoss
}

// NormalCDF, standart normal dağılım fonksiyonudur Φ(x).
//
// ADR-03 ve ADR-18'in ortak yapı taşıdır. erfc üzerinden hesaplanır: doğrudan
// seri açılımı uç değerlerde duyarlılık kaybeder.
func NormalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}
