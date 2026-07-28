// Package radio, 3GPP TR 38.901 yayılım modellerini ve bunlara dayanan
// alınan güç hesaplarını sağlar.
//
// T-E02-10 — ADR-06: TR 38.901 **tek ve yegâne** çalışma zamanı modelidir.
// Fallback yoktur, iki modelin ortalaması alınmaz. Okumura-Hata ve COST-231
// yalnızca birim testte çapraz doğrulama referansı olarak kullanılır
// (crossvalidate_test.go).
//
// Katman kuralı: bu paket saf hesap katmanıdır — durum tutmaz, günlük yazmaz,
// ajan veya envanter tiplerini tanımaz. Girdi Link, çıktı dB'dir. Sıcak yolda
// tick başına milyonlarca kez çağrılacağı için hiçbir fonksiyon ayırma
// (allocation) yapmaz.
package rf

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// ─── Fiziksel ve standart sabitleri ──────────────────────────────────────────

const (
	// speedOfLightMPS, TR 38.901'in kırılma mesafesi formüllerinde kullandığı
	// ışık hızıdır. Standart, tam değer (299 792 458) yerine 3,0·10⁸ m/s
	// yazar; formül uyumu için aynısı kullanılır.
	speedOfLightMPS = 3.0e8

	// minLinkDistanceM, TR 38.901 yol kaybı formüllerinin alt geçerlilik
	// sınırıdır (10 m ≤ d_2D). Altındaki mesafeler bu değere kırpılır:
	// log10 terimleri 0'a giderken formül anlamsızlaşır.
	minLinkDistanceM = 10.0

	// effectiveEnvHeightM, UMa/UMi kırılma mesafesindeki etkin çevre
	// yüksekliği h_E'dir. TR 38.901 Tablo 7.4.1-1 Not 1: h_UT < 13 m iken
	// C(d_2D, h_UT) = 0 olduğundan h_E = 1 m olasılığı 1'dir.
	effectiveEnvHeightM = 1.0

	// simpleEnvHeightMaxHUTm, yukarıdaki h_E = 1 m sadeleşmesinin geçerli
	// olduğu üst UT yüksekliğidir. Projede h_UT = 1,5 m (ADR-06) olduğundan
	// sadeleşme daima geçerlidir.
	simpleEnvHeightMaxHUTm = 13.0

	// freqTermCoefDB, tüm TR 38.901 yol kaybı formüllerinde ortak olan
	// 20·log10(f_c) frekans teriminin katsayısıdır (serbest uzay bağıntısı).
	freqTermCoefDB = 20.0

	// mhzPerGHz / hzPerMHz, frekans birim dönüşümleri.
	// TR 38.901 yol kaybı formülleri GHz, kırılma mesafesi formülleri Hz ister.
	mhzPerGHz = 1000.0
	hzPerMHz  = 1.0e6
)

// UTHeightM, kullanıcı terminali (UT) anten yüksekliğidir (metre).
//
// ADR-06 çapraz doğrulama parametre kümesinde h_UT = 1,5 m olarak sabitlenmiştir;
// simülatör de aynı değeri kullanır (yaya el terminali varsayımı).
const UTHeightM = 1.5

// ─── Bağ (link) tanımı ───────────────────────────────────────────────────────

// Link, tek bir verici–alıcı bağının geometrik ve spektral parametreleridir.
//
// LOS alanı, görüş hattı (line-of-sight) durumunu taşır. Bu değer modelin
// LOSProbability çıktısından türetilir; türetme işi bu pakete ait değildir
// (deterministik çekim ajan kimliğine bağlıdır — T-E02-11/12).
type Link struct {
	// D2DM, verici ile alıcının yatay düzlemdeki mesafesidir (metre).
	D2DM float64
	// HBSm, baz istasyonu anten yüksekliğidir (metre) — cells.ant_height.
	HBSm float64
	// HUTm, kullanıcı terminali anten yüksekliğidir (metre).
	HUTm float64
	// FreqMHz, taşıyıcı frekanstır — cells.freq_mhz.
	FreqMHz int
	// LOS, görüş hattı durumudur; false ise NLOS formülleri uygulanır.
	LOS bool
}

// D3DM, eğik (3 boyutlu) mesafeyi döndürür (metre):
//
//	d_3D = √(d_2D² + (h_BS − h_UT)²)
//
// TR 38.901 yol kaybı formülleri d_3D, LOS olasılığı formülleri d_2D kullanır.
func (l Link) D3DM() float64 {
	dh := l.HBSm - l.HUTm
	return math.Hypot(l.D2DM, dh)
}

// FreqGHz, taşıyıcı frekansı GHz cinsinden döndürür (yol kaybı formülleri için).
func (l Link) FreqGHz() float64 {
	return float64(l.FreqMHz) / mhzPerGHz
}

// FreqHz, taşıyıcı frekansı Hz cinsinden döndürür (kırılma mesafesi için).
func (l Link) FreqHz() float64 {
	return float64(l.FreqMHz) * hzPerMHz
}

// Validate, bağın fiziksel olarak anlamlı olup olmadığını denetler.
//
// Sıcak yolda çağrılmaz; envanter kurulumunda ve testlerde kullanılır.
func (l Link) Validate() error {
	if !isPositiveFinite(l.D2DM) {
		return fmt.Errorf("link: d_2D pozitif ve sonlu olmalı (%g)", l.D2DM)
	}
	if !isPositiveFinite(l.HBSm) {
		return fmt.Errorf("link: h_BS pozitif ve sonlu olmalı (%g)", l.HBSm)
	}
	if !isPositiveFinite(l.HUTm) {
		return fmt.Errorf("link: h_UT pozitif ve sonlu olmalı (%g)", l.HUTm)
	}
	if l.HUTm >= l.HBSm {
		return fmt.Errorf("link: h_UT (%g) h_BS'den (%g) küçük olmalı", l.HUTm, l.HBSm)
	}
	if l.FreqMHz <= 0 {
		return fmt.Errorf("link: frekans pozitif olmalı (%d MHz)", l.FreqMHz)
	}
	return nil
}

// ─── Model arayüzü ───────────────────────────────────────────────────────────

// ValidityRange, bir yayılım modelinin TR 38.901'de tanımlı uygulanabilirlik
// aralığıdır. Sınır dışı bağlar dışdeğerleme (extrapolation) demektir;
// CheckValidity ile görünür kılınır.
type ValidityRange struct {
	MinD2DM, MaxD2DM float64
	MinFreqMHz       int
	MaxFreqMHz       int
	MinHBSm, MaxHBSm float64
	MinHUTm, MaxHUTm float64
}

// PathLossModel, bir yayılım modelinin sözleşmesidir.
//
// Yeni model eklemek (ör. InH-Office, NTN) mevcut hiçbir kodu değiştirmez:
// arayüz uygulanır ve Register ile kaydedilir (açık/kapalı ilkesi).
type PathLossModel interface {
	// Name, modelin envanterdeki karşılığını döndürür (cells.model_type).
	Name() config.PropagationModel

	// PathLossDB, bağın yol kaybını döndürür (dB, pozitif).
	// Gölgeleme İÇERMEZ — o ayrı bir bileşendir (T-E02-11).
	PathLossDB(l Link) float64

	// LOSProbability, verilen yatay mesafe ve UT yüksekliği için görüş hattı
	// olasılığını döndürür (TR 38.901 Tablo 7.4.2-1), [0,1] aralığında.
	LOSProbability(d2DM, hUTm float64) float64

	// ShadowingSigmaDB, standardın bu model için öngördüğü gölgeleme standart
	// sapmasıdır (dB) — TR 38.901 Tablo 7.4.1-1.
	//
	// Simülatör bu değeri **kullanır** (T-E02-11 kararı): gerçeği üreten süreç
	// standarda sadıktır. Analiz motoru ise LOS durumunu bilmediği için tek bir
	// σ varsayar (config: radio.shadowing_sigma_db = 7 dB) ve aradaki farkı
	// λ kalibrasyonu emer — ADR-02/ADR-03 bilgi asimetrisi.
	ShadowingSigmaDB(los bool) float64

	// DecorrelationM, gölgelemenin yatay düzlemdeki dekorelasyon mesafesidir
	// (metre) — TR 38.901 Tablo 7.5-6.
	//
	// Bu mesafeden uzak iki nokta arasındaki gölgeleme bağımsız kabul edilir;
	// daha yakın noktalar aynı engellerin ardındadır. ShadowingField mekânsal
	// ızgarasını bu değerle kurar (T-E02-11).
	DecorrelationM(los bool) float64

	// Range, modelin TR 38.901 uygulanabilirlik aralığını döndürür.
	Range() ValidityRange
}

// ─── Model kayıt defteri ─────────────────────────────────────────────────────

// registry, envanter model etiketini uygulamasına eşler.
// Genişletme noktası: yeni modeller Register ile eklenir.
var registry = map[config.PropagationModel]PathLossModel{
	config.ModelUMa: UMa{},
	config.ModelUMi: UMi{},
	config.ModelRMa: RMa{},
}

// Register, yeni bir yayılım modelini kayıt defterine ekler.
//
// Aynı ada sahip bir model zaten kayıtlıysa hata döner: sessiz üzerine yazma,
// hangi modelin çalıştığını belirsizleştirirdi.
func Register(m PathLossModel) error {
	if m == nil {
		return fmt.Errorf("model kaydı: nil model")
	}
	name := m.Name()
	if _, exists := registry[name]; exists {
		return fmt.Errorf("model kaydı: %q zaten kayıtlı", name)
	}
	registry[name] = m
	return nil
}

// ModelFor, envanter etiketine karşılık gelen yayılım modelini döndürür.
// Cell.ModelType doğrudan bu işleve verilir.
func ModelFor(name config.PropagationModel) (PathLossModel, error) {
	m, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("bilinmeyen yayılım modeli %q", name)
	}
	return m, nil
}

// RegisteredModels, kayıtlı model adlarını döndürür (tanılama ve test için).
func RegisteredModels() []config.PropagationModel {
	names := make([]config.PropagationModel, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// ─── Geçerlilik denetimi ─────────────────────────────────────────────────────

// CheckValidity, bağın modelin TR 38.901 uygulanabilirlik aralığında olup
// olmadığını denetler. Aralık dışıysa açıklayıcı bir hata döndürür.
//
// Bu bir **uyarı mekanizmasıdır**, engelleme değil: çağıran hesabı sürdürüp
// durumu günlüğe yazabilir. Sıcak yolda çağrılmaz — günlükleme maliyeti tick
// başına kabul edilemez; envanter kurulumunda bir kez denetlenir.
//
// Bilinen durum: kırsal profilde r_max = 20 km iken RMa sınırı 10 km'dir
// (Sprint 1 teknik borcu). Bu aralıkta model dışdeğerleme yapar.
func CheckValidity(m PathLossModel, l Link) error {
	r := m.Range()

	if l.D2DM < r.MinD2DM || l.D2DM > r.MaxD2DM {
		return fmt.Errorf("%s: d_2D = %.0f m, geçerlilik aralığı [%.0f, %.0f] m",
			m.Name(), l.D2DM, r.MinD2DM, r.MaxD2DM)
	}
	if l.FreqMHz < r.MinFreqMHz || l.FreqMHz > r.MaxFreqMHz {
		return fmt.Errorf("%s: f_c = %d MHz, geçerlilik aralığı [%d, %d] MHz",
			m.Name(), l.FreqMHz, r.MinFreqMHz, r.MaxFreqMHz)
	}
	if l.HBSm < r.MinHBSm || l.HBSm > r.MaxHBSm {
		return fmt.Errorf("%s: h_BS = %.1f m, geçerlilik aralığı [%.1f, %.1f] m",
			m.Name(), l.HBSm, r.MinHBSm, r.MaxHBSm)
	}
	if l.HUTm < r.MinHUTm || l.HUTm > r.MaxHUTm {
		return fmt.Errorf("%s: h_UT = %.1f m, geçerlilik aralığı [%.1f, %.1f] m",
			m.Name(), l.HUTm, r.MinHUTm, r.MaxHUTm)
	}
	return nil
}

// ─── Ortak yardımcılar ───────────────────────────────────────────────────────

// clampDistance, mesafeyi modelin alt geçerlilik sınırına kırpar.
// 10 m altında TR 38.901 formülleri tanımsızdır.
func clampDistance(d2DM float64) float64 {
	if d2DM < minLinkDistanceM {
		return minLinkDistanceM
	}
	return d2DM
}

// breakpointDistanceUrbanM, UMa/UMi kırılma mesafesini döndürür (metre):
//
//	d'_BP = 4 · h'_BS · h'_UT · f_c / c        (f_c: Hz, c: m/s)
//	h'_BS = h_BS − h_E ,  h'_UT = h_UT − h_E
//
// Kırılma mesafesi, yer yansımasının doğrudan bileşenle yıkıcı girişime
// başladığı noktadır; ötesinde yol kaybı üsteli 2'den 4'e çıkar.
func breakpointDistanceUrbanM(hBSm, hUTm, freqHz float64) float64 {
	hE := effectiveEnvironmentHeightM(hUTm)
	return 4 * (hBSm - hE) * (hUTm - hE) * freqHz / speedOfLightMPS
}

// effectiveEnvironmentHeightM, UMa/UMi etkin çevre yüksekliği h_E'dir.
//
// TR 38.901 Tablo 7.4.1-1 Not 1: h_UT < 13 m iken C(d_2D, h_UT) = 0 olur ve
// h_E = 1 m olasılığı 1'e eşitlenir. Projede h_UT = 1,5 m olduğundan bu dal
// daima çalışır; üst dal ileride yüksek UT desteklenirse anlam kazanır.
func effectiveEnvironmentHeightM(hUTm float64) float64 {
	if hUTm < simpleEnvHeightMaxHUTm {
		return effectiveEnvHeightM
	}
	// h_UT ≥ 13 m: standart h_E'yi {12, 15, ..., h_UT−1,5} kümesinden düzgün
	// dağılımla seçer. Bu proje bu aralığı kullanmaz; en muhafazakâr (en küçük)
	// değer seçilir ve davranış deterministik kalır.
	return effectiveEnvHeightM
}

// log10 , math.Log10 için kısa ad — formüllerin standart metne benzer okunması için.
func log10(v float64) float64 { return math.Log10(v) }

// clampProbability, olasılığı [0,1] aralığına kırpar.
// TR 38.901 LOS olasılığı ifadeleri, yüksek UT düzeltmesiyle 1'i aşabilir.
func clampProbability(p float64) float64 {
	switch {
	case p < 0:
		return 0
	case p > 1:
		return 1
	default:
		return p
	}
}

// isPositiveFinite, değerin pozitif ve sonlu olduğunu bildirir.
// NaN, `v <= 0` karşılaştırmasını sessizce geçtiği için ayrıca elenir.
func isPositiveFinite(v float64) bool {
	return v > 0 && !math.IsInf(v, 1)
}
