package agent

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"

	"github.com/google/uuid"
)

const configsDir = "../../../configs"

// testRunID, testlerde kullanılan sabit koşu kimliği.
var testRunID = uuid.MustParse("00000000-0000-5000-8000-000000000002")

// ─── Test altyapısı: gerçek Sprint 1 envanteri üzerinden şebeke ──────────────

// buildScenario, gerçek senaryo config'ini yükler.
func buildScenario(t *testing.T, file string) *config.Scenario {
	t.Helper()
	scn, err := config.Load(configsDir + "/" + file)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", file, err)
	}
	return scn
}

// buildSelector, Sprint 1 envanterinden gerçek bir best-server seçicisi kurar.
//
// Sahte veri yerine gerçek envanter kullanılır: kapsama oranları, site
// yoğunluğu ve r_max değerleri senaryonun kendisinden gelir.
func buildSelector(t *testing.T, scn *config.Scenario) *radio.Selector {
	t.Helper()

	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("geo.NewProjector: %v", err)
	}
	inv, err := inventory.Build(testRunID, scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}

	// Envanter → radyo katmanı görünümü (sektörler siteye göre gruplanır)
	bySite := make(map[uuid.UUID]*radio.Site, inv.SiteCount())
	order := make([]uuid.UUID, 0, inv.SiteCount())

	model, err := rf.ModelFor(scn.Profile.PropagationModel)
	if err != nil {
		t.Fatalf("rf.ModelFor: %v", err)
	}

	siteENU := make(map[uuid.UUID]geo.Point, inv.SiteCount())
	for _, s := range inv.Layout.Sites {
		siteENU[s.ID] = s.ENU
	}

	for _, c := range inv.Cells {
		site, ok := bySite[c.SiteID]
		if !ok {
			site = &radio.Site{
				Source: radio.Source{
					Key:   radio.SourceKey([16]byte(c.SiteID)),
					ENU:   siteENU[c.SiteID],
					Model: model,
				},
				AntHeightM: c.AntHeightM,
			}
			bySite[c.SiteID] = site
			order = append(order, c.SiteID)
		}
		site.Cells = append(site.Cells, radio.Cell{
			ID:           [16]byte(c.ID),
			AzimuthDeg:   c.Azimuth,
			BeamWidthDeg: c.BeamWidth,
			TiltDeg:      c.TiltDeg,
			EIRPdBm:      c.EIRPdBm,
			FreqMHz:      c.FreqMHz,
			RMaxM:        c.RMaxM,
		})
	}

	sites := make([]radio.Site, 0, len(order))
	for _, id := range order {
		sites = append(sites, *bySite[id])
	}

	net, err := radio.NewNetwork(sites)
	if err != nil {
		t.Fatalf("radio.NewNetwork: %v", err)
	}
	field, err := radio.NewShadowingField(scn.Run.Seed, rf.UTHeightM)
	if err != nil {
		t.Fatalf("radio.NewShadowingField: %v", err)
	}
	sel, err := radio.NewSelector(net, field, scn.Network.RxSensitivityDBm, rf.UTHeightM)
	if err != nil {
		t.Fatalf("radio.NewSelector: %v", err)
	}
	return sel
}

// buildAgents, verilen senaryo için ajan kümesi üretir.
func buildAgents(t *testing.T, scn *config.Scenario, count int) ([]State, *radio.Selector) {
	t.Helper()

	sel := buildSelector(t, scn)
	cfg, err := PlacementConfigFrom(scn, sel)
	if err != nil {
		t.Fatalf("PlacementConfigFrom: %v", err)
	}
	if count > 0 {
		cfg.Count = count
	}

	agents, err := PlaceAgents(cfg)
	if err != nil {
		t.Fatalf("PlaceAgents: %v", err)
	}
	return agents, sel
}

// ─── T-E02-07: yerleşim ──────────────────────────────────────────────────────

// TestPlaceAgents_AllAnchorsCovered, ADR-08/2'nin ana vaadini sınar:
// hiçbir ajan kapsama dışında başlamaz.
func TestPlaceAgents_AllAnchorsCovered(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			scn := buildScenario(t, file)
			agents, sel := buildAgents(t, scn, 200)

			for _, a := range agents {
				if rx := sel.MaxRxDBm(a.Home); rx < sel.RxSensitivityDBm() {
					t.Errorf("ajan %d ev çapası kapsama dışında: %.2f dBm < %.2f dBm",
						a.ID, rx, sel.RxSensitivityDBm())
				}
				if rx := sel.MaxRxDBm(a.Work); rx < sel.RxSensitivityDBm() {
					t.Errorf("ajan %d iş çapası kapsama dışında: %.2f dBm < %.2f dBm",
						a.ID, rx, sel.RxSensitivityDBm())
				}
			}
			t.Logf("%s: %d ajan, tüm çapalar kapsama içinde", file, len(agents))
		})
	}
}

// TestPlaceAgents_HomeWorkDistanceInProfileRange, ADR-17 profil aralığının
// uygulandığını sınar (kentsel 1–8 km, kırsal 5–25 km).
func TestPlaceAgents_HomeWorkDistanceInProfileRange(t *testing.T) {
	tests := []struct {
		file         string
		minKM, maxKM float64
	}{
		{"urban_ta.yaml", 1, 8},
		{"rural_ta.yaml", 5, 25},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			scn := buildScenario(t, tc.file)
			agents, _ := buildAgents(t, scn, 200)

			var sum, min, max float64
			min = math.Inf(1)
			for _, a := range agents {
				d := a.HomeWorkDistanceM()
				if d < tc.minKM*1000-1e-6 || d > tc.maxKM*1000+1e-6 {
					t.Errorf("ajan %d: ev–iş mesafesi %.1f m, aralık [%g, %g] km",
						a.ID, d, tc.minKM, tc.maxKM)
				}
				sum += d
				min = math.Min(min, d)
				max = math.Max(max, d)
			}
			t.Logf("%s: ev–iş mesafesi min %.0f m, ort %.0f m, max %.0f m",
				tc.file, min, sum/float64(len(agents)), max)
		})
	}
}

// TestPlaceAgents_AllInsideArea, tüm çapaların kapsama alanı diski içinde
// kaldığını sınar (ADR-08 sınırlayıcı kutu).
func TestPlaceAgents_AllInsideArea(t *testing.T) {
	scn := buildScenario(t, "rural_ta.yaml")
	agents, _ := buildAgents(t, scn, 200)
	radiusM := scn.Profile.AreaRadiusM()

	for _, a := range agents {
		if d := a.Home.Norm(); d > radiusM {
			t.Errorf("ajan %d ev çapası alan dışında: %.1f m > %.0f m", a.ID, d, radiusM)
		}
		if d := a.Work.Norm(); d > radiusM {
			t.Errorf("ajan %d iş çapası alan dışında: %.1f m > %.0f m", a.ID, d, radiusM)
		}
	}
}

// TestPlaceAgents_Deterministic, K10 gereksinimini sınar.
func TestPlaceAgents_Deterministic(t *testing.T) {
	scn := buildScenario(t, "urban_ta.yaml")

	first, _ := buildAgents(t, scn, 100)
	second, _ := buildAgents(t, scn, 100)

	if len(first) != len(second) {
		t.Fatalf("ajan sayısı deterministik değil: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("ajan[%d] deterministik değil:\n  %+v\n  %+v", i, first[i], second[i])
		}
	}
}

// TestPlaceAgents_SeedChangesPopulation, farklı tohumun farklı popülasyon
// ürettiğini sınar.
func TestPlaceAgents_SeedChangesPopulation(t *testing.T) {
	scn := buildScenario(t, "urban_ta.yaml")
	base, _ := buildAgents(t, scn, 50)

	scn2 := buildScenario(t, "urban_ta.yaml")
	scn2.Run.Seed = 999
	other, _ := buildAgents(t, scn2, 50)

	identical := true
	for i := range base {
		if base[i].Home != other[i].Home {
			identical = false
			break
		}
	}
	if identical {
		t.Error("farklı tohum aynı yerleşimi üretti")
	}
}

// TestPlaceAgents_UniformInDisk, disk örneklemesinin alan bakımından düzgün
// olduğunu sınar.
//
// r = R·√u dönüşümü yapılmasaydı noktalar merkeze yığılırdı: iç yarım
// yarıçaptaki (alanın %25'i) nokta oranı %50 çıkardı.
func TestPlaceAgents_UniformInDisk(t *testing.T) {
	const samples = 200_000
	const radiusM = 5000.0

	rng := newRNG(42, streamAgentPlacement)
	inner := 0
	for i := 0; i < samples; i++ {
		p := uniformInDisk(rng, radiusM)
		if p.Norm() > radiusM {
			t.Fatalf("nokta disk dışında: %.3f m > %.0f m", p.Norm(), radiusM)
		}
		if p.Norm() <= radiusM/2 {
			inner++
		}
	}

	// İç yarım yarıçap alanın tam 1/4'üdür
	ratio := float64(inner) / samples
	if math.Abs(ratio-0.25) > 0.01 {
		t.Errorf("iç yarım yarıçap oranı %.4f, beklenen 0.25 (alan bakımından düzgün dağılım)",
			ratio)
	}
	t.Logf("iç yarım yarıçap oranı: %.4f (kuramsal 0.2500)", ratio)
}

// TestPlaceAgents_UniformInAnnulus, halka örneklemesini sınar.
func TestPlaceAgents_UniformInAnnulus(t *testing.T) {
	const samples = 100_000
	const minM, maxM = 1000.0, 8000.0

	rng := newRNG(42, streamAgentPlacement)
	center := geo.Point{X: 100, Y: -200}

	for i := 0; i < samples; i++ {
		p := uniformInAnnulus(rng, center, minM, maxM)
		d := geo.Distance(center, p)
		if d < minM-1e-6 || d > maxM+1e-6 {
			t.Fatalf("nokta halka dışında: %.3f m, aralık [%g, %g]", d, minM, maxM)
		}
	}
}

// TestPlaceAgents_FailFast, kapsama sağlanamadığında koşunun başlamadığını
// sınar (ADR-08/2 fail-fast).
func TestPlaceAgents_FailFast(t *testing.T) {
	cfg := PlacementConfig{
		Count:           10,
		Seed:            42,
		AreaRadiusM:     5000,
		HomeWorkMinM:    1000,
		HomeWorkMaxM:    8000,
		CommuteSpeedKMH: 30,
		Probe:           deadZoneProbe{},
	}

	_, err := PlaceAgents(cfg)
	if err == nil {
		t.Fatal("kapsama yokken hata bekleniyordu")
	}
	t.Logf("beklenen fail-fast: %v", err)
}

// deadZoneProbe, hiçbir noktada kapsama olmayan sahte sondadır.
type deadZoneProbe struct{}

func (deadZoneProbe) MaxRxDBm(geo.Point) float64 { return math.Inf(-1) }
func (deadZoneProbe) RxSensitivityDBm() float64  { return -110 }

// TestPlacementConfig_Validation, geçersiz girdilerin reddedildiğini sınar.
func TestPlacementConfig_Validation(t *testing.T) {
	valid := PlacementConfig{
		Count: 10, Seed: 42, AreaRadiusM: 5000,
		HomeWorkMinM: 1000, HomeWorkMaxM: 8000,
		CommuteSpeedKMH: 30, Probe: deadZoneProbe{},
	}

	tests := []struct {
		name   string
		mutate func(*PlacementConfig)
	}{
		{"ajan sayısı 0", func(c *PlacementConfig) { c.Count = 0 }},
		{"yarıçap 0", func(c *PlacementConfig) { c.AreaRadiusM = 0 }},
		{"ev-iş aralığı ters", func(c *PlacementConfig) { c.HomeWorkMinM, c.HomeWorkMaxM = 8000, 1000 }},
		{"hız 0", func(c *PlacementConfig) { c.CommuteSpeedKMH = 0 }},
		{"sonda yok", func(c *PlacementConfig) { c.Probe = nil }},
		{"ev-iş alt sınırı alan çapından büyük", func(c *PlacementConfig) { c.HomeWorkMinM = 20000 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			if err := c.validate(); err == nil {
				t.Error("hata bekleniyordu")
			}
		})
	}
}

// TestCommuteMinutes, yolculuk süresi türetmesini sınar.
func TestCommuteMinutes(t *testing.T) {
	tests := []struct {
		distanceM, speedKMH float64
		want                int
	}{
		{30000, 30, 60}, // 30 km @ 30 km/s = 60 dk
		{35000, 70, 30}, // 35 km @ 70 km/s = 30 dk
		{1000, 30, 2},   // 1 km @ 30 km/s = 2 dk
		{1, 70, 1},      // çok kısa → alt sınır
	}
	for _, tc := range tests {
		if got := commuteMinutes(tc.distanceM, tc.speedKMH); got != tc.want {
			t.Errorf("commuteMinutes(%g, %g) = %d, beklenen %d",
				tc.distanceM, tc.speedKMH, got, tc.want)
		}
	}
}

// TestState_Validate, ajan durumu doğrulamasını sınar.
func TestState_Validate(t *testing.T) {
	valid := State{
		ID: 0, Phase: PhaseHome, CommuteMin: 20,
		DepartWorkMin: 480, DepartHomeMin: 1050,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("geçerli durum reddedildi: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{"negatif kimlik", func(s *State) { s.ID = -1 }},
		{"geçersiz evre", func(s *State) { s.Phase = Phase(99) }},
		{"yolculuk süresi 0", func(s *State) { s.CommuteMin = 0 }},
		{"çıkış anı gün dışı", func(s *State) { s.DepartWorkMin = 1500 }},
		{"dönüş anı gün dışı", func(s *State) { s.DepartHomeMin = -1 }},
		{"dönüş varıştan önce", func(s *State) { s.DepartHomeMin = 490 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			tc.mutate(&s)
			if err := s.Validate(); err == nil {
				t.Error("hata bekleniyordu")
			}
		})
	}
}

// TestPhase_String, evre adlarını sınar.
func TestPhase_String(t *testing.T) {
	tests := []struct {
		p    Phase
		want string
	}{
		{PhaseHome, "HOME"},
		{PhaseCommuteToWork, "COMMUTE_TO_WORK"},
		{PhaseWork, "WORK"},
		{PhaseCommuteToHome, "COMMUTE_TO_HOME"},
	}
	for _, tc := range tests {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("Phase(%d).String() = %q, beklenen %q", tc.p, got, tc.want)
		}
	}
	if Phase(99).Valid() {
		t.Error("tanımsız evre geçerli sayıldı")
	}
	if !PhaseCommuteToWork.IsCommuting() || !PhaseCommuteToHome.IsCommuting() {
		t.Error("yolculuk evreleri IsCommuting() true dönmeli")
	}
	if PhaseHome.IsCommuting() || PhaseWork.IsCommuting() {
		t.Error("durağan evreler IsCommuting() false dönmeli")
	}
}
