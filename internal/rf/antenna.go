// T-E02-12 — 3GPP TR 38.901 anten hüzme deseni (Tablo 7.3-1).
//
// Neden zorunlu: aynı sitenin üç sektörü aynı direkte, aynı yükseklikte, aynı
// EIRP ile yayın yapar ve (T-E02-11 kararı gereği) aynı gölgelemeyi görür.
// Anten deseni olmasaydı üçünün alınan gücü **birebir eşit** olurdu ve
// best-server keyfî seçim yapardı — sektörlü şebeke sektörsüze dönerdi.
//
// Desen, sektörleri ayıran tek fiziksel unsurdur.
package rf

import "math"

// Anten deseni katsayıları — TR 38.901 Tablo 7.3-1.
const (
	// patternSlope, 3 dB hüzme genişliği tanımından gelen katsayıdır:
	// açı = θ_3dB/2 olduğunda zayıflama tam 3 dB olur (12·(1/2)² = 3).
	patternSlope = 12.0

	// frontToBackDB, yatay düzlemde ön-arka bastırma sınırıdır (A_max).
	frontToBackDB = 30.0

	// sideLobeDB, düşey düzlemde yan kulak seviyesi sınırıdır (SLA_V).
	sideLobeDB = 30.0

	// verticalBeamWidthDeg, düşey 3 dB hüzme genişliğidir (θ_3dB).
	// Yatay genişlik hücre parametresinden gelir (cells.beam_width = 65°);
	// düşey genişlik standartta aynı değerde sabittir.
	verticalBeamWidthDeg = 65.0

	degPerHalfTurn = 180.0
	degPerTurn     = 360.0
)

// AntennaAttenuationDB, sektör anteninin verilen yöndeki zayıflatmasını
// döndürür (dB, **pozitif**: 0 = hüzme merkezi, 30 = tam bastırma).
//
// ADR-03'ün P = EIRP − PL − A_beam gösterimiyle uyumludur.
//
// Girdiler:
//   - deltaAzimuthDeg: sektör azimutu ile hedef yönü arasındaki fark
//   - elevationDeg:    hedefin yatay düzleme göre düşey açısı (aşağısı pozitif)
//   - beamWidthDeg:    yatay 3 dB hüzme genişliği (cells.beam_width)
//   - tiltDeg:         elektriksel aşağı eğim (cells.tilt_deg)
func AntennaAttenuationDB(deltaAzimuthDeg, elevationDeg, beamWidthDeg, tiltDeg float64) float64 {
	horizontal := horizontalAttenuationDB(deltaAzimuthDeg, beamWidthDeg)
	vertical := verticalAttenuationDB(elevationDeg, tiltDeg)

	// TR 38.901: A(φ,θ) = −min{ −[A_V + A_H], A_max }
	// Pozitif zayıflama gösteriminde bu, toplamın A_max ile kırpılmasıdır.
	return math.Min(horizontal+vertical, frontToBackDB)
}

// horizontalAttenuationDB, yatay düzlem desenidir (TR 38.901 Tablo 7.3-1):
//
//	A_H(φ) = min[ 12·(φ/φ_3dB)² , A_max ]
func horizontalAttenuationDB(deltaAzimuthDeg, beamWidthDeg float64) float64 {
	phi := WrapAngleDeg(deltaAzimuthDeg)
	ratio := phi / beamWidthDeg
	return math.Min(patternSlope*ratio*ratio, frontToBackDB)
}

// verticalAttenuationDB, düşey düzlem desenidir (TR 38.901 Tablo 7.3-1):
//
//	A_V(θ) = min[ 12·((θ − θ_tilt)/θ_3dB)² , SLA_V ]
//
// Eğim (downtilt), hüzme merkezini yataydan aşağı kaydırır: hedef tam eğim
// açısındaysa zayıflama sıfırdır.
func verticalAttenuationDB(elevationDeg, tiltDeg float64) float64 {
	delta := elevationDeg - tiltDeg
	ratio := delta / verticalBeamWidthDeg
	return math.Min(patternSlope*ratio*ratio, sideLobeDB)
}

// WrapAngleDeg, açıyı (−180, 180] aralığına indirger.
//
// Azimut farkları için zorunludur: 350° ile 10° arasındaki gerçek fark
// 340° değil 20°'dir.
func WrapAngleDeg(deg float64) float64 {
	wrapped := math.Mod(deg, degPerTurn)
	if wrapped > degPerHalfTurn {
		wrapped -= degPerTurn
	} else if wrapped <= -degPerHalfTurn {
		wrapped += degPerTurn
	}
	return wrapped
}

// BearingDeg, kaynaktan hedefe olan yatay yönü döndürür (derece, kuzeyden
// saat yönünde: 0° = kuzey, 90° = doğu).
//
// ENU düzleminde X doğu, Y kuzeydir; bu yüzden atan2(doğu, kuzey) kullanılır.
func BearingDeg(dxEast, dyNorth float64) float64 {
	return math.Atan2(dxEast, dyNorth) * degPerHalfTurn / math.Pi
}

// ElevationDeg, alıcının vericiye göre düşey açısını döndürür (derece).
//
// Pozitif değer alıcının anten yüksekliğinin **altında** olduğunu gösterir —
// aşağı eğim (downtilt) ile aynı işaret düzlemindedir.
func ElevationDeg(heightDiffM, horizontalDistM float64) float64 {
	if horizontalDistM <= 0 {
		// Antenin tam altında: hüzme merkezine göre en uç düşey açı.
		return degPerHalfTurn / 2
	}
	return math.Atan2(heightDiffM, horizontalDistM) * degPerHalfTurn / math.Pi
}
