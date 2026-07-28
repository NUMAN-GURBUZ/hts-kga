package rf

import (
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// urbanSpec, ADR-17 kentsel profilinin link budget girdisidir
// (configs/urban_ta.yaml).
func urbanSpec() RangeSpec {
	return RangeSpec{
		EIRPdBm:          58,
		RxSensitivityDBm: -110,
		HBSm:             25,
		HUTm:             UTHeightM,
		FreqMHz:          2100,
		BeamWidthDeg:     65,
		TiltDeg:          6,
	}
}

// ruralSpec, ADR-17 kırsal profilinin link budget girdisidir.
func ruralSpec() RangeSpec {
	return RangeSpec{
		EIRPdBm:          62,
		RxSensitivityDBm: -110,
		HBSm:             45,
		HUTm:             UTHeightM,
		FreqMHz:          900,
		BeamWidthDeg:     65,
		TiltDeg:          3,
	}
}

func mustModel(t *testing.T, name config.PropagationModel) PathLossModel {
	t.Helper()
	m, err := ModelFor(name)
	if err != nil {
		t.Fatalf("ModelFor(%q): %v", name, err)
	}
	return m
}

// TestMixedPathLoss_IsWeightedMixture, karışımın tanımını doğrular:
// ağırlıklar p_LOS ve 1−p_LOS, sonuç iki uç arasında.
func TestMixedPathLoss_IsWeightedMixture(t *testing.T) {
	m := mustModel(t, config.ModelUMa)

	for _, d := range []float64{50, 500, 2000, 6000} {
		link := Link{D2DM: d, HBSm: 25, HUTm: UTHeightM, FreqMHz: 2100}

		pLOS := m.LOSProbability(d, UTHeightM)
		losLink, nlosLink := link, link
		losLink.LOS, nlosLink.LOS = true, false
		los, nlos := m.PathLossDB(losLink), m.PathLossDB(nlosLink)

		want := pLOS*los + (1-pLOS)*nlos
		got := MixedPathLossDB(m, link, UTHeightM)

		if math.Abs(got-want) > 1e-12 {
			t.Errorf("d=%.0f: karışım %.9f, elle %.9f", d, got, want)
		}
		if got < los-1e-9 || got > nlos+1e-9 {
			t.Errorf("d=%.0f: karışım (%.3f) LOS (%.3f) — NLOS (%.3f) arasında değil",
				d, got, los, nlos)
		}
	}
}

// TestBeamPeakDistance_Reference, düşey desen tepesini elle hesapla karşılaştırır.
//
//	d = (h_BS − h_UT) / tan(tilt) = 23,5 / tan(6°) = 223,5876 m
func TestBeamPeakDistance_Reference(t *testing.T) {
	if got := urbanSpec().BeamPeakDistanceM(); math.Abs(got-223.5876) > 1e-3 {
		t.Errorf("kentsel tepe mesafesi %.4f m, elle hesap 223,5876 m", got)
	}
	// 43,5 / tan(3°) = 829,9... m
	want := 43.5 / math.Tan(3*math.Pi/180)
	if got := ruralSpec().BeamPeakDistanceM(); math.Abs(got-want) > 1e-6 {
		t.Errorf("kırsal tepe mesafesi %.4f m, beklenen %.4f m", got, want)
	}
	// Eğim yoksa tepe tanımsızdır; sıfır döner ve arama 1 m'den başlar.
	s := urbanSpec()
	s.TiltDeg = 0
	if got := s.BeamPeakDistanceM(); got != 0 {
		t.Errorf("eğimsiz tepe mesafesi %.4f, 0 beklenir", got)
	}
	// Tepede düşey zayıflama sıfırdır.
	peak := urbanSpec().BeamPeakDistanceM()
	if a := AntennaAttenuationDB(0, ElevationDeg(23.5, peak), 65, 6); math.Abs(a) > 1e-9 {
		t.Errorf("tepede zayıflama %.9f dB, 0 beklenir", a)
	}
}

// TestCoverageRange_MatchesDefinition, çözümün tanımını doğrular: bulunan
// mesafede alınan güç tam olarak alıcı duyarlılığına eşittir.
//
// Testin referansı sabit bir sayı değil, denklemin kendisidir — model
// parametreleri değişse bile geçerli kalır.
func TestCoverageRange_MatchesDefinition(t *testing.T) {
	cases := []struct {
		name  string
		model config.PropagationModel
		spec  RangeSpec
	}{
		{"kentsel UMa 2100", config.ModelUMa, urbanSpec()},
		{"kırsal RMa 900", config.ModelRMa, ruralSpec()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mustModel(t, tc.model)
			r, err := CoverageRangeM(m, tc.spec)
			if err != nil {
				t.Fatalf("CoverageRangeM: %v", err)
			}

			got := tc.spec.ReceivedPowerDBm(m, r)
			if math.Abs(got-tc.spec.RxSensitivityDBm) > 1e-6 {
				t.Errorf("r_max = %.3f m'de alınan güç %.9f dBm, duyarlılık %.1f dBm",
					r, got, tc.spec.RxSensitivityDBm)
			}
			// Sınırın hemen içi/dışı doğru tarafta olmalı.
			if tc.spec.ReceivedPowerDBm(m, r*0.99) <= tc.spec.RxSensitivityDBm {
				t.Error("sınırın içinde sinyal duyarlılığın altında")
			}
			if tc.spec.ReceivedPowerDBm(m, r*1.01) >= tc.spec.RxSensitivityDBm {
				t.Error("sınırın dışında sinyal duyarlılığın üstünde")
			}
			t.Logf("%s → r_max = %.1f m", tc.name, r)
		})
	}
}

// TestCoverageRange_Deterministic, sabit adımlı ikili aramanın aynı girdide
// bit düzeyinde aynı sonucu verdiğini sınar (K10).
func TestCoverageRange_Deterministic(t *testing.T) {
	m := mustModel(t, config.ModelUMa)
	first, err := CoverageRangeM(m, urbanSpec())
	if err != nil {
		t.Fatalf("CoverageRangeM: %v", err)
	}
	for i := 0; i < 100; i++ {
		got, err := CoverageRangeM(m, urbanSpec())
		if err != nil {
			t.Fatalf("CoverageRangeM: %v", err)
		}
		if got != first {
			t.Fatalf("%d. çağrı %.17g döndü, ilk çağrı %.17g", i, got, first)
		}
	}
}

// TestCoverageRange_MonotonicInPower, EIRP arttıkça erişimin arttığını sınar.
func TestCoverageRange_MonotonicInPower(t *testing.T) {
	m := mustModel(t, config.ModelUMa)
	prev := 0.0
	for _, eirp := range []float64{40, 46, 52, 58, 64} {
		s := urbanSpec()
		s.EIRPdBm = eirp
		got, err := CoverageRangeM(m, s)
		if err != nil {
			t.Fatalf("EIRP %.0f: %v", eirp, err)
		}
		if got <= prev {
			t.Errorf("EIRP %.0f dBm → %.1f m, öncekinden (%.1f m) büyük olmalı", eirp, got, prev)
		}
		prev = got
	}
}

// TestCoverageRange_MonotonicInFrequency, yüksek bandın daha kısa eriştiğini
// sınar — serbest uzay bağıntısının doğrudan sonucu.
func TestCoverageRange_MonotonicInFrequency(t *testing.T) {
	m := mustModel(t, config.ModelUMa)
	prev := math.Inf(1)
	for _, f := range []int{800, 900, 1800, 2100, 2600} {
		s := urbanSpec()
		s.FreqMHz = f
		got, err := CoverageRangeM(m, s)
		if err != nil {
			t.Fatalf("%d MHz: %v", f, err)
		}
		if got >= prev {
			t.Errorf("%d MHz → %.1f m, öncekinden (%.1f m) küçük olmalı", f, got, prev)
		}
		prev = got
	}
}

// TestCoverageRange_RejectsInvalid, geçersiz girdileri ve fiziksel olmayan
// yapılandırmaları kapsar.
func TestCoverageRange_RejectsInvalid(t *testing.T) {
	m := mustModel(t, config.ModelUMa)

	if _, err := CoverageRangeM(nil, urbanSpec()); err == nil {
		t.Error("nil model kabul edildi")
	}

	bad := map[string]func(*RangeSpec){
		"rx duyarlılığı pozitif": func(s *RangeSpec) { s.RxSensitivityDBm = 10 },
		"h_BS ≤ h_UT":            func(s *RangeSpec) { s.HBSm = 1 },
		"frekans 0":              func(s *RangeSpec) { s.FreqMHz = 0 },
		"hüzme genişliği 0":      func(s *RangeSpec) { s.BeamWidthDeg = 0 },
		"eğim negatif":           func(s *RangeSpec) { s.TiltDeg = -1 },
		"EIRP NaN":               func(s *RangeSpec) { s.EIRPdBm = math.NaN() },
	}
	for name, mutate := range bad {
		s := urbanSpec()
		mutate(&s)
		if _, err := CoverageRangeM(m, s); err == nil {
			t.Errorf("%s: hata bekleniyordu", name)
		}
	}

	// Çok zayıf verici: hüzme tepesinde bile eşiğin altında.
	weak := urbanSpec()
	weak.EIRPdBm = -50
	if _, err := CoverageRangeM(m, weak); err == nil {
		t.Error("kapsama üretmeyen yapılandırma kabul edildi")
	}

	// Fiziksel olmayan güç: 200 km'de bile eşiğin üstünde.
	huge := urbanSpec()
	huge.EIRPdBm = 300
	if _, err := CoverageRangeM(m, huge); err == nil {
		t.Error("ıraksayan yapılandırma kabul edildi")
	}
}

// TestPBT_CoverageRangeIsRoot, çözümün her parametre bileşiminde denklemin
// kökü olduğunu sınar.
func TestPBT_CoverageRangeIsRoot(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		name := rapid.SampledFrom([]config.PropagationModel{
			config.ModelUMa, config.ModelUMi, config.ModelRMa,
		}).Draw(rt, "model")
		m, err := ModelFor(name)
		if err != nil {
			rt.Fatalf("ModelFor: %v", err)
		}

		s := RangeSpec{
			EIRPdBm:          rapid.Float64Range(40, 70).Draw(rt, "eirp"),
			RxSensitivityDBm: rapid.Float64Range(-125, -95).Draw(rt, "rxSens"),
			HBSm:             rapid.Float64Range(15, 60).Draw(rt, "hBS"),
			HUTm:             UTHeightM,
			FreqMHz:          rapid.IntRange(700, 2700).Draw(rt, "freq"),
			BeamWidthDeg:     65,
			TiltDeg:          rapid.Float64Range(0, 12).Draw(rt, "tilt"),
		}

		r, err := CoverageRangeM(m, s)
		if err != nil {
			return // fiziksel olmayan bileşim; ayrı testte kapsanıyor
		}
		if !(r > 0) || math.IsInf(r, 0) || math.IsNaN(r) {
			rt.Fatalf("r_max = %v", r)
		}
		if got := s.ReceivedPowerDBm(m, r); math.Abs(got-s.RxSensitivityDBm) > 1e-5 {
			rt.Fatalf("%s: r_max = %.3f m'de %.9f dBm, duyarlılık %.3f dBm",
				name, r, got, s.RxSensitivityDBm)
		}
	})
}
