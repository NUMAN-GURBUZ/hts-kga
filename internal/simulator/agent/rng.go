// T-E02-07..09 — Deterministik rastgelelik altyapısı.
//
// Sprint 1'de kurulan akış ayrımı deseni sürdürülür: aynı koşu tohumundan
// amaç başına bağımsız diziler türetilir. Böylece yeni bir rastgelelik
// tüketicisi eklemek, mevcut olanların çıktısını **kaydırmaz** — Sprint 1'in
// 109 site / 327 hücre çıktısı Sprint 2 eklemeleriyle değişmez (K10).
//
// Akış numaraları koşu genelinde benzersizdir:
//
//	1  site yerleşimi      (inventory, Sprint 1)
//	2  frekans ataması     (inventory, Sprint 1)
//	3  ajan yerleşimi      (bu paket)
//	4  günlük rutin        (bu paket)
//	5  hareket gürültüsü   (bu paket)
package agent

import "math/rand/v2"

// Rastgelelik akışları (bkz. paket açıklaması).
const (
	streamAgentPlacement uint64 = 3
	streamRoutine        uint64 = 4
	streamMobility       uint64 = 5
)

// newRNG, koşu tohumundan verilen akış için deterministik bir üreteç türetir.
//
// Kurulum aşamasında kullanılır (yerleşim, rutin kişiselleştirme); sıcak
// yolda değil. Tick başına rastgelelik hashNoise ile durumsuz üretilir.
func newRNG(seed int64, stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), stream))
}

// SplitMix64 karıştırma sabitleri (Sprint 2'de radyo katmanıyla aynı çekirdek).
const (
	splitmixGamma = 0x9E3779B97F4A7C15
	splitmixMulA  = 0xBF58476D1CE4E5B9
	splitmixMulB  = 0x94D049BB133111EB

	// mantissaShift / mantissaScale, 64 bitlik hash'i (0,1) açık aralığına
	// taşır. 52 bit kullanılır: 53 bitte en büyük hash tam 1,0 üretirdi.
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

// hashUnit, (tohum, ajan, tick, amaç) dörtlüsünden (0,1) aralığında
// deterministik bir değer üretir.
//
// **Durumsuzdur**: ajan başına üreteç durumu tutulmaz. Bu, tick başına
// çekilen sayı adedi ileride değişse bile (ör. Sprint 3'te Poisson olayları
// eklendiğinde) mevcut akışların kaymamasını garanti eder.
func hashUnit(seed uint64, agentID, tick int, purpose uint64) float64 {
	h := mix64(seed, purpose)
	h = mix64(h, uint64(agentID))
	h = mix64(h, uint64(int64(tick)))
	return (float64(h>>mantissaShift) + 0.5) * mantissaScale
}
