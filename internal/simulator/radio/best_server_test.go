package radio

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Senaryo parametreleri (configs/urban_ta.yaml + ADR-17 kentsel profil).
const (
	testRxSensitivityDBm = -110.0
	testEIRPdBm          = 58.0
	testBeamWidthDeg     = 65.0
	testTiltDeg          = 6.0
	testFreqMHz          = 2100
)

// testSectorAzimuths, T-E02-04 kararı: sabit 0 / 120 / 240.
var testSectorAzimuths = [3]float64{0, 120, 240}

// buildSite, verilen konumda üç sektörlü bir test sitesi kurar.
func buildSite(t *testing.T, key uint64, enu geo.Point, model config.PropagationModel,
	antHeightM, rMaxM float64) Site {
	t.Helper()

	m, err := ModelFor(model)
	if err != nil {
		t.Fatalf("ModelFor(%q): %v", model, err)
	}

	cells := make([]Cell, len(testSectorAzimuths))
	for i, az := range testSectorAzimuths {
		id := [16]byte{}
		id[0] = byte(key)
		id[1] = byte(i)
		cells[i] = Cell{
			ID:           id,
			AzimuthDeg:   az,
			BeamWidthDeg: testBeamWidthDeg,
			TiltDeg:      testTiltDeg,
			EIRPdBm:      testEIRPdBm,
			FreqMHz:      testFreqMHz,
			RMaxM:        rMaxM,
		}
	}
	return Site{
		Source:     Source{Key: key, ENU: enu, Model: m},
		AntHeightM: antHeightM,
		Cells:      cells,
	}
}

// buildSelector, tek siteli bir seçici kurar (altın senaryo yapısı).
func buildSelector(t *testing.T, sites ...Site) *Selector {
	t.Helper()

	net, err := NewNetwork(sites)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	field, err := NewShadowingField(testSeed, UTHeightM)
	if err != nil {
		t.Fatalf("NewShadowingField: %v", err)
	}
	sel, err := NewSelector(net, field, testRxSensitivityDBm, UTHeightM)
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	return sel
}

// TestNewNetwork_Validation, bozuk şebeke tanımlarının reddedildiğini sınar.
func TestNewNetwork_Validation(t *testing.T) {
	uma, _ := ModelFor(config.ModelUMa)
	valid := buildSite(t, 1, geo.Point{}, config.ModelUMa, urbanHBSm, 5000)

	tests := []struct {
		name  string
		sites []Site
	}{
		{"boş liste", nil},
		{"sektörsüz site", []Site{{Source: Source{Key: 1, Model: uma}, AntHeightM: 25}}},
		{"modelsiz site", []Site{{Source: Source{Key: 1}, AntHeightM: 25, Cells: valid.Cells}}},
		{"anten yüksekliği 0", []Site{{Source: valid.Source, AntHeightM: 0, Cells: valid.Cells}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewNetwork(tc.sites); err == nil {
				t.Error("hata bekleniyordu")
			}
		})
	}

	// Bozuk hücre alanları
	for _, mutate := range []func(*Cell){
		func(c *Cell) { c.RMaxM = 0 },
		func(c *Cell) { c.BeamWidthDeg = 0 },
		func(c *Cell) { c.FreqMHz = 0 },
	} {
		s := buildSite(t, 1, geo.Point{}, config.ModelUMa, urbanHBSm, 5000)
		mutate(&s.Cells[1])
		if _, err := NewNetwork([]Site{s}); err == nil {
			t.Error("bozuk hücre için hata bekleniyordu")
		}
	}
}

// TestNewSelector_Validation, seçici kurulumunu sınar.
func TestNewSelector_Validation(t *testing.T) {
	net, err := NewNetwork([]Site{buildSite(t, 1, geo.Point{}, config.ModelUMa, urbanHBSm, 5000)})
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	field, _ := NewShadowingField(testSeed, UTHeightM)

	if _, err := NewSelector(nil, field, testRxSensitivityDBm, UTHeightM); err == nil {
		t.Error("nil şebeke için hata bekleniyordu")
	}
	if _, err := NewSelector(net, nil, testRxSensitivityDBm, UTHeightM); err == nil {
		t.Error("nil alan için hata bekleniyordu")
	}
	if _, err := NewSelector(net, field, 110, UTHeightM); err == nil {
		t.Error("pozitif rx duyarlılığı için hata bekleniyordu")
	}
	if _, err := NewSelector(net, field, testRxSensitivityDBm, 0); err == nil {
		t.Error("h_UT = 0 için hata bekleniyordu")
	}
}

// TestSelect_SectorChosenByBearing, T-E02-12'nin ana gerekçesini sınar:
// anten deseni sayesinde telefon **yönüne bakan** sektörü seçer.
//
// Anten deseni olmasaydı üç sektörün gücü eşit olur, seçim keyfî kalırdı.
func TestSelect_SectorChosenByBearing(t *testing.T) {
	site := buildSite(t, 1, geo.Point{}, config.ModelUMa, urbanHBSm, 5000)
	sel := buildSelector(t, site)

	const d = 400.0
	tests := []struct {
		name        string
		bearingDeg  float64
		wantSectorI int
	}{
		{"kuzey → sektör 0°", 0, 0},
		{"doğu-güneydoğu → sektör 120°", 120, 1},
		{"batı-güneybatı → sektör 240°", 240, 2},
		{"kuzeydoğu (0° tarafı)", 40, 0},
		{"güney (120/240 sınırı yakını)", 170, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rad := tc.bearingDeg * math.Pi / 180
			p := geo.Point{X: d * math.Sin(rad), Y: d * math.Cos(rad)}

			got := sel.Select(p)
			if !got.Covered {
				t.Fatalf("%.0f m mesafede kapsama bekleniyordu (rx=%.2f dBm)", d, got.RxDBm)
			}
			if int(got.CellID[1]) != tc.wantSectorI {
				t.Errorf("yön %.0f°: sektör %d seçildi, beklenen %d (rx=%.2f dBm)",
					tc.bearingDeg, got.CellID[1], tc.wantSectorI, got.RxDBm)
			}
		})
	}
}

// TestSelect_CoverageDecision, ADR-08/3 kapsama kararını sınar.
func TestSelect_CoverageDecision(t *testing.T) {
	site := buildSite(t, 1, geo.Point{}, config.ModelUMa, urbanHBSm, 5000)
	sel := buildSelector(t, site)

	t.Run("yakın mesafe kapsama içinde", func(t *testing.T) {
		got := sel.Select(geo.Point{X: 100, Y: 0})
		if !got.Covered {
			t.Errorf("100 m'de kapsama bekleniyordu, rx = %.2f dBm", got.RxDBm)
		}
		if got.RxDBm < sel.RxSensitivityDBm() {
			t.Errorf("covered=true ama rx (%.2f) eşiğin (%.2f) altında",
				got.RxDBm, sel.RxSensitivityDBm())
		}
	})

	t.Run("r_max dışında aday yok", func(t *testing.T) {
		got := sel.Select(geo.Point{X: 50000, Y: 0})
		if got.Covered {
			t.Error("50 km'de kapsama olmamalı")
		}
		if !math.IsInf(got.RxDBm, -1) {
			t.Errorf("aday yokken rx −Inf olmalı, bulunan %v", got.RxDBm)
		}
	})

	t.Run("kapsama kararı eşikle tutarlı", func(t *testing.T) {
		for _, d := range []float64{50, 200, 1000, 3000, 4900} {
			got := sel.Select(geo.Point{X: d, Y: 0})
			want := got.RxDBm >= sel.RxSensitivityDBm()
			if got.Covered != want {
				t.Errorf("d=%.0f m: covered=%v ama rx=%.2f eşik=%.2f",
					d, got.Covered, got.RxDBm, sel.RxSensitivityDBm())
			}
		}
	})
}

// TestSelect_StrongestSiteWins, çok siteli şebekede en güçlü sitenin
// seçildiğini sınar.
func TestSelect_StrongestSiteWins(t *testing.T) {
	near := buildSite(t, 1, geo.Point{X: 0, Y: 0}, config.ModelUMa, urbanHBSm, 5000)
	far := buildSite(t, 2, geo.Point{X: 3000, Y: 0}, config.ModelUMa, urbanHBSm, 5000)
	sel := buildSelector(t, near, far)

	// Yakın sitenin hemen yanı
	if got := sel.Select(geo.Point{X: 100, Y: 0}); got.SiteKey != 1 {
		t.Errorf("yakın siteye bağlanmalıydı, seçilen site %d", got.SiteKey)
	}
	// Uzak sitenin hemen yanı
	if got := sel.Select(geo.Point{X: 2900, Y: 0}); got.SiteKey != 2 {
		t.Errorf("uzak siteye bağlanmalıydı, seçilen site %d", got.SiteKey)
	}
}

// TestSelect_Deterministic, K10 gereksinimini sınar.
func TestSelect_Deterministic(t *testing.T) {
	site := buildSite(t, 42, geo.Point{X: 500, Y: -500}, config.ModelRMa, ruralHBSm, 20000)
	sel := buildSelector(t, site)

	p := geo.Point{X: 1234.5, Y: 678.9}
	first := sel.Select(p)
	for i := 0; i < 100; i++ {
		if got := sel.Select(p); got != first {
			t.Fatalf("deterministik değil: %+v vs %+v", first, got)
		}
	}
}

// TestSelect_PrefilterMatchesBruteForce, ön filtrenin sonucu değiştirmediğini
// kaba kuvvet taramasıyla doğrular.
func TestSelect_PrefilterMatchesBruteForce(t *testing.T) {
	// Kapsama yarıçapları farklı siteler: ön filtre gerçekten devreye girsin
	sites := []Site{
		buildSite(t, 1, geo.Point{X: 0, Y: 0}, config.ModelUMa, urbanHBSm, 1000),
		buildSite(t, 2, geo.Point{X: 2000, Y: 0}, config.ModelUMa, urbanHBSm, 3000),
		buildSite(t, 3, geo.Point{X: 0, Y: 2500}, config.ModelUMa, urbanHBSm, 800),
	}
	sel := buildSelector(t, sites...)

	// Ön filtresiz referans: tüm siteleri tara
	brute := func(p geo.Point) Serving {
		best := Serving{RxDBm: math.Inf(-1)}
		field, _ := NewShadowingField(testSeed, UTHeightM)
		for i := range sites {
			s := sites[i]
			dx, dy := p.X-s.ENU.X, p.Y-s.ENU.Y
			d2D := math.Hypot(dx, dy)
			env := field.EnvironmentAt(s.Source, p)
			for _, c := range s.Cells {
				if d2D > c.RMaxM {
					continue
				}
				link := Link{D2DM: d2D, HBSm: s.AntHeightM, HUTm: UTHeightM,
					FreqMHz: c.FreqMHz, LOS: env.LOS}
				rx := c.EIRPdBm - s.Model.PathLossDB(link) -
					AntennaAttenuationDB(BearingDeg(dx, dy)-c.AzimuthDeg,
						ElevationDeg(s.AntHeightM-UTHeightM, d2D), c.BeamWidthDeg, c.TiltDeg) +
					env.ShadowingDB
				if rx > best.RxDBm {
					best = Serving{CellID: c.ID, SiteKey: s.Key, RxDBm: rx, DistanceM: d2D, LOS: env.LOS}
				}
			}
		}
		best.Covered = best.RxDBm >= testRxSensitivityDBm
		return best
	}

	for x := -3000.0; x <= 4000; x += 250 {
		for y := -3000.0; y <= 4000; y += 250 {
			p := geo.Point{X: x, Y: y}
			got, want := sel.Select(p), brute(p)
			if got != want {
				t.Fatalf("nokta %+v: ön filtreli %+v, kaba kuvvet %+v", p, got, want)
			}
		}
	}
}

// TestNetwork_Counts, sayaçları sınar.
func TestNetwork_Counts(t *testing.T) {
	net, err := NewNetwork([]Site{
		buildSite(t, 1, geo.Point{}, config.ModelUMa, urbanHBSm, 5000),
		buildSite(t, 2, geo.Point{X: 900}, config.ModelUMa, urbanHBSm, 5000),
	})
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	if got := net.SiteCount(); got != 2 {
		t.Errorf("SiteCount() = %d, beklenen 2", got)
	}
	if got := net.CellCount(); got != 6 {
		t.Errorf("CellCount() = %d, beklenen 6", got)
	}
}

// TestSelector_Helpers, kolaylık sarmalayıcılarını sınar.
func TestSelector_Helpers(t *testing.T) {
	site := buildSite(t, 1, geo.Point{}, config.ModelUMa, urbanHBSm, 5000)
	sel := buildSelector(t, site)
	p := geo.Point{X: 300, Y: 200}

	full := sel.Select(p)
	if got := sel.MaxRxDBm(p); got != full.RxDBm {
		t.Errorf("MaxRxDBm() = %v, Select().RxDBm = %v", got, full.RxDBm)
	}
	if got := sel.IsCovered(p); got != full.Covered {
		t.Errorf("IsCovered() = %v, Select().Covered = %v", got, full.Covered)
	}
}

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

// ─── Başarım ─────────────────────────────────────────────────────────────────

// BenchmarkSelect, sıcak yolun tick başına maliyetini ölçer.
//
// Gerçek şebeke ölçeği: 109 site × 3 sektör (Sprint 1 çıktısı).
// Hedef: 0 allocs/op.
func BenchmarkSelect(b *testing.B) {
	const siteCount = 109

	sites := make([]Site, siteCount)
	m, _ := ModelFor(config.ModelUMa)
	for i := range sites {
		angle := float64(i) * 2 * math.Pi / siteCount
		radius := 400 + float64(i%10)*450
		cells := make([]Cell, 3)
		for j, az := range testSectorAzimuths {
			id := [16]byte{byte(i), byte(j)}
			cells[j] = Cell{
				ID: id, AzimuthDeg: az, BeamWidthDeg: testBeamWidthDeg,
				TiltDeg: testTiltDeg, EIRPdBm: testEIRPdBm,
				FreqMHz: testFreqMHz, RMaxM: 5000,
			}
		}
		sites[i] = Site{
			Source: Source{
				Key:   uint64(i + 1),
				ENU:   geo.Point{X: radius * math.Cos(angle), Y: radius * math.Sin(angle)},
				Model: m,
			},
			AntHeightM: urbanHBSm,
			Cells:      cells,
		}
	}

	net, err := NewNetwork(sites)
	if err != nil {
		b.Fatalf("NewNetwork: %v", err)
	}
	field, _ := NewShadowingField(testSeed, UTHeightM)
	sel, err := NewSelector(net, field, testRxSensitivityDBm, UTHeightM)
	if err != nil {
		b.Fatalf("NewSelector: %v", err)
	}

	p := geo.Point{X: 100, Y: 100}
	b.ReportAllocs()
	b.ResetTimer()

	var sink Serving
	for i := 0; i < b.N; i++ {
		p.X += 0.01
		sink = sel.Select(p)
	}
	_ = sink
}
