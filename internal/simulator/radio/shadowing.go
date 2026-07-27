// T-E02-11 — Log-normal gölgeleme ve LOS çözümlemesi.
//
// Yol kaybı deterministiktir: aynı mesafe daima aynı dB'yi verir. Gerçekte ise
// aynı mesafedeki iki nokta farklı engellerin (bina, ağaç, tepe) ardındadır.
// Gölgeleme, bu çevresel değişkenliği modelleyen sıfır ortalamalı log-normal
// terimdir:
//
//	RX(dBm) = EIRP − PathLoss(link) − A_beam(θ) + S ,   S ~ N(0, σ²)
//
// LOS/NLOS durumu da aynı fiziksel gerçeğin ürünüdür (aradaki engeller), bu
// yüzden ikisi birlikte, tek sorumluluk altında çözülür.
//
// # Neden konuma bağlı alan, ajan geçmişine bağlı süreç değil
//
// Gölgeleme, ajanın nereden geldiğinin değil, **nerede olduğunun** sonucudur:
// aradaki binalar kimin geçtiğine bağlı değildir. Bu yüzden AR(1) yol-tabanlı
// bir süreç yerine, konumun saf fonksiyonu olan bir rastgele alan kurulur:
//
//	S(kaynak, p) = σ · N⁻¹( hash(seed ‖ kaynak ‖ ⌊p/d_cor⌋) )
//
// Kazanımlar:
//   - Durumsuz  → ajan başına bellek yok, double-buffer etkileşimi yok
//   - Deterministik → K10, goroutine zamanlamasından bağımsız (saf fonksiyon)
//   - Fiziksel → aynı noktadaki iki farklı ajan aynı gölgelemeyi görür
//   - Dayanıklı → hücre aday kümesinden çıkıp geri girerse değer korunur
//
// # Site düzeyi
//
// Gölgeleme ve LOS **site** düzeyinde çözülür: aynı direğin üç sektörü aynı
// binaların ardındadır, biri LOS diğeri NLOS olamaz. Sektörler arası ayrımı
// yalnızca anten deseni A_beam(θ) yapar — best-server kararı böylece fiziksel
// anlam kazanır (T-E02-12).
//
// # Zaman çözünürlüğü sınırı
//
// Tick 5 dakikadır. Kentsel yolculukta ajan tick başına ~2.500 m yer değiştirir;
// dekorelasyon mesafeleri ise 10–120 m'dir. Yani hareket hâlinde ardışık
// tick'ler zaten bağımsızdır. Korelasyonun görünür olduğu tek durum ajanın
// **durağan** olmasıdır (günün ~%80'i) — ve orada Δd = 0 olduğundan gölgeleme
// birebir sabit kalır. Masadaki telefonun her 5 dakikada bir başka hücreye
// atlaması bu tasarımla imkânsızdır.
package radio

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// ─── Karıştırma sabitleri ────────────────────────────────────────────────────

const (
	// splitmixGamma, SplitMix64'ün altın oran türevli artış sabiti.
	splitmixGamma = 0x9E3779B97F4A7C15
	// splitmixMulA / splitmixMulB, SplitMix64 son karıştırma çarpanları.
	splitmixMulA = 0xBF58476D1CE4E5B9
	splitmixMulB = 0x94D049BB133111EB

	// losSalt / shadowSalt, tek konum hash'inden iki **bağımsız** akış
	// türetmek için kullanılan ayraçlar. LOS kararı ile gölgeleme değeri
	// arasında korelasyon oluşmasını engeller.
	losSalt    = 0xA5A5A5A5A5A5A5A5
	shadowSalt = 0x5A5A5A5A5A5A5A5A

	// mantissaShift / mantissaScale, 64 bitlik hash'i (0,1) **açık** aralığına
	// taşır. +0,5 kayması uç değerleri dışarıda bırakır (log(0) = −Inf koruması).
	//
	// 53 değil 52 bit kullanılır: 53 bitte, en büyük hash için (2⁵³−1)+0,5
	// float64 mantisine sığmaz ve 2⁵³'e yuvarlanarak sonucu tam olarak 1,0
	// yapardı. 52 bitte (2⁵²−1)+0,5 tam temsil edilebilir, üst sınır kesin
	// olarak 1'in altında kalır. Kaybedilen 1 bit entropi bu kullanım için
	// önemsizdir. (Bu sınır TestPBT_UnitOpenInterval ile yakalanmıştır.)
	mantissaShift = 12
	mantissaScale = 1.0 / (1 << 52)
)

// maxAbsSigmaMultiple, üretilen gölgelemenin σ cinsinden mutlak üst sınırıdır.
//
// Box-Muller kuramsal olarak sınırsızdır; pratikte 8σ'yı aşan değer 10⁻¹⁵
// olasılıktadır. Kırpma, bozuk bir hash'in uç değer üretmesi hâlinde link
// bütçesini anlamsızlaştırmasını engelleyen bir emniyet sübabıdır.
const maxAbsSigmaMultiple = 8.0

// ─── Tipler ──────────────────────────────────────────────────────────────────

// Source, gölgelemenin ve LOS durumunun kaynağı olan baz istasyonu **sitesidir**
// (tek tek sektörler değil).
type Source struct {
	// Key, siteyi ayırt eden deterministik anahtardır (bkz. SourceKey).
	Key uint64
	// ENU, sitenin yerel düzlem konumudur (metre).
	ENU geo.Point
	// Model, sitenin yayılım modelidir — cells.model_type'tan gelir.
	Model PathLossModel
}

// Environment, bir site ile bir nokta arasındaki yayılım ortamının
// çözümlenmiş durumudur.
type Environment struct {
	// LOS, görüş hattının açık olup olmadığıdır.
	LOS bool
	// ShadowingDB, log-normal gölgeleme terimidir (dB, sıfır ortalamalı).
	// Pozitif değer sinyali güçlendirir, negatif zayıflatır.
	ShadowingDB float64
}

// ShadowingField, konuma bağlı gölgeleme ve LOS alanıdır.
//
// Değişmezdir (immutable): kurulumdan sonra hiçbir alan yazılmaz, bu yüzden
// 1000 goroutine tarafından kilitsiz paylaşılabilir.
type ShadowingField struct {
	seed uint64
	hUTm float64
}

// NewShadowingField, koşu tohumundan bir gölgeleme alanı oluşturur.
//
// Alan σ ve dekorelasyon mesafesi tutmaz: bunlar bağın modeline ve LOS
// durumuna göre PathLossModel'den okunur (TR 38.901 Tablo 7.4.1-1 / 7.5-6).
func NewShadowingField(seed int64, utHeightM float64) (*ShadowingField, error) {
	if !isPositiveFinite(utHeightM) {
		return nil, fmt.Errorf("gölgeleme alanı: h_UT pozitif ve sonlu olmalı (%g)", utHeightM)
	}
	return &ShadowingField{
		// Tohum, akış ayracıyla karıştırılır: site yerleşimi ve frekans
		// atamasının (Sprint 1) akışlarından bağımsız kalır.
		seed: mix64(uint64(seed), shadowSalt),
		hUTm: utHeightM,
	}, nil
}

// SourceKey, 16 baytlık bir kimlikten (UUID) deterministik site anahtarı üretir.
//
// Ham bayt dizisi alır: radio paketi böylece uuid bağımlılığı taşımaz ve saf
// hesap katmanı olarak kalır.
func SourceKey(id [16]byte) uint64 {
	hi := uint64(id[0])<<56 | uint64(id[1])<<48 | uint64(id[2])<<40 | uint64(id[3])<<32 |
		uint64(id[4])<<24 | uint64(id[5])<<16 | uint64(id[6])<<8 | uint64(id[7])
	lo := uint64(id[8])<<56 | uint64(id[9])<<48 | uint64(id[10])<<40 | uint64(id[11])<<32 |
		uint64(id[12])<<24 | uint64(id[13])<<16 | uint64(id[14])<<8 | uint64(id[15])
	return mix64(hi, lo)
}

// ─── Ana çözümleme ───────────────────────────────────────────────────────────

// EnvironmentAt, verilen site ile nokta arasındaki LOS durumunu ve gölgeleme
// terimini birlikte çözer.
//
// Saf fonksiyondur: aynı (alan, kaynak, nokta) üçlüsü daima aynı sonucu verir
// (K10). Yığın ayırma yapmaz — sıcak yolda tick başına milyonlarca çağrılır.
func (f *ShadowingField) EnvironmentAt(src Source, p geo.Point) Environment {
	d2D := geo.Distance(src.ENU, p)

	// Izgara aralığı model başına tek değerle kurulur: NLOS dekorelasyonu.
	// LOS'a bağlı aralık kullanmak tavuk-yumurta sorunu doğururdu (LOS'u
	// çözmek için ızgara, ızgarayı kurmak için LOS gerekirdi). NLOS değeri
	// daha uzun, dolayısıyla daha muhafazakâr (daha güçlü korelasyon)
	// seçimdir. 5 dakikalık tick'te iki aralık arasındaki fark gözlenemez.
	grid := src.Model.DecorrelationM(false)

	// Konum ızgaraya kırpılır: aynı hücredeki noktalar aynı çekimi paylaşır.
	qx := quantize(p.X, grid)
	qy := quantize(p.Y, grid)

	// Tek konum hash'i → iki bağımsız akış.
	h := mix64(mix64(f.seed, src.Key), mix64(qx, qy))

	// 1) LOS kararı. Olasılık **gerçek** mesafeden hesaplanır (kırpılmış
	//    konumdan değil): P_LOS düzgün değişir, yalnızca "şans" mekânsal
	//    olarak korelasyonludur.
	uLOS := unitOpen(mix64(h, losSalt))
	los := uLOS < src.Model.LOSProbability(d2D, f.hUTm)

	// 2) Gölgeleme. σ, LOS durumuna göre standarttan okunur.
	sigma := src.Model.ShadowingSigmaDB(los)
	z := standardNormal(mix64(h, shadowSalt))

	return Environment{
		LOS:         los,
		ShadowingDB: clampSigma(sigma*z, sigma),
	}
}

// ShadowingDB, yalnızca gölgeleme terimini döndürür (dB).
// EnvironmentAt'ın kolaylık sarmalayıcısıdır.
func (f *ShadowingField) ShadowingDB(src Source, p geo.Point) float64 {
	return f.EnvironmentAt(src, p).ShadowingDB
}

// IsLOS, yalnızca görüş hattı durumunu döndürür.
// EnvironmentAt'ın kolaylık sarmalayıcısıdır.
func (f *ShadowingField) IsLOS(src Source, p geo.Point) bool {
	return f.EnvironmentAt(src, p).LOS
}

// ─── Sayısal yardımcılar ─────────────────────────────────────────────────────

// quantize, koordinatı ızgara aralığına kırpar ve hücre indisini döndürür.
// Negatif koordinatlar için de doğru çalışır (math.Floor, sıfıra doğru
// kırpmanın aksine, negatif tarafta da monotondur).
func quantize(v, gridM float64) uint64 {
	return uint64(int64(math.Floor(v / gridM)))
}

// splitmix64, tek turluk SplitMix64 karıştırıcısıdır.
//
// Ayırma yapmaz, dallanma içermez, iyi bit dağılımı verir. Kriptografik
// değildir — gerekmiyor; istenen tek şey konuma göre tekrarlanabilir ve
// desensiz bir bit akışıdır.
func splitmix64(x uint64) uint64 {
	x += splitmixGamma
	x = (x ^ (x >> 30)) * splitmixMulA
	x = (x ^ (x >> 27)) * splitmixMulB
	return x ^ (x >> 31)
}

// mix64, iki değeri tek bir hash'e karıştırır.
func mix64(a, b uint64) uint64 {
	return splitmix64(a ^ splitmix64(b))
}

// unitOpen, hash'i (0,1) açık aralığına taşır.
//
// Uç değerler dışarıda bırakılır: standardNormal içindeki log(u) terimi
// u = 0 için −Inf üretirdi.
func unitOpen(h uint64) float64 {
	return (float64(h>>mantissaShift) + 0.5) * mantissaScale
}

// standardNormal, hash'ten standart normal (μ=0, σ=1) örnek üretir.
//
// Box-Muller dönüşümü kullanılır:
//
//	z = √(−2·ln u₁) · cos(2π·u₂)
//
// Sabit maliyetlidir (reddetme döngüsü yok), bu yüzden deterministik ve
// öngörülebilir sürelidir — Marsaglia kutupsal yöntemi değişken sayıda çekim
// gerektirdiğinden tercih edilmemiştir.
func standardNormal(h uint64) float64 {
	u1 := unitOpen(h)
	u2 := unitOpen(splitmix64(h))
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// clampSigma, gölgelemeyi ±8σ ile sınırlar (emniyet sübabı).
func clampSigma(value, sigma float64) float64 {
	limit := maxAbsSigmaMultiple * sigma
	switch {
	case value > limit:
		return limit
	case value < -limit:
		return -limit
	default:
		return value
	}
}
