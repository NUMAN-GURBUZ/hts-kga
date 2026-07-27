package radio

import (
	"math"
	"strings"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// senaryo parametreleri (configs/*.yaml + ADR-17 profilleri)
const (
	urbanHBSm = 25.0 // kentsel profil ant_height
	ruralHBSm = 45.0 // kırsal profil ant_height
)

// TestLink_D3DM, eğik mesafe hesabını sınar: d_3D = √(d_2D² + Δh²).
func TestLink_D3DM(t *testing.T) {
	tests := []struct {
		name string
		l    Link
		want float64
	}{
		{
			name: "3-4-5 üçgeni",
			l:    Link{D2DM: 4, HBSm: 3.5, HUTm: 0.5},
			want: 5,
		},
		{
			name: "eş yükseklik → d_3D = d_2D",
			l:    Link{D2DM: 1000, HBSm: 10, HUTm: 10},
			want: 1000,
		},
		{
			name: "kentsel 1 km",
			l:    Link{D2DM: 1000, HBSm: urbanHBSm, HUTm: UTHeightM},
			want: math.Sqrt(1000*1000 + 23.5*23.5),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.l.D3DM(); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("D3DM() = %g, beklenen %g", got, tc.want)
			}
		})
	}
}

// TestLink_FrequencyUnits, birim dönüşümlerini sınar.
// Yol kaybı formülleri GHz, kırılma mesafesi formülleri Hz ister.
func TestLink_FrequencyUnits(t *testing.T) {
	l := Link{FreqMHz: 2100}
	if got := l.FreqGHz(); math.Abs(got-2.1) > 1e-12 {
		t.Errorf("FreqGHz() = %g, beklenen 2.1", got)
	}
	if got := l.FreqHz(); math.Abs(got-2.1e9) > 1 {
		t.Errorf("FreqHz() = %g, beklenen 2.1e9", got)
	}
}

// TestLink_Validate, fiziksel olarak anlamsız bağların reddedildiğini sınar.
func TestLink_Validate(t *testing.T) {
	valid := Link{D2DM: 500, HBSm: urbanHBSm, HUTm: UTHeightM, FreqMHz: 2100}
	if err := valid.Validate(); err != nil {
		t.Fatalf("geçerli bağ reddedildi: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Link)
	}{
		{"mesafe 0", func(l *Link) { l.D2DM = 0 }},
		{"mesafe negatif", func(l *Link) { l.D2DM = -100 }},
		{"mesafe NaN", func(l *Link) { l.D2DM = math.NaN() }},
		{"mesafe Inf", func(l *Link) { l.D2DM = math.Inf(1) }},
		{"h_BS 0", func(l *Link) { l.HBSm = 0 }},
		{"h_UT 0", func(l *Link) { l.HUTm = 0 }},
		{"h_UT ≥ h_BS", func(l *Link) { l.HUTm = 30 }},
		{"frekans 0", func(l *Link) { l.FreqMHz = 0 }},
		{"frekans negatif", func(l *Link) { l.FreqMHz = -900 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := valid
			tc.mutate(&l)
			if err := l.Validate(); err == nil {
				t.Errorf("%s: hata bekleniyordu", tc.name)
			}
		})
	}
}

// TestModelFor, envanter etiketinden model çözümlemesini sınar.
// Cell.ModelType doğrudan bu işleve verilir (Sprint 1 verisi).
func TestModelFor(t *testing.T) {
	tests := []struct {
		name config.PropagationModel
		want string
	}{
		{config.ModelUMa, "UMa"},
		{config.ModelUMi, "UMi"},
		{config.ModelRMa, "RMa"},
	}

	for _, tc := range tests {
		t.Run(string(tc.name), func(t *testing.T) {
			m, err := ModelFor(tc.name)
			if err != nil {
				t.Fatalf("ModelFor(%q): %v", tc.name, err)
			}
			if string(m.Name()) != tc.want {
				t.Errorf("Name() = %q, beklenen %q", m.Name(), tc.want)
			}
		})
	}

	if _, err := ModelFor("COST231"); err == nil {
		t.Error("bilinmeyen model için hata bekleniyordu")
	}
}

// TestRegisteredModels, üç standart modelin kayıtlı olduğunu sınar.
func TestRegisteredModels(t *testing.T) {
	names := RegisteredModels()
	if len(names) < 3 {
		t.Fatalf("kayıtlı model sayısı = %d, en az 3 beklenir", len(names))
	}

	seen := make(map[config.PropagationModel]bool, len(names))
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []config.PropagationModel{config.ModelUMa, config.ModelUMi, config.ModelRMa} {
		if !seen[want] {
			t.Errorf("%q kayıtlı değil", want)
		}
	}
}

// stubModel, genişletilebilirliği sınamak için sahte bir modeldir
// (ör. ileride InH-Office veya NTN aynı yolu izleyecek).
type stubModel struct{ name config.PropagationModel }

func (s stubModel) Name() config.PropagationModel     { return s.name }
func (stubModel) PathLossDB(Link) float64             { return 100 }
func (stubModel) LOSProbability(_, _ float64) float64 { return 1 }
func (stubModel) ShadowingSigmaDB(bool) float64       { return 0 }
func (stubModel) DecorrelationM(bool) float64         { return 50 }
func (stubModel) Range() ValidityRange                { return ValidityRange{} }

// TestRegister, yeni model eklemenin mevcut kodu değiştirmeden çalıştığını
// sınar (açık/kapalı ilkesi) ve çift kayda izin verilmediğini doğrular.
func TestRegister(t *testing.T) {
	const name config.PropagationModel = "TEST-MODEL"
	t.Cleanup(func() { delete(registry, name) })

	if err := Register(stubModel{name: name}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	m, err := ModelFor(name)
	if err != nil {
		t.Fatalf("kayıtlı model bulunamadı: %v", err)
	}
	if got := m.PathLossDB(Link{}); got != 100 {
		t.Errorf("PathLossDB() = %g, beklenen 100", got)
	}

	// Aynı ada ikinci kayıt → sessiz üzerine yazma olmamalı
	if err := Register(stubModel{name: name}); err == nil {
		t.Error("çift kayıt için hata bekleniyordu")
	}
	// nil model
	if err := Register(nil); err == nil {
		t.Error("nil model için hata bekleniyordu")
	}
}

// TestCheckValidity, TR 38.901 uygulanabilirlik sınırlarının bildirildiğini sınar.
func TestCheckValidity(t *testing.T) {
	uma, _ := ModelFor(config.ModelUMa)
	rma, _ := ModelFor(config.ModelRMa)

	t.Run("kentsel geçerli bağ", func(t *testing.T) {
		l := Link{D2DM: 2000, HBSm: urbanHBSm, HUTm: UTHeightM, FreqMHz: 2100}
		if err := CheckValidity(uma, l); err != nil {
			t.Errorf("geçerli bağ reddedildi: %v", err)
		}
	})

	t.Run("kentsel mesafe aşımı", func(t *testing.T) {
		l := Link{D2DM: 6000, HBSm: urbanHBSm, HUTm: UTHeightM, FreqMHz: 2100}
		err := CheckValidity(uma, l)
		if err == nil {
			t.Fatal("5 km aşımı bildirilmeliydi")
		}
		if !strings.Contains(err.Error(), "d_2D") {
			t.Errorf("hata mesajı mesafeyi belirtmeli: %v", err)
		}
	})

	// Sprint 1 teknik borcu #3: kırsalda r_max = 20 km, RMa sınırı 10 km.
	t.Run("kırsal r_max dışdeğerlemesi", func(t *testing.T) {
		l := Link{D2DM: 20000, HBSm: ruralHBSm, HUTm: UTHeightM, FreqMHz: 900}
		err := CheckValidity(rma, l)
		if err == nil {
			t.Fatal("20 km, RMa 10 km sınırını aşıyor — bildirilmeliydi")
		}
		t.Logf("beklenen dışdeğerleme uyarısı: %v", err)
	})

	t.Run("kırsal geçerli bağ", func(t *testing.T) {
		l := Link{D2DM: 8000, HBSm: ruralHBSm, HUTm: UTHeightM, FreqMHz: 900}
		if err := CheckValidity(rma, l); err != nil {
			t.Errorf("8 km kırsal bağ geçerli olmalı: %v", err)
		}
	})

	t.Run("frekans aşımı", func(t *testing.T) {
		l := Link{D2DM: 1000, HBSm: urbanHBSm, HUTm: UTHeightM, FreqMHz: 300}
		if err := CheckValidity(uma, l); err == nil {
			t.Error("500 MHz altı bildirilmeliydi")
		}
	})

	t.Run("anten yüksekliği aşımı", func(t *testing.T) {
		l := Link{D2DM: 1000, HBSm: 200, HUTm: UTHeightM, FreqMHz: 900}
		if err := CheckValidity(rma, l); err == nil {
			t.Error("150 m üstü h_BS bildirilmeliydi")
		}
	})
}

// TestClampDistance, 10 m alt sınırının uygulandığını sınar.
// Altında TR 38.901 formülleri tanımsızdır.
func TestClampDistance(t *testing.T) {
	tests := []struct{ in, want float64 }{
		{0, minLinkDistanceM},
		{5, minLinkDistanceM},
		{minLinkDistanceM, minLinkDistanceM},
		{100, 100},
	}
	for _, tc := range tests {
		if got := clampDistance(tc.in); got != tc.want {
			t.Errorf("clampDistance(%g) = %g, beklenen %g", tc.in, got, tc.want)
		}
	}
}

// TestClampDistance_NoNaNOrInfPathLoss, sıfır mesafenin bile sonlu yol kaybı
// verdiğini sınar: kırpma olmasaydı log10(0) = −Inf olurdu.
func TestClampDistance_NoNaNOrInfPathLoss(t *testing.T) {
	for _, name := range []config.PropagationModel{config.ModelUMa, config.ModelUMi, config.ModelRMa} {
		m, _ := ModelFor(name)
		for _, d := range []float64{0, 0.001, 1, 5} {
			l := Link{D2DM: d, HBSm: urbanHBSm, HUTm: UTHeightM, FreqMHz: 2100}
			for _, los := range []bool{true, false} {
				l.LOS = los
				pl := m.PathLossDB(l)
				if math.IsNaN(pl) || math.IsInf(pl, 0) {
					t.Errorf("%s d=%g LOS=%v: yol kaybı sonlu değil (%g)", name, d, los, pl)
				}
			}
		}
	}
}

// TestEffectiveEnvironmentHeight, h_E sadeleşmesini sınar.
// Projede h_UT = 1,5 m < 13 m olduğundan h_E = 1 m daima geçerlidir.
func TestEffectiveEnvironmentHeight(t *testing.T) {
	if got := effectiveEnvironmentHeightM(UTHeightM); got != effectiveEnvHeightM {
		t.Errorf("h_E(%g) = %g, beklenen %g", UTHeightM, got, effectiveEnvHeightM)
	}
	if got := effectiveEnvironmentHeightM(12.9); got != effectiveEnvHeightM {
		t.Errorf("h_UT < 13 m için h_E = 1 m olmalı, bulunan %g", got)
	}
}

// TestClampProbability, olasılık kırpmasını sınar.
func TestClampProbability(t *testing.T) {
	tests := []struct{ in, want float64 }{
		{-0.5, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {1.5, 1},
	}
	for _, tc := range tests {
		if got := clampProbability(tc.in); got != tc.want {
			t.Errorf("clampProbability(%g) = %g, beklenen %g", tc.in, got, tc.want)
		}
	}
}

// TestShadowingSigma_MatchesStandard, σ_SF meta verisinin TR 38.901
// Tablo 7.4.1-1 değerleriyle uyuştuğunu sınar.
//
// NOT: Bu değerler simülatörde KULLANILMAZ; simülatör config'teki
// radio.shadowing_sigma_db (σ_nominal = 7) değerini kullanır (T-E02-11).
func TestShadowingSigma_MatchesStandard(t *testing.T) {
	tests := []struct {
		model     config.PropagationModel
		los, nlos float64
	}{
		{config.ModelUMa, 4.0, 6.0},
		{config.ModelUMi, 4.0, 7.82},
		{config.ModelRMa, 6.0, 8.0},
	}

	for _, tc := range tests {
		t.Run(string(tc.model), func(t *testing.T) {
			m, _ := ModelFor(tc.model)
			if got := m.ShadowingSigmaDB(true); got != tc.los {
				t.Errorf("σ_LOS = %g, beklenen %g", got, tc.los)
			}
			if got := m.ShadowingSigmaDB(false); got != tc.nlos {
				t.Errorf("σ_NLOS = %g, beklenen %g", got, tc.nlos)
			}
		})
	}
}
