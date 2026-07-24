package geo

import (
	"math"
	"testing"
)

// TestHaversine_KnownDistances, Haversine'i **bilinen kapalı-form değerlerle**
// çapalar. Bu test, enu_test.go'nun Haversine'i referans olarak kullanmasını
// meşru kılar: referansın kendisi burada bağımsız olarak doğrulanmıştır.
func TestHaversine_KnownDistances(t *testing.T) {
	tests := []struct {
		name string
		a, b WGS84
		want float64
		tol  float64
	}{
		{
			// Meridyen boyunca 1° = R · (π/180)
			name: "ekvatorda 1° enlem",
			a:    WGS84{Lat: 0, Lon: 0},
			b:    WGS84{Lat: 1, Lon: 0},
			want: EarthRadiusM * math.Pi / 180,
			tol:  1e-6,
		},
		{
			// Ekvatorda boylam çemberi meridyenle aynı yarıçapta
			name: "ekvatorda 1° boylam",
			a:    WGS84{Lat: 0, Lon: 0},
			b:    WGS84{Lat: 0, Lon: 1},
			want: EarthRadiusM * math.Pi / 180,
			tol:  1e-6,
		},
		{
			name: "ekvator → kuzey kutbu (çeyrek çember)",
			a:    WGS84{Lat: 0, Lon: 0},
			b:    WGS84{Lat: 90, Lon: 0},
			want: EarthRadiusM * math.Pi / 2,
			tol:  1e-6,
		},
		{
			name: "kutuptan kutba (yarım çember)",
			a:    WGS84{Lat: -90, Lon: 0},
			b:    WGS84{Lat: 90, Lon: 0},
			want: EarthRadiusM * math.Pi,
			tol:  1e-6,
		},
		{
			name: "antipodal — ekvator boyunca 180°",
			a:    WGS84{Lat: 0, Lon: 0},
			b:    WGS84{Lat: 0, Lon: 180},
			want: EarthRadiusM * math.Pi,
			tol:  1e-6,
		},
		{
			// 60°N enleminde boylam çemberinin yarıçapı R·cos(60°) = R/2.
			// Küçük açılarda büyük daire mesafesi ≈ paralel yayı.
			name: "60°N'de 1° boylam ≈ ekvatorun yarısı",
			a:    WGS84{Lat: 60, Lon: 0},
			b:    WGS84{Lat: 60, Lon: 1},
			want: EarthRadiusM * math.Pi / 180 * 0.5,
			tol:  2.0, // paralel yayı ↔ büyük daire farkı (~metre mertebesi)
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Haversine(tc.a, tc.b)
			if math.Abs(got-tc.want) > tc.tol {
				t.Errorf("Haversine() = %.6f m, beklenen %.6f m (tolerans %g)",
					got, tc.want, tc.tol)
			}
		})
	}
}

// TestHaversine_Identity, aynı noktanın mesafesinin tam sıfır olduğunu sınar.
func TestHaversine_Identity(t *testing.T) {
	points := []WGS84{
		{Lat: 0, Lon: 0},
		{Lat: 38.6748, Lon: 39.2225}, // senaryo origin'i
		{Lat: 90, Lon: 0},
		{Lat: -33.9, Lon: 151.2},
	}
	for _, p := range points {
		if got := Haversine(p, p); got != 0 {
			t.Errorf("Haversine(%+v, %+v) = %g, beklenen 0", p, p, got)
		}
	}
}

// TestHaversine_AntipodalNoNaN, antipodal noktalarda kırpma korumasının
// çalıştığını sınar: Sqrt(h) > 1 olursa Asin NaN döndürürdü.
func TestHaversine_AntipodalNoNaN(t *testing.T) {
	pairs := [][2]WGS84{
		{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 180}},
		{{Lat: 45, Lon: 90}, {Lat: -45, Lon: -90}},
		{{Lat: 38.6748, Lon: 39.2225}, {Lat: -38.6748, Lon: -140.7775}},
		{{Lat: -90, Lon: 0}, {Lat: 90, Lon: 0}},
	}
	for _, pr := range pairs {
		got := Haversine(pr[0], pr[1])
		if math.IsNaN(got) {
			t.Errorf("Haversine(%+v, %+v) = NaN", pr[0], pr[1])
		}
		if math.Abs(got-EarthRadiusM*math.Pi) > 1e-6 {
			t.Errorf("antipodal mesafe %.6f m, beklenen %.6f m",
				got, EarthRadiusM*math.Pi)
		}
	}
}

// TestHaversine_MatchesENUAtShortRange, iki mesafe hesabının kısa mesafede
// birbirine yakınsadığını sınar — ADR-07'nin düzlemsel yaklaşım gerekçesi.
func TestHaversine_MatchesENUAtShortRange(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)
	origin := WGS84{Lat: testOriginLat, Lon: testOriginLon}

	// 1 km'de iki yöntem arasındaki fark %1'i aşmamalı
	for _, az := range []float64{0, 45, 90, 135, 180, 225, 270, 315} {
		sin, cos := math.Sincos(az * math.Pi / 180)
		pt := Point{X: 1000 * sin, Y: 1000 * cos}

		planar := pt.Norm()
		spherical := Haversine(origin, p.Inverse(pt))

		if rel := math.Abs(spherical/planar - 1); rel > 0.01 {
			t.Errorf("azimut %.0f°: küresel %.2f m vs düzlemsel %.2f m (%%%.2f fark)",
				az, spherical, planar, rel*100)
		}
	}
}

// TestEarthRadiusConstant, sabitin beklenen IUGG değerinde kaldığını sınar.
// Değişirse tüm mesafe testleri kayar; bilinçli olmalı.
func TestEarthRadiusConstant(t *testing.T) {
	if EarthRadiusM != 6_371_008.8 {
		t.Errorf("EarthRadiusM = %g, beklenen 6371008.8 (IUGG ortalama yarıçap)",
			EarthRadiusM)
	}
}
