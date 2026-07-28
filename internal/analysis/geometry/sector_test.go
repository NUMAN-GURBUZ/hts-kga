package geometry

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// goldenSector, altın senaryonun tek sektörüdür: direk başlangıçta, kuzeye
// bakan hüzme, ADR-17 kentsel profil parametreleri.
func goldenSector(t *testing.T) Sector {
	t.Helper()
	s, err := NewSector(geo.Point{}, 0, 65, 5000)
	if err != nil {
		t.Fatalf("NewSector: %v", err)
	}
	return s
}

// at, direğin çevresinde verilen yön ve mesafedeki noktayı döndürür.
func at(bearingDeg, distM float64) geo.Point {
	rad := bearingDeg * math.Pi / 180
	return geo.Point{X: distM * math.Sin(rad), Y: distM * math.Cos(rad)}
}

// TestSector_OffAxisReference, eksenden sapma açısını elle hesaplanmış
// yönlerle karşılaştırır.
func TestSector_OffAxisReference(t *testing.T) {
	s := goldenSector(t)

	cases := []struct {
		bearing float64
		want    float64
	}{
		{0, 0}, {30, 30}, {90, 90}, {180, 180}, {270, -90}, {350, -10},
	}
	for _, tc := range cases {
		if got := s.OffAxisDeg(at(tc.bearing, 1000)); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("yön %.0f°: sapma %.9f, beklenen %.0f", tc.bearing, got, tc.want)
		}
	}
	// Direğin üzerindeki nokta: yön tanımsız, 0 kabul edilir.
	if got := s.OffAxisDeg(geo.Point{}); got != 0 {
		t.Errorf("direk üzerinde sapma %.9f, 0 beklenir", got)
	}
}

// TestSector_ContainsBoundaries, dilim sınırlarını sınar.
//
// Yarı açıklık 65/2 = 32,5°; yarıçap 5000 m. Sınırlar dâhildir.
func TestSector_ContainsBoundaries(t *testing.T) {
	s := goldenSector(t)

	cases := []struct {
		name string
		p    geo.Point
		want bool
	}{
		{"eksende, yarıçap içinde", at(0, 2000), true},
		{"tam açı sınırında", at(32.5, 2000), true},
		{"açı sınırının dışında", at(32.6, 2000), false},
		{"tam yarıçap sınırında", at(0, 5000), true},
		{"yarıçap sınırının dışında", at(0, 5000.001), false},
		{"arka lob", at(180, 1000), false},
		{"direk üzerinde", geo.Point{}, true},
	}
	for _, tc := range cases {
		if got := s.Contains(tc.p); got != tc.want {
			t.Errorf("%s: Contains = %v, beklenen %v", tc.name, got, tc.want)
		}
	}
}

// TestSector_Omnidirectional, 360° hüzmenin tüm yönleri kapsadığını sınar.
func TestSector_Omnidirectional(t *testing.T) {
	s, err := NewSector(geo.Point{}, 0, 360, 1000)
	if err != nil {
		t.Fatalf("NewSector: %v", err)
	}
	for _, b := range []float64{0, 90, 180, 270} {
		if !s.Contains(at(b, 999)) {
			t.Errorf("360° hüzme %.0f° yönünü kapsamalı", b)
		}
	}
	if s.Contains(at(0, 1001)) {
		t.Error("yarıçap dışı kapsanmamalı")
	}
}

// TestSector_BoundingBoxReference, çevreleyen kutuyu elle hesaplanmış
// değerlerle karşılaştırır.
//
// Azimut 0°, yarı açıklık 32,5°, r_max 5000 m için:
//
//	kenar uçları  : (±5000·sin32,5° , 5000·cos32,5°)
//	yay içindeki eksen: kuzey (0, 5000)
//	direğin kendisi   : (0, 0)
func TestSector_BoundingBoxReference(t *testing.T) {
	s := goldenSector(t)
	min, max := s.BoundingBox()

	wantX := 5000 * math.Sin(32.5*math.Pi/180)
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"minX", min.X, -wantX},
		{"maxX", max.X, wantX},
		{"minY", min.Y, 0},
		{"maxY", max.Y, 5000},
	} {
		if math.Abs(c.got-c.want) > 1e-6 {
			t.Errorf("%s = %.6f, beklenen %.6f", c.name, c.got, c.want)
		}
	}

	// Kaba "merkez ± r_max" kutusuna göre kazanç: alan oranı.
	naive := (2 * 5000.0) * (2 * 5000.0)
	tight := (max.X - min.X) * (max.Y - min.Y)
	t.Logf("kutu alanı %.0f m² (kaba kutu %.0f m², oran %.3f)", tight, naive, tight/naive)
	if tight > 0.4*naive {
		t.Errorf("kutu beklenenden geniş: %.0f m²", tight)
	}
}

// TestSector_BoundingBoxContainsSector, kutunun dilimi gerçekten kapsadığını
// sınar: kutu dar olmalı ama hiçbir dilim noktasını dışarıda bırakmamalı.
func TestSector_BoundingBoxContainsSector(t *testing.T) {
	for _, az := range []float64{0, 45, 120, 240, 315, -30} {
		s, err := NewSector(geo.Point{X: 100, Y: -200}, az, 65, 3000)
		if err != nil {
			t.Fatalf("NewSector: %v", err)
		}
		min, max := s.BoundingBox()

		for bearing := az - 32.5; bearing <= az+32.5; bearing += 0.5 {
			for _, d := range []float64{0, 500, 1500, 3000} {
				p := geo.Point{
					X: s.Site.X + d*math.Sin(bearing*math.Pi/180),
					Y: s.Site.Y + d*math.Cos(bearing*math.Pi/180),
				}
				if p.X < min.X-1e-6 || p.X > max.X+1e-6 || p.Y < min.Y-1e-6 || p.Y > max.Y+1e-6 {
					t.Fatalf("azimut %.0f°: dilim noktası %v kutunun dışında [%v, %v]",
						az, p, min, max)
				}
			}
		}
	}
}

// TestNewSector_Validation, bozuk tanımların reddedildiğini sınar.
func TestNewSector_Validation(t *testing.T) {
	bad := []struct {
		name           string
		az, beam, rMax float64
	}{
		{"hüzme 0", 0, 0, 1000},
		{"hüzme > 360", 0, 400, 1000},
		{"r_max 0", 0, 65, 0},
		{"r_max sonsuz", 0, 65, math.Inf(1)},
		{"azimut NaN", math.NaN(), 65, 1000},
	}
	for _, tc := range bad {
		if _, err := NewSector(geo.Point{}, tc.az, tc.beam, tc.rMax); err == nil {
			t.Errorf("%s: hata bekleniyordu", tc.name)
		}
	}
	// Azimut sarmalanır: 370° → 10°
	s, err := NewSector(geo.Point{}, 370, 65, 1000)
	if err != nil {
		t.Fatalf("NewSector: %v", err)
	}
	if math.Abs(s.AzimuthDeg-10) > 1e-9 {
		t.Errorf("azimut sarmalanmadı: %.9f", s.AzimuthDeg)
	}
}
