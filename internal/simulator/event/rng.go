// T-E02-13 — Olay katmanının deterministik rastgeleliği.
//
// Sprint 1 ve 2'de kurulan akış ayrımı deseni sürdürülür: aynı koşu tohumundan
// amaç başına bağımsız diziler türetilir, hiçbir paylaşılan üreteç yoktur.
// Böylece yeni bir rastgelelik tüketicisi eklemek mevcut olanların çıktısını
// **kaydırmaz**; Sprint 1'in 109 site / 327 hücre yerleşimi ve Sprint 2'nin
// ajan yörüngeleri bu paketin eklenmesiyle bit düzeyinde aynı kalır (K10).
//
// Koşu genelindeki akış numaraları:
//
//	1  site yerleşimi      (inventory, Sprint 1)
//	2  frekans ataması     (inventory, Sprint 1)
//	3  ajan yerleşimi      (agent,     Sprint 2)
//	4  günlük rutin        (agent,     Sprint 2)
//	5  hareket gürültüsü   (agent,     Sprint 2)
//	6  olay üretimi        (bu paket)  ← T-E02-13
//	7  enjeksiyon          (bu paket)  ← T-E02-17, ayrılmıştır
//
// # Karıştırıcının burada tekrarlanmasının nedeni
//
// Aynı SplitMix64 çekirdeği agent paketinde de vardır ama dışa açık değildir.
// Yalnızca bu paket kullanabilsin diye agent'ın public API'sini genişletmek,
// bir yardımcı fonksiyon uğruna paket sınırını gevşetmek olurdu. Kopya
// zararsızdır: iki paketin çekimleri purpose etiketiyle zaten ayrışır ve
// karıştırıcıların birbirine eşit kalması hiçbir değişmezin koşulu değildir —
// tek koşul, her birinin kendi içinde deterministik olmasıdır.
package event

// Olay katmanının akış numaraları (bkz. dosya başı).
const (
	streamEventGeneration uint64 = 6
	streamInjection       uint64 = 7 // T-E02-17'de kullanılacak
)

// Çekim ayraçları: aynı ajan-tick'te bağımsız çekimler için.
//
// agent paketindeki 0x9x bloğundan ayrı bir blok (0xA1..) seçilmiştir; iki
// paketin etiketleri hiçbir zaman çakışmaz.
const (
	purposeEventCount uint64 = 0xA1
	purposeEventType  uint64 = 0xA2
	purposeInjection  uint64 = 0xA3
	purposeInjectRule uint64 = 0xA4
	purposeInjectPick uint64 = 0xA5
)

// SplitMix64 karıştırma sabitleri (koşu genelinde aynı çekirdek).
const (
	splitmixGamma = 0x9E3779B97F4A7C15
	splitmixMulA  = 0xBF58476D1CE4E5B9
	splitmixMulB  = 0x94D049BB133111EB

	// mantissaShift / mantissaScale, 64 bitlik hash'i (0,1) açık aralığına
	// taşır. 52 bit kullanılır: 53 bitte en büyük hash tam 1,0 üretirdi ve
	// ters CDF'te u = 1 hiçbir zaman aşılamayan bir eşik yaratırdı.
	mantissaShift = 12
	mantissaScale = 1.0 / (1 << 52)
)

// splitmix64, tek turluk SplitMix64 karıştırıcısıdır. Ayırma yapmaz.
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

// hashUnit, (tohum, akış, amaç, ajan, tick) beşlisinden (0,1) aralığında
// deterministik bir değer üretir.
//
// **Durumsuzdur**: ajan başına üreteç durumu tutulmaz. Bu, tick başına çekilen
// sayı adedi ileride değişse bile mevcut akışların kaymamasını garanti eder ve
// 1000 goroutine'in kilitsiz, sıra bağımsız çalışmasını mümkün kılar.
func hashUnit(seed, stream, purpose uint64, agentID, tick int) float64 {
	h := mix64(seed, stream)
	h = mix64(h, purpose)
	h = mix64(h, uint64(agentID))
	h = mix64(h, uint64(int64(tick)))
	return (float64(h>>mantissaShift) + 0.5) * mantissaScale
}

// UnitHash, olay katmanının durumsuz çekimini dışa açar.
//
// `event/injector` alt paketi (ADR-09) aynı akış disiplinine uymak zorundadır
// ama karıştırıcıyı bir kez daha kopyalamak, aynı çekirdeğin **üçüncü**
// kopyası olurdu. Bunun yerine tek giriş noktası açılır: akış ve amaç
// numaraları yine bu dosyada tanımlıdır, dolayısıyla koşu genelindeki kayıt
// tek yerde kalır.
func UnitHash(seed, stream, purpose uint64, agentID, tick int) float64 {
	return hashUnit(seed, stream, purpose, agentID, tick)
}

// InjectionStream, enjeksiyon aşamasının akış numarasıdır (T-E02-17).
func InjectionStream() uint64 { return streamInjection }

// Enjeksiyon çekim ayraçları (alt pakete açık).
func InjectionPurposes() (trigger, rule, pick uint64) {
	return purposeInjection, purposeInjectRule, purposeInjectPick
}
