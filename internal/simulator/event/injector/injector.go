// Package injector, üretilmiş olaylara manipülasyon enjekte eder (T-E02-17,
// ADR-09).
//
// Boru hattındaki yeri:
//
//	ajan hareketi → best-server → olay üretimi → [ENJEKSİYON] → Kafka publish
//
// # Neden simülatör içinde
//
// ADR-09 üç seçenekten A'yı seçti. Kafka middleware (B) Kafka semantiğini
// kirletir; veritabanına doğrudan bozuk kayıt yazmak (C) ise Kafka akışını
// atlar ve S4'ün gerçek yolu test edilmemiş olur. Simülatör içi aşama, bozuk
// kaydın **tam olarak temiz kayıtla aynı yoldan** geçmesini sağlar.
//
// # Kör test buradan doğar
//
// Enjeksiyon etiketi (`injected_rule`) yalnızca `ground_truth`'a yazılır.
// `hts_records` bu bilgiyi hiçbir alanında taşımaz — S4 hangi kaydın enjekte
// edildiğini bilemez, dolayısıyla bütünlük tespiti de kör testtir.
// Precision/recall'ü yalnızca etiketi görebilen S3b hesaplar.
//
// # Kural 4'ün özel durumu
//
// Kayıt boşluğu, olayın **silinmesidir**: `hts.records`'a hiçbir şey gitmez,
// yalnızca ground truth `injected_rule = 4` ile yazılır. Diğer dört kural
// kaydı bozar ama yayınlar.
package injector

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Rule, manipülasyon kuralıdır (ground_truth.injected_rule).
type Rule int

const (
	// RuleFakeCell, envanterde bulunmayan bir hücre kimliği yazar.
	// S6 karşılığı: envanter kuralı.
	RuleFakeCell Rule = 1
	// RuleJump, kaydı çok uzaktaki bir hücreye taşır.
	// S6 karşılığı: hız kuralı (max_velocity_kmh).
	RuleJump Rule = 2
	// RuleTime, zaman damgasını geriye kaydırır.
	// S6 karşılığı: zaman kuralı.
	RuleTime Rule = 3
	// RuleGap, kaydı tamamen siler (yalnızca ground truth kalır).
	// S6 karşılığı: yörünge kuralı (eksik halka).
	RuleGap Rule = 4
	// RuleDeviceSwap, cihaz takma adını başka bir cihazla değiştirir.
	// S6 karşılığı: aktivite kuralı.
	RuleDeviceSwap Rule = 5
)

// AllRules, tanımlı kuralların artan sıralı listesidir.
var AllRules = [...]Rule{RuleFakeCell, RuleJump, RuleTime, RuleGap, RuleDeviceSwap}

// Valid, kuralın tanımlı olup olmadığını bildirir.
func (r Rule) Valid() bool { return r >= RuleFakeCell && r <= RuleDeviceSwap }

// String, kuralın okunabilir adını döndürür.
func (r Rule) String() string {
	switch r {
	case RuleFakeCell:
		return "sahte hücre"
	case RuleJump:
		return "atlama"
	case RuleTime:
		return "zaman"
	case RuleGap:
		return "kayıt boşluğu"
	case RuleDeviceSwap:
		return "cihaz değişimi"
	default:
		return fmt.Sprintf("Rule(%d)", int(r))
	}
}

// CellRef, enjeksiyonun ihtiyaç duyduğu en küçük hücre görünümüdür.
type CellRef struct {
	ID  uuid.UUID
	ENU geo.Point
}

// Config, enjeksiyon aşamasının parametreleridir (senaryo config'i).
type Config struct {
	// Rate, enjeksiyon oranıdır (integrity.injection_rate, varsayılan 0.02).
	Rate float64
	// Weights, kural seçim ağırlıklarıdır (integrity.rule_weights).
	Weights map[int]float64
	// TimeShift, kural 3'ün zaman damgasını kaydırma miktarıdır.
	TimeShift time.Duration
}

// DefaultTimeShift, kural 3'ün varsayılan kaydırma miktarıdır.
//
// İki saat geriye kaydırma, olayı aynı abonenin önceki kayıtlarının **öncesine**
// düşürür: ajan başına günde ~10 olay olduğundan ortalama olay aralığı ~2,4
// saattir (T-E02-13 profili). Kaydırma bu aralıktan küçük seçilseydi kayıt
// sırası çoğu zaman bozulmaz ve kural gözlemlenemez olurdu; değer profilden
// türetilmiştir, keyfi değildir.
const DefaultTimeShift = -2 * time.Hour

// Injector, olaylara manipülasyon uygular.
//
// Değişmezdir; ajan goroutine'leri tarafından paylaşılabilir. Rastgelelik
// durumsuzdur: (tohum, ajan, tick) üçlüsünden türetilir, bu yüzden aynı koşu
// aynı enjeksiyonları üretir (K10).
type Injector struct {
	seed    uint64
	cfg     Config
	cells   []CellRef
	cumRule []float64 // kümülatif kural ağırlıkları
	rules   []Rule
}

// New, enjeksiyon aşamasını kurar.
//
// cells, kural 2'nin uzak hücre seçimi için envanterin tamamıdır.
func New(seed int64, cfg Config, cells []CellRef) (*Injector, error) {
	if cfg.Rate < 0 || cfg.Rate > 1 || math.IsNaN(cfg.Rate) {
		return nil, fmt.Errorf("enjeksiyon: oran [0,1] aralığında olmalı (%g)", cfg.Rate)
	}
	if len(cells) == 0 {
		return nil, fmt.Errorf("enjeksiyon: hücre envanteri zorunlu (kural %d için)", RuleJump)
	}
	if cfg.TimeShift == 0 {
		cfg.TimeShift = DefaultTimeShift
	}

	in := &Injector{seed: uint64(seed), cfg: cfg, cells: append([]CellRef(nil), cells...)}

	// Envanter kimliğe göre sıralanır: uzak hücre seçimi koşudan koşuya aynı
	// olmalıdır (K10).
	sort.Slice(in.cells, func(i, j int) bool {
		return in.cells[i].ID.String() < in.cells[j].ID.String()
	})

	if err := in.buildRuleTable(); err != nil {
		return nil, err
	}
	return in, nil
}

// buildRuleTable, kural ağırlıklarından kümülatif seçim tablosu kurar.
func (in *Injector) buildRuleTable() error {
	total := 0.0
	for _, r := range AllRules {
		w, ok := in.cfg.Weights[int(r)]
		if !ok {
			w = 1.0 / float64(len(AllRules)) // ağırlık verilmemişse eşit dağılım
		}
		if w < 0 || math.IsNaN(w) {
			return fmt.Errorf("enjeksiyon: kural %d ağırlığı negatif olamaz (%g)", r, w)
		}
		if w == 0 {
			continue
		}
		total += w
		in.rules = append(in.rules, r)
		in.cumRule = append(in.cumRule, total)
	}
	if total <= 0 {
		return fmt.Errorf("enjeksiyon: tüm kural ağırlıkları sıfır")
	}
	for i := range in.cumRule {
		in.cumRule[i] /= total
	}
	return nil
}

// Rate, yapılandırılmış enjeksiyon oranıdır.
func (in *Injector) Rate() float64 { return in.cfg.Rate }

// Apply, kayıt çiftine enjeksiyon uygular.
//
// Temiz olaylarda çift değişmeden döner (`injected_rule` nil kalır).
// Kapsama dışı olaylarda enjeksiyon yapılmaz: ortada bozulacak bir kayıt
// yoktur.
//
// pseudo, kural 5'in cihaz takma adını yeniden üretmesi için gereklidir.
func (in *Injector) Apply(pair event.Pair, pseudo *event.Pseudonymizer, agentID, tick, seq int) event.Pair {
	if pair.Record == nil {
		return pair
	}

	slot := tick*maxSeqPerTick + seq
	trigger, ruleP, pick := event.InjectionPurposes()

	if event.UnitHash(in.seed, event.InjectionStream(), trigger, agentID, slot) >= in.cfg.Rate {
		return pair
	}

	rule := in.pickRule(event.UnitHash(in.seed, event.InjectionStream(), ruleP, agentID, slot))
	label := int(rule)
	pair.Truth.InjectedRule = &label

	switch rule {
	case RuleFakeCell:
		pair.Record.CellID = fakeCellID(pair.Record.EventID)
	case RuleJump:
		pair.Record.CellID = in.farthestCell(pair.Record.CellID).ID
	case RuleTime:
		pair.Record.Time = pair.Record.Time.Add(in.cfg.TimeShift)
	case RuleGap:
		pair.Record = nil // kayıt silinir; ground truth etiketli kalır
	case RuleDeviceSwap:
		other := in.otherAgent(agentID,
			event.UnitHash(in.seed, event.InjectionStream(), pick, agentID, slot))
		pair.Record.PseudoIMEI = pseudo.PseudoIMEIFor(event.IMEI(other))
	}
	return pair
}

// maxSeqPerTick, tick içi olay sırasının çekim yuvasına katılma çarpanıdır.
//
// Aynı tick'in farklı olayları bağımsız çekim görmelidir; olay üretecinin üst
// sınırıyla (16) aynı değerdir.
const maxSeqPerTick = 16

// pickRule, kümülatif ağırlık tablosundan kural seçer.
func (in *Injector) pickRule(u float64) Rule {
	for i, threshold := range in.cumRule {
		if u < threshold {
			return in.rules[i]
		}
	}
	return in.rules[len(in.rules)-1]
}

// fakeCellNamespace, envanterde bulunmayan hücre kimlikleri için ayrı bir
// namespace'tir.
//
// Gerçek hücre kimlikleri `hts-kga/cell` namespace'inden üretilir; buradan
// üretilen kimlik hiçbir zaman envantere düşmez — kural 1'in tanımı budur.
var fakeCellNamespace = uuid.NewSHA1(uuid.NameSpaceOID, []byte("hts-kga/injected-fake-cell"))

// fakeCellID, olaya bağlı deterministik sahte hücre kimliği üretir.
func fakeCellID(eventID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(fakeCellNamespace, eventID[:])
}

// farthestCell, verilen hücreden en uzaktaki envanter hücresini döndürür.
//
// Kural 2, kaydı fiziksel olarak ulaşılamayacak bir konuma taşır. Ne kadar
// uzağa taşınabileceği envanterin çapıyla sınırlıdır: kentsel alanda (yarıçap
// 5 km) en büyük ayrım ~10 km'dir. Tespit edilebilirlik, bozulan kaydın komşu
// kayıtlarla arasındaki zaman farkına bağlıdır ve K7'de **recall raporlanır**,
// eşiğe bağlanmaz — bu sınır bilinçlidir.
func (in *Injector) farthestCell(from uuid.UUID) CellRef {
	origin, ok := in.lookup(from)
	if !ok {
		return in.cells[0]
	}

	best, bestDist := in.cells[0], -1.0
	for _, c := range in.cells {
		if d := geo.Distance(origin.ENU, c.ENU); d > bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// lookup, hücre kimliğinden referansı bulur.
func (in *Injector) lookup(id uuid.UUID) (CellRef, bool) {
	for _, c := range in.cells {
		if c.ID == id {
			return c, true
		}
	}
	return CellRef{}, false
}

// otherAgent, verilen ajandan farklı bir ajan kimliği seçer.
//
// Cihaz değişimi, aynı abonenin başka bir cihazdan görünmesidir; kimlik
// çakışırsa manipülasyon gözlemlenemez olurdu.
func (in *Injector) otherAgent(agentID int, u float64) int {
	const span = 1000
	offset := 1 + int(u*float64(span-1))
	return agentID + offset
}
