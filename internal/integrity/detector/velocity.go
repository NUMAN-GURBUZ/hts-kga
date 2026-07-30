// T-E05-05 — Kural 2: kinematik tutarlılık (ADR-27 ClassBatch, ADR-29).
//
// # Kuralın iddiası
//
// Abonenin ardışık iki kaydı arasındaki ima edilen hız fiziksel üst sınırı
// aşıyor: kayıtlardan biri manipüle edilmiş olmalı.
//
// # Spesifikasyon dondurulmuştur (ADR-29)
//
// Bu kural, K7'nin geçip geçmemesini belirleyen kuraldır ve etiketli veri
// elimizdedir. Bu yüzden tanımı **kod yazılmadan önce** ADR-29'da dondurulmuştu:
//
//  1. İhlal geçişin özelliğidir, kaydın değil.
//  2. Eşik 300 km/h (BÖLÜM C.1) — korunur.
//  3. Δt = 0 ∧ d > 0 → gerçekten imkânsız; margin kapılır.
//  4. Kural 1 kayıtları zincirden çıkarılır (konumu bilinmiyor).
//  5. Atıf: yerel destek — hangi uç yerel yörüngeden uzaklaşıyorsa o suçlanır.
//  6. Karar verilemezse **bulgu üretilmez**.
//  7. Öncelik: kural 1/5/3 tarafından talep edilmiş olaylar değerlendirilmez.
//
// # Neden atıf kuralın tanımının parçası
//
// Ölçüm (Sprint 5 verisi): naif kayıt-bazlı kural precision **%36,5** (kentsel)
// / **%42,1** (kırsal). Neden yapısaldır — hız ihlali bir *çiftin* özelliğidir ve
// kayıt bazlı kural çiftin iki ucunu da işaretler; biri enjekte, diğeri temizdir.
// Kentselde birebir doğrulandı: `58 (kural 2) + 21 (kural 3) = 79 = temiz bulgu`.
//
// Yerel destek atfıyla precision **%71,8 / %83,4**; ADR-28 önceliğiyle ~%80/%90.
//
// # Recall'un tavanı fizikseldir
//
// Ortalama olay aralığı 2,4 saat; kentsel envanter çapı ~10 km, kırsal ~40 km.
// 300 km/h'yi aşmak için Δt < 2 dk (kentsel) / < 8 dk (kırsal) gerekiyor ve
// damgalar 5 dakikalık ızgarada. Atlamaların büyük kısmı **fiziksel olarak
// mümkündür**. Recall %4,7–12,3'te kalır; K7 recall'u eşiğe bağlamaz, raporlar.
//
// # Kapatılmış yol
//
// TA / r_max tutarlılığı (statik, Δt'den bağımsız sinyal) dört koşuda **sıfır
// ihlal** verdi: r_max, TA mesafelerine göre çok büyük. Denenmeyecek.

package detector

import (
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// CellPositions, hücre konumlarını ENU düzleminde döndüren yüzeydir.
//
// `internal/integrity/source.Inventory` bunu uygular. Arayüz olarak
// tanımlanması kuralın birim testinde Redis'e ihtiyaç duymamasını sağlar.
type CellPositions interface {
	Position(id uuid.UUID) (geo.Point, bool)
}

// zeroDistanceEpsilonM, "aynı yerde" sayılan mesafe eşiğidir (metre).
//
// Aynı tick'in olayları aynı gözlemden gelir ve aynı hücreyi gösterir → mesafe
// tam 0. Kayan nokta gürültüsüne pay bırakmak için 1 m kullanılır; envanterdeki
// en yakın iki site arasındaki mesafe yüzlerce metredir, bu yüzden eşik gerçek
// bir ayrımı gizlemez.
const zeroDistanceEpsilonM = 1.0

// VelocityRule, kinematik olarak imkânsız geçişleri tespit eder.
//
// Durumsuzdur: her abone dizisi bağımsız değerlendirilir.
type VelocityRule struct {
	cells        CellPositions
	maxKMH       float64
	marginCap    float64
	claimedCheck func(uuid.UUID) bool
}

// NewVelocityRule, kuralı kurar.
//
// maxKMH, `integrity.max_velocity_kmh` (300). Ölçümden önce beyan edilmiş bir
// parametredir ve sonuca göre değiştirilmez (ADR-29/2).
func NewVelocityRule(cells CellPositions, maxKMH, marginCap float64) (*VelocityRule, error) {
	if cells == nil {
		return nil, errRuleSetup(integrityrule.Velocity, "hücre konumları zorunlu")
	}
	if !(maxKMH > 0) {
		return nil, errRuleSetup(integrityrule.Velocity,
			fmt.Sprintf("hız üst sınırı pozitif olmalı (%g)", maxKMH))
	}
	if !(marginCap > 0) {
		marginCap = DefaultMarginCap
	}
	return &VelocityRule{cells: cells, maxKMH: maxKMH, marginCap: marginCap}, nil
}

// ID, kural kimliğidir.
func (r *VelocityRule) ID() integrityrule.ID { return integrityrule.Velocity }

// link, zincirdeki bir kaydın konumuyla birlikte tutulan hâlidir.
type link struct {
	rec Record
	pos geo.Point
}

// Evaluate, abonenin dizisini geçiş geçiş inceler.
//
// # Neden geçiş üzerinden döngü, kayıt üzerinden değil
//
// ADR-28/3: bir geçiş → en çok bir bulgu. Kayıt üzerinden dönülse aynı ihlal iki
// kez değerlendirilir ve iki uç da işaretlenebilirdi — precision'ı %50'nin
// altına indiren tam olarak bu.
func (r *VelocityRule) Evaluate(seq Sequence) []Hit {
	chain := r.buildChain(seq)
	if len(chain) < 2 {
		return nil
	}

	// Suçlanan kayıt → o kayda atfedilen en yüksek hız.
	type blame struct {
		link      link
		peer      uuid.UUID
		velocity  float64
		distanceM float64
		deltaS    float64
		zeroDelta bool
	}
	blamed := make(map[uuid.UUID]blame)
	order := make([]uuid.UUID, 0, 2)

	for i := 0; i+1 < len(chain); i++ {
		a, b := chain[i], chain[i+1]

		distanceM := geo.Distance(a.pos, b.pos)
		deltaS := b.rec.Time.Sub(a.rec.Time).Seconds()
		velocity, zeroDelta := impliedVelocityKMH(distanceM, deltaS)
		if velocity <= r.maxKMH {
			continue
		}

		culprit, peer, ok := r.attribute(chain, i)
		if !ok {
			// Karar verilemez: adli bir sistem iki tarafı birlikte suçlamaz
			// (ADR-29/6). Bulgu üretilmez.
			continue
		}

		if prev, seen := blamed[culprit.rec.EventID]; seen && prev.velocity >= velocity {
			continue
		} else if !seen {
			order = append(order, culprit.rec.EventID)
		}
		blamed[culprit.rec.EventID] = blame{
			link: culprit, peer: peer, velocity: velocity,
			distanceM: distanceM, deltaS: deltaS, zeroDelta: zeroDelta,
		}
	}

	hits := make([]Hit, 0, len(order))
	for _, eventID := range order {
		bl := blamed[eventID]
		evidence := NewEvidence(integrityrule.Velocity, r.marginCap).
			UUID("record_cell", bl.link.rec.CellID).
			UUID("peer_event_id", bl.peer).
			Float("distance_m", bl.distanceM).
			Float("delta_s", bl.deltaS).
			Float("velocity_kmh", bl.velocity).
			Float("threshold_kmh", r.maxKMH).
			Str("attribution", "local_support").
			Bool("zero_interval", bl.zeroDelta).
			MustBuild()

		// `margin` burada kapılır, motorun güvenlik ağına bırakılmaz.
		//
		// Δt = 0 durumunda oran +Inf'tir ve kural şema-geçersiz bir isabet
		// üretmemelidir: motorun geçerlilik denetimi onu düzeltebilir ama
		// kuralın kendi çıktısı da sözleşmeye uymalı. PBT #15 bu satır
		// olmadan +Inf yakaladı.
		margin, _ := ClampMargin(bl.velocity/r.maxKMH, r.marginCap)

		hits = append(hits, Hit{
			Rule:     integrityrule.Velocity,
			EventID:  eventID,
			Time:     bl.link.rec.Time,
			Scenario: bl.link.rec.Scenario,
			Margin:   margin,
			Evidence: evidence,
		})
	}
	return hits
}

// buildChain, konumu bilinen kayıtları sırayla toplar.
//
// Envanterde olmayan hücreye işaret eden kayıtlar **çıkarılır** (ADR-29/4):
// konumu bilinmiyor ve zincire NULL konumla girerse ardışık çiftler kırılır,
// komşu geçişler yanlış hesaplanır. O kayıt zaten kural 1 tarafından talep
// edilmiştir.
func (r *VelocityRule) buildChain(seq Sequence) []link {
	chain := make([]link, 0, len(seq.Records))
	for _, rec := range seq.Records {
		pos, ok := r.cells.Position(rec.CellID)
		if !ok {
			continue
		}
		chain = append(chain, link{rec: rec, pos: pos})
	}
	return chain
}

// attribute, ihlal eden (i, i+1) geçişinin hangi ucuna bulgu yazılacağını
// belirler (ADR-29/5).
//
//	Gelen kenar:  d(B, p2) > d(A, p2)   →  B suçlanır
//	Giden kenar:  d(A, n2) > d(B, n2)   →  A suçlanır
//
// Sezgi: atlanan kayıt envanterin uzak bir hücresine taşınmıştır; abonenin
// komşu kayıtları gerçek konumun etrafındadır. Dolayısıyla **dış** komşuya olan
// mesafe, suçlu uçta belirgin biçimde büyüktür.
//
// Karşılaştırma katı eşitsizliktir. Dış komşu yoksa veya mesafeler eşitse karar
// verilemez ve bulgu üretilmez (ADR-29/6).
func (r *VelocityRule) attribute(chain []link, i int) (culprit link, peer uuid.UUID, ok bool) {
	a, b := chain[i], chain[i+1]

	// Gelen kenar: B'nin dış komşusu p2 = chain[i-1].
	if i-1 >= 0 {
		outer := chain[i-1].pos
		if geo.Distance(b.pos, outer) > geo.Distance(a.pos, outer) {
			return b, a.rec.EventID, true
		}
	}

	// Giden kenar: A'nın dış komşusu n2 = chain[i+2].
	if i+2 < len(chain) {
		outer := chain[i+2].pos
		if geo.Distance(a.pos, outer) > geo.Distance(b.pos, outer) {
			return a, b.rec.EventID, true
		}
	}

	return link{}, uuid.Nil, false
}

// impliedVelocityKMH, iki kayıt arasındaki ima edilen hızı döndürür.
//
// İkinci dönüş değeri Δt = 0 durumunu bildirir. Δt = 0 ve mesafe > 0
// gerçekten imkânsızdır (sonsuz hız); mesafe de 0 ise hareket yoktur.
//
// Negatif Δt burada oluşamaz: dizi olay zamanına göre sıralıdır ve
// `Sequence.Validate` bunu denetler.
func impliedVelocityKMH(distanceM, deltaS float64) (kmh float64, zeroDelta bool) {
	if deltaS <= 0 {
		if distanceM > zeroDistanceEpsilonM {
			return math.Inf(1), true
		}
		return 0, true
	}
	return (distanceM / 1000.0) / (deltaS / 3600.0), false
}
