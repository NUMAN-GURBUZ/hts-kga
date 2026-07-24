package inventory

import (
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// mustSectors, yerleşim + sektör üretimini birlikte çalıştırır.
func mustSectors(t *testing.T, file string) (*config.Scenario, *Layout, []Cell) {
	t.Helper()
	scn, _, layout := mustPlace(t, file)
	cells, err := BuildSectors(layout, scn)
	if err != nil {
		t.Fatalf("BuildSectors(%s): %v", file, err)
	}
	return scn, layout, cells
}

// TestBuildSectors_ThreePerSite, Bölüm C.1 gereksinimini sınar:
// her site tam 3 sektör taşır.
func TestBuildSectors_ThreePerSite(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			_, layout, cells := mustSectors(t, file)

			if want := len(layout.Sites) * 3; len(cells) != want {
				t.Fatalf("hücre sayısı = %d, beklenen %d (site × 3)", len(cells), want)
			}

			perSite := make(map[uuid.UUID]int, len(layout.Sites))
			for _, c := range cells {
				perSite[c.SiteID]++
			}
			if len(perSite) != len(layout.Sites) {
				t.Errorf("sektör üretilen site sayısı = %d, beklenen %d",
					len(perSite), len(layout.Sites))
			}
			for siteID, n := range perSite {
				if n != 3 {
					t.Errorf("site %s: %d sektör, beklenen 3", siteID, n)
				}
			}
			t.Logf("%s: %d site → %d hücre", file, len(layout.Sites), len(cells))
		})
	}
}

// TestBuildSectors_FixedAzimuths, kesin kararı sınar: azimutlar tam olarak
// 0°, 120°, 240° — rastgele sapma yok.
func TestBuildSectors_FixedAzimuths(t *testing.T) {
	_, layout, cells := mustSectors(t, "urban_ta.yaml")

	bySite := make(map[uuid.UUID][]float64, len(layout.Sites))
	for _, c := range cells {
		bySite[c.SiteID] = append(bySite[c.SiteID], c.Azimuth)
	}

	for siteID, azimuths := range bySite {
		if len(azimuths) != 3 {
			t.Fatalf("site %s: %d azimut", siteID, len(azimuths))
		}
		for i, want := range SectorAzimuths {
			if azimuths[i] != want {
				t.Errorf("site %s sektör[%d]: azimut %g, beklenen %g (sapma olmamalı)",
					siteID, i, azimuths[i], want)
			}
		}
	}
}

// TestBuildSectors_AzimuthsEvenlySpaced, üç sektörün 120° aralıkla tam kapsama
// verdiğini sınar (toplam 360°).
func TestBuildSectors_AzimuthsEvenlySpaced(t *testing.T) {
	for i := range SectorAzimuths {
		next := SectorAzimuths[(i+1)%3]
		gap := math.Mod(next-SectorAzimuths[i]+360, 360)
		if gap != 120 {
			t.Errorf("sektör[%d] → sektör[%d] aralığı %g°, beklenen 120°", i, (i+1)%3, gap)
		}
	}
}

// TestBuildSectors_ProfileParameters, ADR-17'ye göre hüzme/tilt/EIRP/anten
// yüksekliği/model değerlerinin profilden geldiğini sınar.
func TestBuildSectors_ProfileParameters(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			scn, _, cells := mustSectors(t, file)
			p := scn.Profile

			for _, c := range cells {
				if c.BeamWidth != p.BeamWidthDeg {
					t.Fatalf("beam_width = %g, profilde %g", c.BeamWidth, p.BeamWidthDeg)
				}
				if c.TiltDeg != p.TiltDeg {
					t.Fatalf("tilt = %g, profilde %g", c.TiltDeg, p.TiltDeg)
				}
				if c.EIRPdBm != p.EIRPdBm {
					t.Fatalf("eirp = %g, profilde %g", c.EIRPdBm, p.EIRPdBm)
				}
				if c.AntHeightM != p.AntHeightM {
					t.Fatalf("ant_height = %g, profilde %g", c.AntHeightM, p.AntHeightM)
				}
				if c.ModelType != p.PropagationModel {
					t.Fatalf("model_type = %q, profilde %q", c.ModelType, p.PropagationModel)
				}
				if c.Morphology != p.Morphology {
					t.Fatalf("morphology = %q, profilde %q", c.Morphology, p.Morphology)
				}
			}
		})
	}
}

// TestBuildSectors_MorphologyDifferentiates, kentsel ve kırsal envanterin
// profil üzerinden gerçekten farklılaştığını sınar (ADR-17).
func TestBuildSectors_MorphologyDifferentiates(t *testing.T) {
	_, _, urban := mustSectors(t, "urban_ta.yaml")
	_, _, rural := mustSectors(t, "rural_ta.yaml")

	u, r := urban[0], rural[0]
	if u.AntHeightM == r.AntHeightM {
		t.Errorf("anten yüksekliği aynı (%g): profil uygulanmamış", u.AntHeightM)
	}
	if u.TiltDeg == r.TiltDeg {
		t.Errorf("tilt aynı (%g): profil uygulanmamış", u.TiltDeg)
	}
	if u.EIRPdBm == r.EIRPdBm {
		t.Errorf("EIRP aynı (%g): profil uygulanmamış", u.EIRPdBm)
	}
	if u.ModelType == r.ModelType {
		t.Errorf("model_type aynı (%q): profil uygulanmamış", u.ModelType)
	}
}

// TestBuildSectors_SectorsShareSiteLocation, aynı sitenin üç sektörünün
// aynı konumda olduğunu sınar (tek direk, üç anten).
func TestBuildSectors_SectorsShareSiteLocation(t *testing.T) {
	_, layout, cells := mustSectors(t, "urban_ta.yaml")

	siteLoc := make(map[uuid.UUID]int, len(layout.Sites))
	for i, s := range layout.Sites {
		siteLoc[s.ID] = i
	}

	for _, c := range cells {
		idx, ok := siteLoc[c.SiteID]
		if !ok {
			t.Fatalf("hücre %s bilinmeyen siteye bağlı: %s", c.ID, c.SiteID)
		}
		site := layout.Sites[idx]
		if c.Location != site.WGS84 || c.ENU != site.ENU {
			t.Errorf("hücre %s konumu sitesinden farklı", c.ID)
		}
	}
}

// TestBuildSectors_UniqueCellIDs, hücre kimliklerinin benzersiz ve
// deterministik olduğunu sınar (ADR-01).
func TestBuildSectors_UniqueCellIDs(t *testing.T) {
	_, layout, cells := mustSectors(t, "rural_ta.yaml")

	seen := make(map[uuid.UUID]bool, len(cells))
	for _, c := range cells {
		if seen[c.ID] {
			t.Errorf("hücre kimliği tekrarlanmış: %s", c.ID)
		}
		if c.ID == uuid.Nil {
			t.Error("boş hücre kimliği üretildi")
		}
		seen[c.ID] = true
	}

	// Aynı site + aynı indis → aynı kimlik
	if got := cellID(layout.Sites[0].ID, 1); got != cells[1].ID {
		t.Errorf("cellID deterministik değil: %s vs %s", got, cells[1].ID)
	}
	// Farklı indis → farklı kimlik
	if cellID(layout.Sites[0].ID, 0) == cellID(layout.Sites[0].ID, 1) {
		t.Error("farklı sektör indisleri aynı kimliği üretti")
	}
}

// TestBuildSectors_Deterministic, K10: aynı tohum → birebir aynı envanter
// (frekans ataması dâhil).
func TestBuildSectors_Deterministic(t *testing.T) {
	scn, _, first := mustSectors(t, "urban_ta.yaml")
	_ = scn

	_, _, second := mustSectors(t, "urban_ta.yaml")

	if len(first) != len(second) {
		t.Fatalf("hücre sayısı deterministik değil: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("hücre[%d] deterministik değil:\n  %+v\n  %+v", i, first[i], second[i])
		}
	}
}

// TestBuildSectors_FreqFromProfileBands, atanan her frekansın profil band
// listesinde bulunduğunu sınar.
func TestBuildSectors_FreqFromProfileBands(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			scn, _, cells := mustSectors(t, file)

			allowed := make(map[int]bool, len(scn.Profile.FreqDistribution))
			for _, b := range scn.Profile.FreqDistribution {
				allowed[b.MHz] = true
			}
			for _, c := range cells {
				if !allowed[c.FreqMHz] {
					t.Fatalf("hücre %s: %d MHz profil dağılımında yok", c.ID, c.FreqMHz)
				}
			}
		})
	}
}

// TestBuildSectors_FreqDistributionMatchesWeights, gerçek envanterdeki band
// dağılımının profil ağırlıklarına yakınsadığını sınar.
func TestBuildSectors_FreqDistributionMatchesWeights(t *testing.T) {
	scn, _, cells := mustSectors(t, "urban_ta.yaml")

	counts := make(map[int]int)
	for _, c := range cells {
		counts[c.FreqMHz]++
	}

	total := float64(len(cells))
	for _, b := range scn.Profile.FreqDistribution {
		got := float64(counts[b.MHz]) / total
		// ~327 örnekte %8 mutlak sapma payı (istatistiksel dalgalanma)
		if math.Abs(got-b.Weight) > 0.08 {
			t.Errorf("%d MHz payı %.3f, beklenen %.3f (±0.08)", b.MHz, got, b.Weight)
		}
		t.Logf("%d MHz: gerçekleşen %.3f / hedef %.3f", b.MHz, got, b.Weight)
	}
}

// TestBuildSectors_UrbanUsesHigherBands, ADR-17'nin band yönü kuralını
// üretilmiş envanter üzerinde sınar.
func TestBuildSectors_UrbanUsesHigherBands(t *testing.T) {
	_, _, urban := mustSectors(t, "urban_ta.yaml")
	_, _, rural := mustSectors(t, "rural_ta.yaml")

	mean := func(cells []Cell) float64 {
		var sum float64
		for _, c := range cells {
			sum += float64(c.FreqMHz)
		}
		return sum / float64(len(cells))
	}

	uMean, rMean := mean(urban), mean(rural)
	if uMean <= rMean {
		t.Errorf("kentsel ortalama frekans %.0f MHz ≤ kırsal %.0f MHz", uMean, rMean)
	}
	t.Logf("ortalama taşıyıcı: kentsel %.0f MHz, kırsal %.0f MHz", uMean, rMean)
}

// TestBuildSectors_InvalidInput, eksik girdilerin reddedildiğini sınar.
func TestBuildSectors_InvalidInput(t *testing.T) {
	scn, _, layout := mustPlace(t, "urban_ta.yaml")

	if _, err := BuildSectors(nil, scn); err == nil {
		t.Error("nil yerleşim için hata bekleniyordu")
	}
	if _, err := BuildSectors(layout, nil); err == nil {
		t.Error("nil senaryo için hata bekleniyordu")
	}
	if _, err := BuildSectors(&Layout{RunID: testRunID}, scn); err == nil {
		t.Error("boş yerleşim için hata bekleniyordu")
	}

	// Bozuk band dağılımı → fail-fast
	broken := *scn
	broken.Profile.FreqDistribution = []config.FreqBand{{MHz: 900, Weight: 0.5}}
	if _, err := BuildSectors(layout, &broken); err == nil {
		t.Error("bozuk frekans dağılımı için hata bekleniyordu")
	}
}

// TestCellValidate, `cells` tablosu CHECK kısıtlarının koddaki karşılığını sınar.
func TestCellValidate(t *testing.T) {
	valid := Cell{
		ID: uuid.New(), RunID: uuid.New(), SiteID: uuid.New(),
		Azimuth: 120, BeamWidth: 65, FreqMHz: 2100, EIRPdBm: 58,
		AntHeightM: 25, TiltDeg: 6, RMaxM: 5000,
		Morphology: config.MorphologyUrban, ModelType: config.ModelUMa,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("geçerli hücre reddedildi: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Cell)
	}{
		{"boş id", func(c *Cell) { c.ID = uuid.Nil }},
		{"boş run_id", func(c *Cell) { c.RunID = uuid.Nil }},
		{"boş site_id", func(c *Cell) { c.SiteID = uuid.Nil }},
		{"azimut 360", func(c *Cell) { c.Azimuth = 360 }},
		{"azimut negatif", func(c *Cell) { c.Azimuth = -1 }},
		{"hüzme 0", func(c *Cell) { c.BeamWidth = 0 }},
		{"hüzme 361", func(c *Cell) { c.BeamWidth = 361 }},
		{"frekans 0", func(c *Cell) { c.FreqMHz = 0 }},
		{"anten yüksekliği 0", func(c *Cell) { c.AntHeightM = 0 }},
		{"r_max 0 (AssignRMax çağrılmamış)", func(c *Cell) { c.RMaxM = 0 }},
		{"geçersiz morfoloji", func(c *Cell) { c.Morphology = "suburban" }},
		{"geçersiz model", func(c *Cell) { c.ModelType = "COST231" }},
		{"geçersiz enlem", func(c *Cell) { c.Location.Lat = 91 }},
		{"geçersiz boylam", func(c *Cell) { c.Location.Lon = -181 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("%s: hata bekleniyordu", tc.name)
			}
		})
	}
}

// TestBandSelector_InverseCDF, ağırlıklı seçimin sınır davranışını sınar.
func TestBandSelector_InverseCDF(t *testing.T) {
	bands := []config.FreqBand{
		{MHz: 800, Weight: 0.5},
		{MHz: 900, Weight: 0.3},
		{MHz: 1800, Weight: 0.2},
	}
	s, err := newBandSelector(bands)
	if err != nil {
		t.Fatalf("newBandSelector: %v", err)
	}

	tests := []struct {
		u    float64
		want int
	}{
		{0.0, 800}, {0.49, 800}, {0.5, 800},
		{0.51, 900}, {0.79, 900}, {0.8, 900},
		{0.81, 1800}, {0.999, 1800},
	}
	for _, tc := range tests {
		if got := s.pick(tc.u); got != tc.want {
			t.Errorf("pick(%g) = %d, beklenen %d", tc.u, got, tc.want)
		}
	}
}

// TestBandSelector_EmpiricalDistribution, çok sayıda çekimde ampirik dağılımın
// ağırlıklara yakınsadığını sınar.
func TestBandSelector_EmpiricalDistribution(t *testing.T) {
	bands := []config.FreqBand{
		{MHz: 700, Weight: 0.15},
		{MHz: 1800, Weight: 0.55},
		{MHz: 2600, Weight: 0.30},
	}
	s, err := newBandSelector(bands)
	if err != nil {
		t.Fatalf("newBandSelector: %v", err)
	}

	const n = 200_000
	rng := newRNG(42, streamFrequency)
	counts := make(map[int]int, len(bands))
	for i := 0; i < n; i++ {
		counts[s.pick(rng.Float64())]++
	}

	for _, b := range bands {
		got := float64(counts[b.MHz]) / n
		if math.Abs(got-b.Weight) > 0.01 {
			t.Errorf("%d MHz payı %.4f, beklenen %.4f (±0.01)", b.MHz, got, b.Weight)
		}
	}
}

// TestBandSelector_RejectsInvalid, bozuk dağılımın reddedildiğini sınar.
func TestBandSelector_RejectsInvalid(t *testing.T) {
	for _, bands := range [][]config.FreqBand{
		nil,
		{{MHz: 900, Weight: 0.5}},
		{{MHz: 0, Weight: 1.0}},
	} {
		if _, err := newBandSelector(bands); err == nil {
			t.Errorf("%v için hata bekleniyordu", bands)
		}
	}
}

// TestBandSelector_CopiesInput, seçicinin çağıranın dilimini paylaşmadığını sınar.
func TestBandSelector_CopiesInput(t *testing.T) {
	bands := []config.FreqBand{{MHz: 800, Weight: 0.5}, {MHz: 900, Weight: 0.5}}
	s, err := newBandSelector(bands)
	if err != nil {
		t.Fatalf("newBandSelector: %v", err)
	}

	bands[0].MHz = 9999
	if got := s.pick(0.1); got != 800 {
		t.Errorf("girdi dilimi paylaşılıyor: pick(0.1) = %d", got)
	}
}
