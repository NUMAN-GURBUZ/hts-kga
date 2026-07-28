package density

import (
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// goldenConfig, altın senaryonun kütle parametreleridir.
// λ = 1 → σ_eff = σ_nominal = 7 dB (kalibrasyon öncesi başlangıç noktası).
func goldenConfig() Config {
	return Config{
		RxSensitivityDBm: -110,
		SigmaNominalDB:   7,
		Lambda:           1,
		UTHeightM:        testUTHeightM,
	}
}

// TestNormalCDF_References, Φ'yi bağımsız hesaplanmış değerlerle karşılaştırır.
//
// Değerler standart normal dağılım tablosundan / erfc tanımından gelir;
// uygulamadan bağımsızdır. Φ hem ADR-18'in kapsama olasılığında hem ADR-03'ün
// komşu kısıtında kullanıldığı için buradaki bir hata iki bileşeni birden
// bozardı.
func TestNormalCDF_References(t *testing.T) {
	cases := []struct {
		x    float64
		want float64
	}{
		{0, 0.500000000000},
		{1, 0.841344746069},
		{-1, 0.158655253931},
		{2, 0.977249868052},
		{-2, 0.022750131948},
		{3.5, 0.999767370921},
		{-3.5, 0.000232629079},
	}
	for _, tc := range cases {
		if got := NormalCDF(tc.x); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("Φ(%.1f) = %.12f, beklenen %.12f", tc.x, got, tc.want)
		}
	}
	// Simetri ve uç davranış
	if math.Abs(NormalCDF(4)+NormalCDF(-4)-1) > 1e-15 {
		t.Error("Φ(x) + Φ(−x) = 1 bozuldu")
	}
	if NormalCDF(math.Inf(1)) != 1 || NormalCDF(math.Inf(-1)) != 0 {
		t.Error("Φ uç değerlerde 1/0 vermiyor")
	}
}

// TestMixedPathLoss_IsWeightedMixture, karışımın iki uç arasında ve doğru
// ağırlıkla kaldığını sınar (ADR-19/1).
//
// Referans doğrudan tanımdan hesaplanır: p_LOS ve iki yol kaybı ayrı ayrı
// çağrılıp elle harmanlanır.
func TestMixedPathLoss_IsWeightedMixture(t *testing.T) {
	cell := goldenCell(t)

	for _, d := range []float64{50, 200, 500, 1000, 2500, 5000} {
		link := rf.Link{D2DM: d, HBSm: cell.AntHeightM, HUTm: testUTHeightM, FreqMHz: cell.FreqMHz}

		pLOS := cell.Model.LOSProbability(d, testUTHeightM)
		losLink, nlosLink := link, link
		losLink.LOS, nlosLink.LOS = true, false
		los := cell.Model.PathLossDB(losLink)
		nlos := cell.Model.PathLossDB(nlosLink)

		want := pLOS*los + (1-pLOS)*nlos
		got := MixedPathLossDB(cell.Model, link, testUTHeightM)

		if math.Abs(got-want) > 1e-12 {
			t.Errorf("d=%.0f m: karışım %.9f, elle %.9f", d, got, want)
		}
		if got < los-1e-9 || got > nlos+1e-9 {
			t.Errorf("d=%.0f m: karışım (%.4f) LOS (%.4f) ile NLOS (%.4f) arasında değil",
				d, got, los, nlos)
		}
		t.Logf("d=%5.0f m  p_LOS=%.4f  PL_LOS=%7.3f  PL_NLOS=%7.3f  PL_karışım=%7.3f dB",
			d, pLOS, los, nlos, got)
	}
}

// TestMixedPathLoss_MonotonicInDistance, karışımın mesafeyle azalmadığını
// sınar: uzaklaşmak sinyali güçlendiremez.
func TestMixedPathLoss_MonotonicInDistance(t *testing.T) {
	cell := goldenCell(t)
	prev := math.Inf(-1)
	for d := 20.0; d <= 5000; d += 20 {
		link := rf.Link{D2DM: d, HBSm: cell.AntHeightM, HUTm: testUTHeightM, FreqMHz: cell.FreqMHz}
		got := MixedPathLossDB(cell.Model, link, testUTHeightM)
		if got < prev-1e-6 {
			t.Fatalf("d=%.0f m: yol kaybı düştü (%.6f < %.6f)", d, got, prev)
		}
		prev = got
	}
}

// TestCoverageProbability_HalfAtSensitivity, ADR-18'in birinci çarpanının
// yapısal referansıdır.
//
// Alınan güç tam olarak alıcı duyarlılığına eşit olduğunda marj sıfırdır ve
// Φ(0) = 0,5 olmalıdır. Bu nokta ikili aramayla bulunur; σ_eff'ten bağımsızdır,
// yani λ ne olursa olsun aynı mesafede 0,5 çıkar.
func TestCoverageProbability_HalfAtSensitivity(t *testing.T) {
	cell := goldenCell(t)
	cfg := goldenConfig()

	lo, hi := 1.0, 50000.0
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if ReceivedPowerDBm(cell, at(0, mid), cfg.UTHeightM) > cfg.RxSensitivityDBm {
			lo = mid
		} else {
			hi = mid
		}
	}
	dEdge := (lo + hi) / 2
	p := ReceivedPowerDBm(cell, at(0, dEdge), cfg.UTHeightM)
	t.Logf("duyarlılık sınırı: d = %.3f m, P_s = %.6f dBm (eşik %.1f dBm)",
		dEdge, p, cfg.RxSensitivityDBm)

	if got := CoverageProbability(cell, at(0, dEdge), cfg); math.Abs(got-0.5) > 1e-6 {
		t.Errorf("duyarlılık sınırında kapsama olasılığı %.9f, 0.5 beklenir", got)
	}

	// λ değişse de sınır noktası aynı kalmalı: λ eğrinin dikliğini değiştirir,
	// yerini değil.
	for _, lambda := range []float64{0.5, 1.5, 3.0} {
		c := cfg
		c.Lambda = lambda
		if got := CoverageProbability(cell, at(0, dEdge), c); math.Abs(got-0.5) > 1e-6 {
			t.Errorf("λ=%.1f: sınırda %.9f, 0.5 beklenir", lambda, got)
		}
	}
}

// TestCoverageProbability_LambdaSharpness, λ'nın geçişin dikliğini
// değiştirdiğini sınar (ADR-19/3: σ_eff = λ·σ_nominal).
//
// Küçük λ → dar σ_eff → keskin geçiş; büyük λ → yayvan geçiş.
func TestCoverageProbability_LambdaSharpness(t *testing.T) {
	cell := goldenCell(t)
	base := goldenConfig()

	// Sınırın belirgin biçimde içinde kalan bir nokta
	p := at(0, 300)

	var prev float64 = 1
	for _, lambda := range []float64{0.5, 1.0, 2.0, 3.0} {
		cfg := base
		cfg.Lambda = lambda
		got := CoverageProbability(cell, p, cfg)
		t.Logf("λ=%.1f (σ_eff=%.1f dB) → kapsama olasılığı %.9f", lambda, cfg.SigmaEffDB(), got)

		if got > prev+1e-12 {
			t.Errorf("λ büyüdükçe güçlü sinyalde olasılık artmamalı (%.9f > %.9f)", got, prev)
		}
		prev = got
	}
}

// TestRadial_Composition, radyal ağırlığın iki çarpanın çarpımı olduğunu
// sınar (ADR-18/1).
func TestRadial_Composition(t *testing.T) {
	cell := goldenCell(t)
	cfg := goldenConfig()
	p := at(0, 800)

	cov := CoverageProbability(cell, p, cfg)
	for _, taW := range []float64{0.25, 0.5, 1.0} {
		want := cov * taW
		if got := Radial(cell, p, taW, cfg); math.Abs(got-want) > 1e-12 {
			t.Errorf("w_TA=%.2f: w_rad = %.12f, beklenen %.12f", taW, got, want)
		}
	}
	// TA ağırlığı sıfırsa hücre kütleye katılmaz.
	if got := Radial(cell, p, 0, cfg); got != 0 {
		t.Errorf("w_TA=0 için w_rad = %.12f, 0 beklenir", got)
	}
}

// TestRadial_DecreasesWithDistance, radyal ağırlığın mesafeyle azaldığını
// sınar: uzaklaştıkça kaydın var olma olasılığı düşer.
func TestRadial_DecreasesWithDistance(t *testing.T) {
	cell := goldenCell(t)
	cfg := goldenConfig()

	prev := math.Inf(1)
	for d := 100.0; d <= 6000; d += 100 {
		got := Radial(cell, at(0, d), 1, cfg)
		if got > prev+1e-12 {
			t.Fatalf("d=%.0f m: ağırlık arttı (%.12f > %.12f)", d, got, prev)
		}
		prev = got
	}
	t.Logf("6000 m'de w_rad = %.9e", prev)
}

// TestConfig_Validation, parametre denetimlerini kapsar.
func TestConfig_Validation(t *testing.T) {
	valid := goldenConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("geçerli config reddedildi: %v", err)
	}
	if got := valid.SigmaEffDB(); math.Abs(got-7) > 1e-12 {
		t.Errorf("λ=1 için σ_eff = %.9f, 7 beklenir", got)
	}

	bad := map[string]func(*Config){
		"rx duyarlılığı pozitif": func(c *Config) { c.RxSensitivityDBm = 10 },
		"σ_nominal sıfır":        func(c *Config) { c.SigmaNominalDB = 0 },
		"λ sıfır":                func(c *Config) { c.Lambda = 0 },
		"alıcı yüksekliği sıfır": func(c *Config) { c.UTHeightM = 0 },
	}
	for name, mutate := range bad {
		c := goldenConfig()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: hata bekleniyordu", name)
		}
	}
}

// TestPBT_RadialInUnitInterval, radyal ağırlığın her koşulda [0,1] aralığında
// kaldığını sınar.
func TestPBT_RadialInUnitInterval(t *testing.T) {
	cell := goldenCell(t)

	rapid.Check(t, func(rt *rapid.T) {
		cfg := Config{
			RxSensitivityDBm: rapid.Float64Range(-130, -90).Draw(rt, "rxSens"),
			SigmaNominalDB:   rapid.Float64Range(1, 12).Draw(rt, "sigma"),
			Lambda:           rapid.Float64Range(0.5, 3).Draw(rt, "lambda"),
			UTHeightM:        testUTHeightM,
		}
		p := geo.Point{
			X: rapid.Float64Range(-25000, 25000).Draw(rt, "x"),
			Y: rapid.Float64Range(-25000, 25000).Draw(rt, "y"),
		}
		taW := rapid.Float64Range(0, 1).Draw(rt, "taWeight")

		got := Radial(cell, p, taW, cfg)
		if math.IsNaN(got) || got < 0 || got > 1 {
			rt.Fatalf("w_rad = %v — [0,1] dışında (p=%v, w_TA=%.4f)", got, p, taW)
		}
	})
}
