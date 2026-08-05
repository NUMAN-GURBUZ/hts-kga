// T-E03-03 — Açısal ağırlık (anten deseni).
//
// Kayıt, kullanıcının `s` sektörüne bağlı olduğunu söyler. Sektörün yayını
// yönlüdür: hüzme ekseninde en güçlü, kenarlara doğru zayıflar, arkada
// bastırılır. Açısal ağırlık bu deseni [0,1] aralığına taşır.
//
// Desen fonksiyonu simülatörünkiyle **aynıdır** (internal/rf paketinden
// çağrılır). Analiz tarafında basitleştirilmiş bir desen kullanılsaydı — ör.
// yalnızca azimut, eğim yok sayılarak — sistematik bir biçim hatası doğardı;
// λ kalibrasyonu ölçek hatasını emer, biçim hatasını emmez.

package density

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Angular, verilen noktadaki açısal ağırlıktır [0,1].
//
//	w_ang(p) = 10^( −A_hüzme(φ, θ) / 10 )
//
// A_hüzme dB cinsinden zayıflamadır (0 = eksende, en çok ön-arka bastırma
// kadar). Doğrusal orana çevrilmesi, ağırlığın çarpımsal bileşenler arasında
// tutarlı bir "oran" olmasını sağlar: dB'yi doğrudan ağırlık olarak kullanmak
// birimsiz bir çarpanı logaritmik bir büyüklükle karıştırmak olurdu.
//
// Eksendeki nokta 1, ön-arka bastırma sınırındaki nokta 10^(−A_max/10) alır;
// desen hiçbir yerde sıfırlanmaz, dolayısıyla arka lobdaki gerçek konum
// kütleden dışlanmaz — yalnızca çok küçük ağırlık alır.
func Angular(cell params.Cell, p geo.Point, utHeightM float64) float64 {
	d := p.Sub(cell.Site)
	d2D := math.Hypot(d.X, d.Y)

	offAxis := 0.0
	if d.X != 0 || d.Y != 0 {
		offAxis = rf.WrapAngleDeg(rf.BearingDeg(d.X, d.Y) - cell.AzimuthDeg)
	}
	elevation := rf.ElevationDeg(cell.AntHeightM-utHeightM, d2D)

	attenDB := rf.AntennaAttenuationDB(offAxis, elevation, cell.BeamWidthDeg, cell.TiltDeg)
	return math.Pow(10, -attenDB/10)
}
