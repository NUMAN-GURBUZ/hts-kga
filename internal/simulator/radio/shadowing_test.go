package radio

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// testSeed, testlerde kullanılan sabit koşu tohumu (configs/*.yaml: seed 42).
const testSeed = 42

func mustField(t *testing.T) *ShadowingField {
	t.Helper()
	f, err := NewShadowingField(testSeed, rf.UTHeightM)
	if err != nil {
		t.Fatalf("NewShadowingField: %v", err)
	}
	return f
}

// newSource, verilen model ve konumda bir test sitesi kurar.
func newSource(t *testing.T, model config.PropagationModel, key uint64, enu geo.Point) Source {
	t.Helper()
	m, err := rf.ModelFor(model)
	if err != nil {
		t.Fatalf("rf.ModelFor(%q): %v", model, err)
	}
	return Source{Key: key, ENU: enu, Model: m}
}

// TestNewShadowingField_Invalid, geçersiz h_UT'nin reddedildiğini sınar.
func TestNewShadowingField_Invalid(t *testing.T) {
	for _, h := range []float64{0, -1.5, math.NaN(), math.Inf(1)} {
		if _, err := NewShadowingField(testSeed, h); err == nil {
			t.Errorf("h_UT = %g için hata bekleniyordu", h)
		}
	}
}

// TestShadowing_Deterministic, K10 gereksinimini sınar:
// aynı (tohum, kaynak, konum) daima aynı sonucu verir.
func TestShadowing_Deterministic(t *testing.T) {
	src := newSource(t, config.ModelUMa, 12345, geo.Point{})
	p := geo.Point{X: 731.4, Y: -288.9}

	f1 := mustField(t)
	first := f1.EnvironmentAt(src, p)

	// Aynı alan, tekrar tekrar
	for i := 0; i < 100; i++ {
		if got := f1.EnvironmentAt(src, p); got != first {
			t.Fatalf("aynı alanda deterministik değil: %+v vs %+v", first, got)
		}
	}

	// Yeniden kurulan alan da aynı sonucu vermeli (koşu tekrarı)
	f2 := mustField(t)
	if got := f2.EnvironmentAt(src, p); got != first {
		t.Errorf("yeniden kurulan alan farklı sonuç verdi: %+v vs %+v", first, got)
	}
}

// TestShadowing_StationaryAgentIsConstant, tasarımın ana gerekçesini sınar:
// durağan ajanın gölgelemesi ve LOS durumu tick'ten tick'e değişmez.
//
// i.i.d. gölgeleme seçilseydi masadaki telefon her 5 dakikada bir başka
// hücreye bağlanırdı (sahte handover seli).
func TestShadowing_StationaryAgentIsConstant(t *testing.T) {
	f := mustField(t)
	home := geo.Point{X: 1234.5, Y: -678.9}

	for _, model := range []config.PropagationModel{config.ModelUMa, config.ModelUMi, config.ModelRMa} {
		t.Run(string(model), func(t *testing.T) {
			src := newSource(t, model, 777, geo.Point{X: 2000, Y: 1500})

			want := f.EnvironmentAt(src, home)
			// 30 gün × 288 tick = 8640 tick boyunca değişmemeli
			for tick := 0; tick < 8640; tick++ {
				if got := f.EnvironmentAt(src, home); got != want {
					t.Fatalf("tick %d: durağan ajanın ortamı değişti %+v → %+v", tick, want, got)
				}
			}
		})
	}
}

// TestShadowing_SiteLevelCorrelation, karara bağlanan davranışı sınar:
// aynı sitenin üç sektörü aynı LOS ve aynı gölgelemeyi paylaşır.
//
// Sektörler aynı direkte, aynı engellerin ardındadır. Ayrımı yalnızca anten
// deseni yapmalıdır (T-E02-12).
func TestShadowing_SiteLevelCorrelation(t *testing.T) {
	f := mustField(t)
	siteENU := geo.Point{X: 500, Y: 500}
	siteKey := SourceKey([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})

	// Üç sektör → tek Source (site anahtarı), üç ayrı çağrı
	src := newSource(t, config.ModelUMa, siteKey, siteENU)
	p := geo.Point{X: 900, Y: 250}

	sector0 := f.EnvironmentAt(src, p)
	sector120 := f.EnvironmentAt(src, p)
	sector240 := f.EnvironmentAt(src, p)

	if sector0 != sector120 || sector0 != sector240 {
		t.Errorf("aynı sitenin sektörleri farklı ortam gördü: %+v / %+v / %+v",
			sector0, sector120, sector240)
	}

	// Farklı site → bağımsız olmalı (aynı konumda bile)
	other := newSource(t, config.ModelUMa, siteKey+1, siteENU)
	if f.EnvironmentAt(other, p) == sector0 {
		t.Error("farklı siteler aynı ortamı üretti — anahtar hash'e girmiyor olabilir")
	}
}

// TestShadowing_SpatialCorrelation, aynı ızgara hücresindeki noktaların aynı
// çekimi paylaştığını sınar.
//
// Site çok uzağa konur: hücre içindeki yer değiştirme d_2D'yi (dolayısıyla
// P_LOS'u) ihmal edilebilir ölçüde değiştirir, böylece yalnızca ızgara
// etkisi ölçülür.
func TestShadowing_SpatialCorrelation(t *testing.T) {
	f := mustField(t)
	uma, _ := rf.ModelFor(config.ModelUMa)
	grid := uma.DecorrelationM(false) // 50 m

	src := newSource(t, config.ModelUMa, 4242, geo.Point{X: 100000, Y: 0})

	base := geo.Point{X: 5, Y: 5}
	want := f.EnvironmentAt(src, base)

	// Aynı ızgara hücresi [0,50) × [0,50)
	for _, p := range []geo.Point{{X: 1, Y: 1}, {X: 25, Y: 25}, {X: 49, Y: 49}, {X: 5, Y: 45}} {
		if got := f.EnvironmentAt(src, p); got != want {
			t.Errorf("aynı ızgara hücresinde farklı ortam: %+v (%+v) vs %+v", got, p, want)
		}
	}

	// Komşu ızgara hücresi → farklı çekim beklenir
	neighbour := geo.Point{X: base.X + grid, Y: base.Y}
	if got := f.EnvironmentAt(src, neighbour); got.ShadowingDB == want.ShadowingDB {
		t.Error("komşu ızgara hücresi aynı gölgelemeyi verdi — kırpma çalışmıyor olabilir")
	}
}

// TestShadowing_NegativeCoordinates, negatif ENU koordinatlarında kırpmanın
// doğru çalıştığını sınar (origin'in güneybatısı da geçerli alandır).
func TestShadowing_NegativeCoordinates(t *testing.T) {
	f := mustField(t)
	src := newSource(t, config.ModelUMa, 99, geo.Point{X: 100000, Y: 0})

	// −50 ile 0 arası tek bir ızgara hücresidir (grid = 50 m)
	want := f.EnvironmentAt(src, geo.Point{X: -25, Y: -25})
	for _, p := range []geo.Point{{X: -1, Y: -1}, {X: -49, Y: -49}, {X: -10, Y: -40}} {
		if got := f.EnvironmentAt(src, p); got != want {
			t.Errorf("negatif ızgara hücresinde tutarsızlık: %+v (%+v)", got, p)
		}
	}

	// Pozitif taraf farklı hücredir
	if f.EnvironmentAt(src, geo.Point{X: 25, Y: 25}) == want {
		t.Error("negatif ve pozitif hücreler aynı sonucu verdi")
	}
}

// sampleShadowing, verilen model için çok sayıda bağımsız gölgeleme örneği
// toplar ve LOS durumuna göre ayırır.
//
// Bağımsızlık, site anahtarını değiştirerek sağlanır: geometri sabit kalır,
// dolayısıyla P_LOS(d) tüm örnekler için aynıdır.
func sampleShadowing(t *testing.T, f *ShadowingField, model config.PropagationModel,
	d2DM float64, n int) (losSamples, nlosSamples []float64) {
	t.Helper()

	m, err := rf.ModelFor(model)
	if err != nil {
		t.Fatalf("rf.ModelFor: %v", err)
	}

	agent := geo.Point{}
	for i := 0; i < n; i++ {
		src := Source{Key: uint64(i) * 0x9E3779B97F4A7C15, ENU: geo.Point{X: d2DM}, Model: m}
		env := f.EnvironmentAt(src, agent)
		if env.LOS {
			losSamples = append(losSamples, env.ShadowingDB)
		} else {
			nlosSamples = append(nlosSamples, env.ShadowingDB)
		}
	}
	return losSamples, nlosSamples
}

// moments, örneklem ortalaması ve standart sapmasını döndürür.
func moments(xs []float64) (mean, std float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))

	var variance float64
	for _, x := range xs {
		d := x - mean
		variance += d * d
	}
	variance /= float64(len(xs))
	return mean, math.Sqrt(variance)
}

// TestShadowing_SigmaMatchesStandard, kararın özünü sınar: simülatör
// TR 38.901'in model+LOS başına σ değerlerini kullanır (config'in tek 7 dB'sini
// değil).
//
// Beklenen σ değerleri (Tablo 7.4.1-1):
//
//	UMa 4/6  ·  UMi 4/7,82  ·  RMa 6/8
func TestShadowing_SigmaMatchesStandard(t *testing.T) {
	const samples = 400_000
	// Örneklem standart sapmasının bağıl payı; n = 400k için ~%0,3 yeterli.
	const relTol = 0.02

	tests := []struct {
		model config.PropagationModel
		// LOS ve NLOS örneği birlikte toplamak için orta menzil seçilir.
		d2DM float64
	}{
		{config.ModelUMa, 60},
		{config.ModelUMi, 25},
		{config.ModelRMa, 800},
	}

	f := mustField(t)

	for _, tc := range tests {
		t.Run(string(tc.model), func(t *testing.T) {
			m, _ := rf.ModelFor(tc.model)
			los, nlos := sampleShadowing(t, f, tc.model, tc.d2DM, samples)

			for _, c := range []struct {
				name    string
				samples []float64
				wantSig float64
			}{
				{"LOS", los, m.ShadowingSigmaDB(true)},
				{"NLOS", nlos, m.ShadowingSigmaDB(false)},
			} {
				if len(c.samples) < 1000 {
					t.Fatalf("%s: yalnızca %d örnek toplandı, mesafe seçimi gözden geçirilmeli",
						c.name, len(c.samples))
				}
				mean, std := moments(c.samples)

				if math.Abs(mean) > 0.1 {
					t.Errorf("%s: ortalama %.4f dB, 0 bekleniyordu", c.name, mean)
				}
				if rel := math.Abs(std/c.wantSig - 1); rel > relTol {
					t.Errorf("%s: σ = %.4f dB, beklenen %.2f dB (bağıl fark %%%.2f)",
						c.name, std, c.wantSig, rel*100)
				}
				t.Logf("%s: n=%6d  ortalama=%+.4f dB  σ=%.4f dB (standart: %.2f dB)",
					c.name, len(c.samples), mean, std, c.wantSig)
			}
		})
	}
}

// TestShadowing_Normality, üretilen dağılımın normal olduğunu moment
// testleriyle sınar: çarpıklık ≈ 0, basıklık ≈ 3.
//
// Box-Muller'ın doğru uygulandığının ve hash'in desen taşımadığının
// göstergesidir.
func TestShadowing_Normality(t *testing.T) {
	const samples = 500_000

	f := mustField(t)
	m, _ := rf.ModelFor(config.ModelRMa)

	xs := make([]float64, 0, samples)
	agent := geo.Point{}
	for i := 0; i < samples; i++ {
		// Kısa mesafe → RMa'da neredeyse daima LOS → tek σ ile temiz örneklem
		src := Source{Key: uint64(i) * 0x9E3779B97F4A7C15, ENU: geo.Point{X: 15}, Model: m}
		xs = append(xs, f.EnvironmentAt(src, agent).ShadowingDB)
	}

	mean, std := moments(xs)

	var m3, m4 float64
	for _, x := range xs {
		d := (x - mean) / std
		m3 += d * d * d
		m4 += d * d * d * d
	}
	skew := m3 / float64(len(xs))
	kurt := m4 / float64(len(xs))

	if math.Abs(skew) > 0.02 {
		t.Errorf("çarpıklık = %.4f, 0 bekleniyordu (normal dağılım)", skew)
	}
	if math.Abs(kurt-3) > 0.05 {
		t.Errorf("basıklık = %.4f, 3 bekleniyordu (normal dağılım)", kurt)
	}
	t.Logf("n=%d  ortalama=%+.4f  σ=%.4f  çarpıklık=%+.4f  basıklık=%.4f",
		len(xs), mean, std, skew, kurt)
}

// TestShadowing_LOSRatioMatchesProbability, ampirik LOS oranının modelin
// P_LOS(d) değerine yakınsadığını sınar.
func TestShadowing_LOSRatioMatchesProbability(t *testing.T) {
	const samples = 200_000
	const tol = 0.005

	f := mustField(t)

	tests := []struct {
		model config.PropagationModel
		d2DM  float64
	}{
		{config.ModelUMa, 50},
		{config.ModelUMa, 200},
		{config.ModelUMi, 30},
		{config.ModelRMa, 500},
		{config.ModelRMa, 2000},
	}

	for _, tc := range tests {
		m, _ := rf.ModelFor(tc.model)
		want := m.LOSProbability(tc.d2DM, rf.UTHeightM)

		los, nlos := sampleShadowing(t, f, tc.model, tc.d2DM, samples)
		got := float64(len(los)) / float64(len(los)+len(nlos))

		if math.Abs(got-want) > tol {
			t.Errorf("%s d=%.0f m: ampirik LOS oranı %.4f, P_LOS = %.4f",
				tc.model, tc.d2DM, got, want)
		}
		t.Logf("%s d=%6.0f m: ampirik %.4f / kuramsal %.4f", tc.model, tc.d2DM, got, want)
	}
}

// TestShadowing_LOSAndValueIndependent, LOS kararı ile gölgeleme değerinin
// birbirinden bağımsız türetildiğini sınar.
//
// Aynı hash'ten iki akış ayrılıyor; ayraçlar (losSalt / shadowSalt) yanlış
// kurulsaydı LOS durumu gölgelemenin işaretiyle korelasyon gösterirdi.
func TestShadowing_LOSAndValueIndependent(t *testing.T) {
	const samples = 300_000

	f := mustField(t)
	m, _ := rf.ModelFor(config.ModelUMa)

	// σ'nın LOS'a bağlı olması karşılaştırmayı bozar; normalize ederek bakılır.
	var sumLOS, sumNLOS float64
	var nLOS, nNLOS int

	agent := geo.Point{}
	for i := 0; i < samples; i++ {
		src := Source{Key: uint64(i) * 0x9E3779B97F4A7C15, ENU: geo.Point{X: 60}, Model: m}
		env := f.EnvironmentAt(src, agent)
		z := env.ShadowingDB / m.ShadowingSigmaDB(env.LOS) // standartlaştır

		if env.LOS {
			sumLOS += z
			nLOS++
		} else {
			sumNLOS += z
			nNLOS++
		}
	}

	meanLOS := sumLOS / float64(nLOS)
	meanNLOS := sumNLOS / float64(nNLOS)

	// Bağımsızsa her iki koşullu ortalama da 0 olmalı
	if math.Abs(meanLOS) > 0.02 || math.Abs(meanNLOS) > 0.02 {
		t.Errorf("LOS ile gölgeleme bağımlı görünüyor: E[z|LOS] = %+.4f, E[z|NLOS] = %+.4f",
			meanLOS, meanNLOS)
	}
	t.Logf("E[z|LOS] = %+.4f (n=%d)   E[z|NLOS] = %+.4f (n=%d)",
		meanLOS, nLOS, meanNLOS, nNLOS)
}

// TestShadowing_SeedSeparation, farklı koşu tohumlarının farklı alan
// ürettiğini sınar (ADR-05: bir koşu = bir senaryo × bir seed).
func TestShadowing_SeedSeparation(t *testing.T) {
	src := newSource(t, config.ModelUMa, 555, geo.Point{X: 300, Y: 400})
	p := geo.Point{X: 100, Y: 100}

	seen := make(map[float64]bool)
	for _, seed := range []int64{1, 7, 42, 99, 12345} {
		f, err := NewShadowingField(seed, rf.UTHeightM)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		v := f.EnvironmentAt(src, p).ShadowingDB
		if seen[v] {
			t.Errorf("seed %d aynı gölgelemeyi üretti (%g) — tohum hash'e girmiyor", seed, v)
		}
		seen[v] = true
	}
}

// TestSourceKey, site anahtarı türetmesini sınar.
func TestSourceKey(t *testing.T) {
	a := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	b := a
	b[15] = 17 // tek bayt farkı

	if SourceKey(a) != SourceKey(a) {
		t.Error("SourceKey deterministik değil")
	}
	if SourceKey(a) == SourceKey(b) {
		t.Error("tek bayt farkı aynı anahtarı üretti")
	}
	if SourceKey([16]byte{}) == 0 {
		t.Error("sıfır kimlik sıfır anahtar üretti — karıştırma zayıf")
	}
}

// TestShadowing_ConvenienceWrappers, kolaylık sarmalayıcılarının
// EnvironmentAt ile tutarlı olduğunu sınar.
func TestShadowing_ConvenienceWrappers(t *testing.T) {
	f := mustField(t)
	src := newSource(t, config.ModelRMa, 31337, geo.Point{X: 1000, Y: 2000})
	p := geo.Point{X: 250, Y: -125}

	env := f.EnvironmentAt(src, p)
	if got := f.ShadowingDB(src, p); got != env.ShadowingDB {
		t.Errorf("ShadowingDB() = %g, EnvironmentAt = %g", got, env.ShadowingDB)
	}
	if got := f.IsLOS(src, p); got != env.LOS {
		t.Errorf("IsLOS() = %v, EnvironmentAt = %v", got, env.LOS)
	}
}

// TestShadowing_ClampedAtEightSigma, emniyet sübabının çalıştığını sınar.
func TestShadowing_ClampedAtEightSigma(t *testing.T) {
	tests := []struct {
		value, sigma, want float64
	}{
		{10, 7, 10},
		{100, 7, 56}, // 8 × 7
		{-100, 7, -56},
		{56, 7, 56},
	}
	for _, tc := range tests {
		if got := clampSigma(tc.value, tc.sigma); got != tc.want {
			t.Errorf("clampSigma(%g, %g) = %g, beklenen %g", tc.value, tc.sigma, got, tc.want)
		}
	}
}

// ─── Başarım ─────────────────────────────────────────────────────────────────

// BenchmarkEnvironmentAt, sıcak yol maliyetini ölçer.
//
// Hedef: **0 allocs/op**. 8,64M tick × ~8 aday hücre = ~69M çağrı beklenir;
// çağrı başına tek bir yığın ayırma bile GC baskısını kabul edilemez kılar.
func BenchmarkEnvironmentAt(b *testing.B) {
	f, err := NewShadowingField(testSeed, rf.UTHeightM)
	if err != nil {
		b.Fatalf("NewShadowingField: %v", err)
	}
	m, err := rf.ModelFor(config.ModelUMa)
	if err != nil {
		b.Fatalf("rf.ModelFor: %v", err)
	}

	src := Source{Key: 0xDEADBEEF, ENU: geo.Point{X: 500, Y: 500}, Model: m}
	p := geo.Point{X: 123.4, Y: 567.8}

	b.ReportAllocs()
	b.ResetTimer()

	var sink Environment
	for i := 0; i < b.N; i++ {
		// Konumu değiştir: önbelleklenmiş tek değer ölçülmesin
		p.X += 0.001
		sink = f.EnvironmentAt(src, p)
	}
	_ = sink
}
