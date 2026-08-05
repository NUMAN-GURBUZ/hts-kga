// T-E02-13 — Poisson olay üreteci (saatlik λ).
//
// Bir ajan, her tick'te şebekeye kayıt düşürmez; olaylar seyrek ve düzensizdir.
// Sayım süreci Poisson olarak modellenir: olaylar birbirinden bağımsız gelir ve
// yoğunlukları günün saatine göre değişir. Bu, çağrı/veri kaydı üretiminin
// standart modelidir ve tek parametreyle (λ) tanımlanır.
//
// # Yoğunluk profili
//
// Plan λ(h)'yi sayısal olarak vermez ("Poisson olay üreteci (saate göre λ)").
// Profil, planın kendi çıpasından türetilmiştir: ADR-14 toplam olay hacmini
// ~300.000 olarak verir; 1000 ajan × 30 gün bölümüyle **ajan başına günde 10
// olay** çıkar. 24 saatlik vektör tam olarak bu toplamı verecek şekilde
// normalize edilmiştir:
//
//	E[günlük olay] = Σ_h ( tickPerHour · λ(h) · Δt ) = Σ_h λ(h) = 10.00
//
// Şekil, yerleşik günlük hareketlilik örüntüsünü izler: gece 00–05 dip,
// 08–09 sabah ve 17–19 akşam tepeleri, gündüz platosu. Hafta içi ve hafta sonu
// için ayrı profil **yoktur**: plan yalnızca saatlik λ tanımlar, haftalık ayrım
// uydurmak savunulamayacak bir parametre eklemek olurdu. Haftalık periyodisite
// zaten ajan rutininde (T-E02-08) modellenmiştir; orada konum değişir, olay
// yoğunluğu değil.
//
// # Kapsama bu katmanın işi değildir
//
// Üreteç ham olay sayısını verir; alınan gücün alıcı duyarlılığını sağlayıp
// sağlamadığına bakmaz. Kapsama dışı tick'te olay yazılmaması ADR-08'in ayrı
// bir katmanıdır (T-E02-15/16). İki mekanizmanın burada karışması, gerçekleşen
// olay sayısı beklenenden düşük çıktığında hatanın Poisson'da mı kapsamada mı
// olduğunu ayırt edilemez hâle getirirdi.
package event

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/agent"
)

// minutesPerHour, tick indeksinden saat türetmek için.
const minutesPerHour = 60

// DailyEventTarget, ajan başına beklenen günlük olay sayısıdır.
//
// ADR-14'ün ~300.000 olay hacminden türetilmiştir (1000 ajan × 30 gün × 10).
// diurnalLambda bu değere normalize edilmiştir; ikisi birlikte değişir.
const DailyEventTarget = 10.0

// normalisationTolerance, profil toplamının hedefe uyma toleransıdır.
//
// Kayan nokta birikimi dışında sapmaya izin verilmez: profil elle yazılmış
// sabit bir vektördür, toplamı tesadüfe bırakılamaz.
const normalisationTolerance = 1e-9

// diurnalLambda, saat başına olay yoğunluğudur (olay/saat/ajan).
//
// İndeks gün içindeki saattir [0,24). Toplamı DailyEventTarget'a eşittir.
// Senaryo config'inde **değildir**: dört senaryo da (A–D) aynı insan
// davranışını varsayar; senaryoları ayıran şey morfoloji ve TA'dır, günün
// saatine göre arama alışkanlığı değil.
var diurnalLambda = [24]float64{
	0.12, 0.08, 0.06, 0.05, 0.06, 0.10, // 00–05  gece dibi
	0.22, 0.45, 0.80, 0.75, 0.55, 0.50, // 06–11  sabah yükselişi ve tepesi
	0.50, 0.50, 0.45, 0.45, 0.50, 0.72, // 12–17  gündüz platosu
	0.80, 0.72, 0.58, 0.48, 0.36, 0.20, // 18–23  akşam tepesi ve iniş
}

// maxEventsPerTick, ters CDF döngüsünün üst sınırıdır.
//
// Güvenlik sınırıdır, modelin parçası değil: en yoğun saatte tick başına
// λ ≈ 0,067 olduğundan 16 olaya ulaşma olasılığı 10⁻³⁰'un altındadır. Sınır
// yalnızca kayan nokta birikimi u'yu kümülatife hiç ulaştıramazsa döngünün
// sonsuza gitmesini engeller.
const maxEventsPerTick = 16

// Generator, ajan-tick başına olay sayısı üreten Poisson üretecidir.
//
// Değişmezdir; 1000 goroutine tarafından kilitsiz paylaşılabilir. Durum
// tutmaz: sayım yalnızca (tohum, ajan, tick) üçlüsünün fonksiyonudur, bu
// yüzden goroutine sırası çıktıyı etkilemez (K10).
type Generator struct {
	seed          uint64
	clock         agent.Clock
	lambdaPerTick [24]float64
}

// NewGenerator, koşu tohumu ve zaman modelinden bir olay üreteci kurar.
//
// Profilin normalizasyonu burada da denetlenir: bozuk bir vektörle koşu
// başlatmaktansa kurulumda düşmek yeğdir, çünkü hata ancak koşu bittikten
// sonra toplam olay sayısında fark edilirdi.
func NewGenerator(seed int64, clock agent.Clock) (*Generator, error) {
	if clock.TickMinutes() <= 0 {
		return nil, fmt.Errorf("olay üreteci: tick uzunluğu pozitif olmalı (%d dk)",
			clock.TickMinutes())
	}
	if err := validateProfile(diurnalLambda); err != nil {
		return nil, err
	}

	g := &Generator{seed: uint64(seed), clock: clock}
	dt := float64(clock.TickMinutes()) / minutesPerHour
	for h, lambda := range diurnalLambda {
		g.lambdaPerTick[h] = lambda * dt
	}
	return g, nil
}

// validateProfile, yoğunluk vektörünün geçerli ve normalize olduğunu denetler.
func validateProfile(profile [24]float64) error {
	total := 0.0
	for h, lambda := range profile {
		if !(lambda > 0) || math.IsInf(lambda, 0) {
			return fmt.Errorf("olay üreteci: λ(%d) pozitif ve sonlu olmalı (%g)", h, lambda)
		}
		total += lambda
	}
	if math.Abs(total-DailyEventTarget) > normalisationTolerance {
		return fmt.Errorf("olay üreteci: profil toplamı %.12f, beklenen %.2f (tolerans %g)",
			total, DailyEventTarget, normalisationTolerance)
	}
	return nil
}

// Count, verilen ajan-tick'te üretilen olay sayısını döndürür.
//
// Kapsama durumundan bağımsızdır (bkz. paket açıklaması): ham Poisson
// çekilişidir. Kapsama dışı tick'te olayın yazılmaması çağıranın işidir.
func (g *Generator) Count(agentID, tick int) int {
	hour := g.clock.MinuteOfDay(tick) / minutesPerHour
	u := hashUnit(g.seed, streamEventGeneration, purposeEventCount, agentID, tick)
	return poissonCount(g.lambdaPerTick[hour], u)
}

// LambdaPerHour, verilen saatteki yoğunluğu döndürür (olay/saat/ajan).
func (g *Generator) LambdaPerHour(hour int) float64 { return diurnalLambda[hour] }

// LambdaPerTick, verilen saatteki tick başına Poisson ortalamasını döndürür.
func (g *Generator) LambdaPerTick(hour int) float64 { return g.lambdaPerTick[hour] }

// DiurnalProfile, saatlik yoğunluk vektörünün bir kopyasını döndürür.
//
// Dizi değer tipi olduğundan dönen kopya çağıran tarafından değiştirilse bile
// profil etkilenmez.
func DiurnalProfile() [24]float64 { return diurnalLambda }

// poissonCount, ters kümülatif dağılımla Poisson çekilişi yapar.
//
// Tek bir tekdüze sayı tüketir. Knuth'un çarpım yöntemi de kullanılabilirdi
// ama o, λ'ya bağlı **değişken sayıda** tekdüze sayı ister; durumsuz hash
// tabanlı bir üreteçte bu, tick başına kaç hash çekildiğini λ'ya bağlı kılar
// ve akışı kırılgan hâle getirir. Ters CDF'te tüketim daima birdir.
//
// Yineleme, p_k = p_{k−1}·λ/k özyinelemesini kullanır: üstel ve faktöriyel
// yalnızca bir kez, k = 0 için hesaplanır.
func poissonCount(lambda, u float64) int {
	if lambda <= 0 {
		return 0
	}
	p := math.Exp(-lambda) // P(X = 0)
	cum := p
	k := 0
	for u > cum && k < maxEventsPerTick {
		k++
		p *= lambda / float64(k)
		cum += p
	}
	return k
}
