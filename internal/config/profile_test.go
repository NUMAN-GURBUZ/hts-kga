package config

import (
	"math"
	"testing"
)

// TestProfileFor_ADR17Table, ADR-17 tablosundaki sekiz alanın birebir
// karşılığını doğrular. Tablo değişirse bu test kırılmalıdır.
func TestProfileFor_ADR17Table(t *testing.T) {
	tests := []struct {
		morphology Morphology
		areaRadius float64
		isd        float64
		model      PropagationModel
		antHeight  float64
		homeWorkKM [2]float64
		commuteKMH float64
		dMaxKM     float64
	}{
		{MorphologyUrban, 5, 500, ModelUMa, 25, [2]float64{1, 8}, 30, 5},
		{MorphologyRural, 20, 2000, ModelRMa, 45, [2]float64{5, 25}, 70, 10},
	}

	for _, tc := range tests {
		t.Run(string(tc.morphology), func(t *testing.T) {
			p, err := ProfileFor(tc.morphology)
			if err != nil {
				t.Fatalf("ProfileFor(%q) hata döndürdü: %v", tc.morphology, err)
			}
			if p.AreaRadiusKM != tc.areaRadius {
				t.Errorf("AreaRadiusKM = %g, beklenen %g", p.AreaRadiusKM, tc.areaRadius)
			}
			if p.InterSiteDistanceM != tc.isd {
				t.Errorf("InterSiteDistanceM = %g, beklenen %g", p.InterSiteDistanceM, tc.isd)
			}
			if p.PropagationModel != tc.model {
				t.Errorf("PropagationModel = %q, beklenen %q", p.PropagationModel, tc.model)
			}
			if p.AntHeightM != tc.antHeight {
				t.Errorf("AntHeightM = %g, beklenen %g", p.AntHeightM, tc.antHeight)
			}
			if p.AgentHomeWorkMinKM != tc.homeWorkKM[0] || p.AgentHomeWorkMaxKM != tc.homeWorkKM[1] {
				t.Errorf("ev–iş aralığı = %g–%g, beklenen %g–%g",
					p.AgentHomeWorkMinKM, p.AgentHomeWorkMaxKM, tc.homeWorkKM[0], tc.homeWorkKM[1])
			}
			if p.CommuteSpeedKMH != tc.commuteKMH {
				t.Errorf("CommuteSpeedKMH = %g, beklenen %g", p.CommuteSpeedKMH, tc.commuteKMH)
			}
			if p.DMaxKM != tc.dMaxKM {
				t.Errorf("DMaxKM = %g, beklenen %g", p.DMaxKM, tc.dMaxKM)
			}
		})
	}
}

// TestProfileFor_FreqDistributionDirection, ADR-17'nin "kentsel yüksek bant /
// kırsal düşük bant ağırlıklı" kuralını ağırlıklı ortalama üzerinden sınar.
func TestProfileFor_FreqDistributionDirection(t *testing.T) {
	weightedMean := func(bands []FreqBand) float64 {
		var sum float64
		for _, b := range bands {
			sum += float64(b.MHz) * b.Weight
		}
		return sum
	}

	urban, _ := ProfileFor(MorphologyUrban)
	rural, _ := ProfileFor(MorphologyRural)

	uMean := weightedMean(urban.FreqDistribution)
	rMean := weightedMean(rural.FreqDistribution)

	if uMean <= rMean {
		t.Errorf("kentsel ağırlıklı frekans ortalaması kırsaldan büyük olmalı: urban=%g rural=%g",
			uMean, rMean)
	}
}

// TestProfileFor_DefaultsValid, factory çıktısının kendi doğrulamasından
// geçtiğini garanti eder (varsayılanlar tutarlı olmalı).
func TestProfileFor_DefaultsValid(t *testing.T) {
	for _, m := range []Morphology{MorphologyUrban, MorphologyRural} {
		p, err := ProfileFor(m)
		if err != nil {
			t.Fatalf("ProfileFor(%q): %v", m, err)
		}
		if err := p.Validate(); err != nil {
			t.Errorf("%q profili kendi doğrulamasından geçemedi: %v", m, err)
		}
	}
}

// TestProfileFor_UnknownMorphology, bilinmeyen anahtarın fail-fast davrandığını sınar.
func TestProfileFor_UnknownMorphology(t *testing.T) {
	if _, err := ProfileFor(Morphology("suburban")); err == nil {
		t.Fatal("bilinmeyen morfoloji için hata bekleniyordu, nil döndü")
	}
}

// TestProfileFor_ReturnsCopy, factory'nin paylaşılan durum döndürmediğini sınar:
// dönen profil üzerinde yapılan override sonraki çağrıyı etkilememeli.
func TestProfileFor_ReturnsCopy(t *testing.T) {
	p1, _ := ProfileFor(MorphologyUrban)
	p1.AntHeightM = 999
	p1.FreqDistribution[0].Weight = 0.99

	p2, _ := ProfileFor(MorphologyUrban)
	if p2.AntHeightM == 999 {
		t.Error("skaler alan değişikliği sonraki factory çağrısına sızdı")
	}
	if p2.FreqDistribution[0].Weight == 0.99 {
		t.Error("frekans dilimi paylaşılıyor: slice kopyalanmalı")
	}
}

// TestAreaRadiusM, km→m dönüşümünü sınar (ENU hesapları metre cinsindendir).
func TestAreaRadiusM(t *testing.T) {
	p, _ := ProfileFor(MorphologyRural)
	if got := p.AreaRadiusM(); got != 20000 {
		t.Errorf("AreaRadiusM() = %g, beklenen 20000", got)
	}
}

// TestValidateFreqBands, band listesi doğrulama kurallarını sınar.
func TestValidateFreqBands(t *testing.T) {
	tests := []struct {
		name    string
		bands   []FreqBand
		wantErr bool
	}{
		{"geçerli", []FreqBand{{MHz: 900, Weight: 0.4}, {MHz: 1800, Weight: 0.6}}, false},
		{"boş", nil, true},
		{"toplam 1 değil", []FreqBand{{MHz: 900, Weight: 0.4}, {MHz: 1800, Weight: 0.4}}, true},
		{"negatif ağırlık", []FreqBand{{MHz: 900, Weight: -0.2}, {MHz: 1800, Weight: 1.2}}, true},
		{"sıfır frekans", []FreqBand{{MHz: 0, Weight: 1.0}}, true},
		{"kayan nokta toleransı", []FreqBand{
			{MHz: 800, Weight: 0.1}, {MHz: 900, Weight: 0.2}, {MHz: 1800, Weight: 0.7},
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFreqBands(tc.bands)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateFreqBands() hata = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// TestProfileValidate_RejectsInvalid, profil doğrulamasının şema kısıtlarıyla
// (001_schema.sql CHECK'leri) hizalı olduğunu sınar.
func TestProfileValidate_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MorphologyProfile)
	}{
		{"beam_width 0", func(p *MorphologyProfile) { p.BeamWidthDeg = 0 }},
		{"beam_width > 360", func(p *MorphologyProfile) { p.BeamWidthDeg = 361 }},
		{"r_max 0", func(p *MorphologyProfile) { p.RMaxM = 0 }},
		{"r_max negatif", func(p *MorphologyProfile) { p.RMaxM = -1 }},
		{"tilt 90", func(p *MorphologyProfile) { p.TiltDeg = 90 }},
		{"ant_height 0", func(p *MorphologyProfile) { p.AntHeightM = 0 }},
		{"geçersiz model", func(p *MorphologyProfile) { p.PropagationModel = "COST231" }},
		{"alan yarıçapı 0", func(p *MorphologyProfile) { p.AreaRadiusKM = 0 }},
		{"ISD negatif", func(p *MorphologyProfile) { p.InterSiteDistanceM = -100 }},
		{"d_max 0", func(p *MorphologyProfile) { p.DMaxKM = 0 }},
		{"ev–iş aralığı ters", func(p *MorphologyProfile) {
			p.AgentHomeWorkMinKM, p.AgentHomeWorkMaxKM = 8, 1
		}},
		{"commute hızı 0", func(p *MorphologyProfile) { p.CommuteSpeedKMH = 0 }},
		{"bozuk frekans dağılımı", func(p *MorphologyProfile) {
			p.FreqDistribution = []FreqBand{{MHz: 900, Weight: 0.5}}
		}},
		{"NaN yarıçap", func(p *MorphologyProfile) { p.AreaRadiusKM = math.NaN() }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := ProfileFor(MorphologyUrban)
			tc.mutate(&p)
			if err := p.Validate(); err == nil {
				t.Errorf("%s için hata bekleniyordu, nil döndü", tc.name)
			}
		})
	}
}
