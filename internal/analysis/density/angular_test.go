package density

import (
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Altın senaryo parametreleri: tek site, başlangıçta, kuzeye bakan sektör.
// ADR-17 kentsel profil (configs/urban_ta.yaml).
const (
	testAzimuthDeg   = 0.0
	testBeamWidthDeg = 65.0
	testTiltDeg      = 6.0
	testEIRPdBm      = 58.0
	testFreqMHz      = 2100
	testAntHeightM   = 25.0
	testUTHeightM    = rf.UTHeightM // 1,5 m
	testRMaxM        = 5000.0
)

// goldenCell, altın senaryonun tek sektörünü kurar.
func goldenCell(t *testing.T) params.Cell {
	t.Helper()
	model, err := rf.ModelFor(config.ModelUMa)
	if err != nil {
		t.Fatalf("rf.ModelFor: %v", err)
	}
	return params.Cell{
		ID:           uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		SiteID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		Site:         geo.Point{},
		AzimuthDeg:   testAzimuthDeg,
		BeamWidthDeg: testBeamWidthDeg,
		TiltDeg:      testTiltDeg,
		EIRPdBm:      testEIRPdBm,
		FreqMHz:      testFreqMHz,
		AntHeightM:   testAntHeightM,
		RMaxM:        testRMaxM,
		Model:        model,
	}
}

// at, direğin çevresinde verilen yön ve mesafedeki noktayı döndürür.
func at(bearingDeg, distM float64) geo.Point {
	rad := bearingDeg * math.Pi / 180
	return geo.Point{X: distM * math.Sin(rad), Y: distM * math.Cos(rad)}
}

// TestAngular_HandComputedReferences, açısal ağırlığı TR 38.901 Tablo 7.3-1'den
// **elle** hesaplanmış değerlerle karşılaştırır.
//
// Hesap (h_BS − h_UT = 23,5 m, θ_3dB = 65°, tilt = 6°):
//
//	θ(d)   = atan(23,5 / d)
//	A_H(φ) = min[ 12·(φ/65)² , 30 ]
//	A_V(θ) = min[ 12·((θ−6)/65)² , 30 ]
//	A      = min[ A_H + A_V , 30 ]
//	w_ang  = 10^(−A/10)
//
// d = 500 m için θ = 2,690921387836609°, A_V = 0,031100595298870 dB.
func TestAngular_HandComputedReferences(t *testing.T) {
	cell := goldenCell(t)

	cases := []struct {
		name     string
		bearing  float64
		distM    float64
		wantAdB  float64
		wantWAng float64
	}{
		// A_H: 0° → 0 · 32,5° → 12·(0,5)² = 3 · 65° → 12·1² = 12 · 180° → 30'a kırpılır
		{"eksende, 500 m", 0, 500, 0.031100595298870, 0.992864403},
		{"3 dB kenarı (32,5°)", 32.5, 500, 3.031100595298870, 0.497610964},
		{"hüzme genişliği kadar (65°)", 65, 500, 12.031100595298870, 0.062645509},
		{"arka lob (180°)", 180, 500, 30.0, 0.001},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := at(tc.bearing, tc.distM)
			got := Angular(cell, p, testUTHeightM)

			if math.Abs(got-tc.wantWAng) > 1e-9 {
				t.Errorf("w_ang = %.12f, elle hesap %.12f", got, tc.wantWAng)
			}
			// Ağırlığın dB karşılığı da doğrulanır: log dönüşümü tersine.
			gotAdB := -10 * math.Log10(got)
			if math.Abs(gotAdB-tc.wantAdB) > 1e-6 {
				t.Errorf("A = %.9f dB, elle hesap %.9f dB", gotAdB, tc.wantAdB)
			}
			t.Logf("φ=%.1f° d=%.0f m → A=%.6f dB, w_ang=%.9f", tc.bearing, tc.distM, gotAdB, got)
		})
	}
}

// TestAngular_TiltPeak, eğimin hüzme merkezini aşağı kaydırdığını sınar:
// düşey açı tam tilt kadar olan mesafede zayıflama sıfır, ağırlık 1'dir.
//
// 23,5 / tan(6°) = 223,5876 m.
func TestAngular_TiltPeak(t *testing.T) {
	cell := goldenCell(t)
	d := (testAntHeightM - testUTHeightM) / math.Tan(testTiltDeg*math.Pi/180)

	if math.Abs(d-223.5876) > 1e-3 {
		t.Fatalf("tepe mesafesi %.4f m, elle hesap 223,5876 m", d)
	}
	got := Angular(cell, at(0, d), testUTHeightM)
	if math.Abs(got-1) > 1e-12 {
		t.Errorf("eğim tepesinde w_ang = %.12f, 1 beklenir", got)
	}
}

// TestAngular_Bounds, ağırlığın her yerde (0,1] aralığında kaldığını sınar.
//
// Desen hiçbir yerde sıfırlanmaz: arka lobdaki gerçek konum küçük ama sıfır
// olmayan ağırlık alır, yani kütleden dışlanmaz.
func TestAngular_Bounds(t *testing.T) {
	cell := goldenCell(t)
	minW := math.Inf(1)

	for bearing := -180.0; bearing <= 180.0; bearing += 1 {
		for _, d := range []float64{1, 50, 500, 2500, 5000} {
			w := Angular(cell, at(bearing, d), testUTHeightM)
			if math.IsNaN(w) || w <= 0 || w > 1 {
				t.Fatalf("φ=%.0f° d=%.0f: w_ang = %v — (0,1] dışında", bearing, d, w)
			}
			minW = math.Min(minW, w)
		}
	}
	// Ön-arka bastırma 30 dB → en küçük ağırlık 10⁻³.
	if math.Abs(minW-0.001) > 1e-12 {
		t.Errorf("en küçük ağırlık %.12f, 30 dB bastırmaya karşılık gelen 0.001 beklenir", minW)
	}
}

// TestAngular_SymmetricAroundAxis, desenin hüzme ekseni etrafında simetrik
// olduğunu sınar.
func TestAngular_SymmetricAroundAxis(t *testing.T) {
	cell := goldenCell(t)
	for _, phi := range []float64{5, 20, 32.5, 60, 120} {
		left := Angular(cell, at(cell.AzimuthDeg+phi, 800), testUTHeightM)
		right := Angular(cell, at(cell.AzimuthDeg-phi, 800), testUTHeightM)
		if math.Abs(left-right) > 1e-12 {
			t.Errorf("φ=±%.1f°: %.12f ≠ %.12f", phi, left, right)
		}
	}
}

// TestAngular_MonotonicOffAxis, eksenden uzaklaştıkça ağırlığın azaldığını
// sınar (bastırma sınırına kadar).
func TestAngular_MonotonicOffAxis(t *testing.T) {
	cell := goldenCell(t)
	prev := math.Inf(1)
	for phi := 0.0; phi <= 90; phi += 2.5 {
		w := Angular(cell, at(phi, 1000), testUTHeightM)
		if w > prev+1e-12 {
			t.Fatalf("φ=%.1f°: ağırlık arttı (%.12f > %.12f)", phi, w, prev)
		}
		prev = w
	}
}
