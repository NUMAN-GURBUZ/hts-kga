// T-E02-12 — Anten deseni tablo testleri.
//
// ADR-20 ile yol kaybı ve anten deseni internal/rf'e taşındığında bu testler
// de birlikte geldi: sınadıkları fonksiyonlar artık bu paketin içindedir ve
// bir kısmı (horizontalAttenuationDB, frontToBackDB) dışa açık değildir.
package rf

import (
	"math"
	"testing"
)

// Sektör parametreleri (ADR-17 kentsel profil). Aynı değerler
// internal/simulator/radio test dosyalarında da vardır; ADR-20 taşımasından
// sonra iki paket test yardımcılarını paylaşamaz.
const (
	testBeamWidthDeg = 65.0
	testTiltDeg      = 6.0
)

// ─── Anten deseni ────────────────────────────────────────────────────────────

// TestAntennaPattern_ReferenceValues, TR 38.901 Tablo 7.3-1 tanımını sınar.
func TestAntennaPattern_ReferenceValues(t *testing.T) {
	t.Run("hüzme merkezi sıfır zayıflama", func(t *testing.T) {
		// Δφ = 0 ve elevation = tilt → her iki bileşen de 0
		if got := AntennaAttenuationDB(0, testTiltDeg, testBeamWidthDeg, testTiltDeg); got != 0 {
			t.Errorf("hüzme merkezinde zayıflama %g, beklenen 0", got)
		}
	})

	t.Run("yarım hüzme genişliğinde 3 dB", func(t *testing.T) {
		// φ = φ_3dB/2 → 12·(0.5)² = 3 dB (3 dB genişliği tanımı)
		got := horizontalAttenuationDB(testBeamWidthDeg/2, testBeamWidthDeg)
		if math.Abs(got-3) > 1e-12 {
			t.Errorf("yarım hüzmede %g dB, beklenen 3 dB", got)
		}
	})

	t.Run("ön-arka bastırma sınırı", func(t *testing.T) {
		if got := horizontalAttenuationDB(180, testBeamWidthDeg); got != frontToBackDB {
			t.Errorf("arka yönde %g dB, beklenen %g dB", got, frontToBackDB)
		}
	})

	t.Run("toplam zayıflama A_max ile kırpılır", func(t *testing.T) {
		got := AntennaAttenuationDB(180, 90, testBeamWidthDeg, testTiltDeg)
		if got != frontToBackDB {
			t.Errorf("toplam zayıflama %g dB, beklenen kırpma %g dB", got, frontToBackDB)
		}
	})

	t.Run("zayıflama daima [0, A_max]", func(t *testing.T) {
		for phi := -360.0; phi <= 360; phi += 7 {
			for elev := -90.0; elev <= 90; elev += 7 {
				got := AntennaAttenuationDB(phi, elev, testBeamWidthDeg, testTiltDeg)
				if got < 0 || got > frontToBackDB {
					t.Fatalf("A(%.0f, %.0f) = %g — [0, %g] dışında",
						phi, elev, got, frontToBackDB)
				}
			}
		}
	})
}

// TestWrapAngleDeg, açı indirgemesini sınar.
func TestWrapAngleDeg(t *testing.T) {
	tests := []struct{ in, want float64 }{
		{0, 0}, {90, 90}, {180, 180}, {181, -179},
		{270, -90}, {350, -10}, {360, 0}, {-90, -90},
		{-181, 179}, {-350, 10}, {720, 0}, {450, 90},
	}
	for _, tc := range tests {
		if got := WrapAngleDeg(tc.in); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("WrapAngleDeg(%g) = %g, beklenen %g", tc.in, got, tc.want)
		}
	}
}

// TestBearingDeg, ENU yön hesabını sınar (kuzeyden saat yönünde).
func TestBearingDeg(t *testing.T) {
	tests := []struct {
		name   string
		dx, dy float64
		want   float64
	}{
		{"kuzey", 0, 100, 0},
		{"doğu", 100, 0, 90},
		{"güney", 0, -100, 180},
		{"batı", -100, 0, -90},
		{"kuzeydoğu", 100, 100, 45},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BearingDeg(tc.dx, tc.dy); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("BearingDeg(%g, %g) = %g, beklenen %g", tc.dx, tc.dy, got, tc.want)
			}
		})
	}
}

// TestElevationDeg, düşey açı hesabını sınar.
func TestElevationDeg(t *testing.T) {
	// 45° üçgen
	if got := ElevationDeg(100, 100); math.Abs(got-45) > 1e-9 {
		t.Errorf("ElevationDeg(100, 100) = %g, beklenen 45", got)
	}
	// Uzakta → sıfıra yaklaşır
	if got := ElevationDeg(23.5, 100000); got > 0.02 {
		t.Errorf("çok uzakta düşey açı %g, sıfıra yakın olmalı", got)
	}
	// Antenin tam altı
	if got := ElevationDeg(23.5, 0); got != 90 {
		t.Errorf("anten altında %g, beklenen 90", got)
	}
}
