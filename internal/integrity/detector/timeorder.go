// T-E05-03 — Kural 3: zaman tutarlılığı (ADR-27 ClassStream).
//
// # Kuralın iddiası
//
// Kaydın olay zamanı, aynı abonenin daha önce **görülmüş** en yüksek olay
// zamanından geriye gidiyor. Akış ilerlerken zaman geriye gitmez.
//
// # Kanıt yalnızca akış modunda var — ölçüldü
//
// Kaydırılmış damga, `hts_records`'a yazıldıktan sonra zamana göre
// sıralandığında **sessizce yeni yerine oturur**: kaydırılmış olduğuna dair hiç
// iz kalmaz. Sprint 5 verisinde toplu moddaki tek dolaylı sinyal ("aynı abone +
// aynı damga, farklı hücre") 265 bulgu üretiyor ve bunların yalnızca 22'si
// kural 3 — **precision %8,3**.
//
// Akış modunda kanıt yapısaldır ve precision **%100**'dür: damgaya dokunan tek
// kural 3'tür, dolayısıyla başka hiçbir kural bu sinyali tetikleyemez.
//
// # Yük taşıyan varsayım
//
// Üretim sırası abone başına olay zamanında monoton artmalıdır. Simülatör
// tick-major döngü kuruyor, tick içi damgalar eşit, Kafka anahtarı
// `pseudo_msisdn` olduğu için abone başına sıra tek partition'da korunur.
//
// Varsayım `tests/integration/stream_order_test.go` ile teste bağlıdır:
// monotonluk ihlallerinin kümesi tam olarak `injected_rule = 3` kümesi olmalı.
// Sessizce kırılırsa kural 3, %100 precision'lı bir kuraldan yüzlerce yanlış
// pozitif üreten bir kurala dönüşür ve bunu fark etmenin tek yolu etiketlere
// bakmaktır — yani üretimde kör kalırız.
//
// # Recall'un tavanı: %64,8 (fiziksel)
//
// Kaydırma 2 saat, ortalama olay aralığı 2,4 saat (T-E02-13). Önceki olay 2
// saatten eskiyse kaydırılmış damga hâlâ ondan sonra kalır ve sıra bozulmaz.
// Sprint 5 verisinde kural 3 olaylarının %64,8'inde önceki olay 2 saat içinde.
// Bu bir tasarım kusuru değil, ölçülmüş bir bilgi sınırıdır; K7 recall'u eşiğe
// bağlamaz.

package detector

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// TimeOrderRule, abone başına olay zamanı monotonluğunu denetler.
//
// Durum abone başına bir zaman damgası ve bir olay kimliğidir: 1000 abone ×
// ~40 bayt ≈ 40 KB. **Kayıt sayısıyla büyümez** — 300.000 olaylık koşu ile
// 3.000.000 olaylık koşu aynı belleği kullanır.
type TimeOrderRule struct {
	// watermark, abonenin şimdiye kadar görülen en yüksek olay zamanıdır.
	watermark map[string]watermark
	// tolerance, geriye gidiş toleransıdır (integrity.detection.
	// time_backstep_tolerance_s). Varsayılan 0 — katı karşılaştırma.
	tolerance time.Duration
	// tickLength, `margin` normalizasyonu içindir.
	tickLength time.Duration
	marginCap  float64
}

// watermark, bir abonenin en yüksek görülen olay zamanı ve o olayın kimliğidir.
type watermark struct {
	at      time.Time
	eventID uuid.UUID
}

// NewTimeOrderRule, kuralı kurar.
//
// tickLength, `margin` alanının birimidir (ADR-31/4:
// geri_gidiş_saniye / tick_saniye). 2 saat geri + 5 dakika tick → margin 24,0.
// Pozitif olmak zorundadır: sıfır bölme margin'i sonsuz yapar ve bulgu
// kapılarak yazılır ama bilgi kaybolur.
func NewTimeOrderRule(tolerance, tickLength time.Duration, marginCap float64) (*TimeOrderRule, error) {
	if tolerance < 0 {
		return nil, errRuleSetup(integrityrule.TimeOrder,
			fmt.Sprintf("tolerans negatif olamaz (%s)", tolerance))
	}
	if tickLength <= 0 {
		return nil, errRuleSetup(integrityrule.TimeOrder,
			fmt.Sprintf("tick uzunluğu pozitif olmalı (%s)", tickLength))
	}
	if !(marginCap > 0) {
		marginCap = DefaultMarginCap
	}
	return &TimeOrderRule{
		watermark:  make(map[string]watermark),
		tolerance:  tolerance,
		tickLength: tickLength,
		marginCap:  marginCap,
	}, nil
}

// ID, kural kimliğidir.
func (r *TimeOrderRule) ID() integrityrule.ID { return integrityrule.TimeOrder }

// Observe, kaydın damgasını abonenin su işaretiyle karşılaştırır.
//
// # Neden katı eşitsizlik
//
// Tick içi olaylar **aynı** damgayı taşır (`Builder.TimeAt(tick)` tick'ten
// türetir, seq'ten değil). Eşitlik ihlal sayılsaydı her tick-içi ikinci olay
// bulgu üretir ve precision çökerdi: Sprint 5 verisinde ~%1,7 olay bir başka
// olayla aynı tick'i paylaşıyor, yani ~5.000 yanlış pozitif.
//
// # Su işareti ihlalde güncellenmez
//
// Kaydırılmış damga su işaretini geriye çekmemelidir; çekseydi kaydırılmış
// kaydın **ardından** gelen temiz kayıtlar da ihlal görünmez hâle gelirdi ve
// bir sonraki kaydırma tespit edilemezdi.
func (r *TimeOrderRule) Observe(rec Record) []Hit {
	if rec.Subscriber == "" {
		// Takma adı olmayan kayıt için abone dizisi kurulamaz. Sessizce
		// geçilir: kural 1 zaten böyle bir kaydı yakalayacak durumda değil,
		// ama burada bulgu üretmek de yanlış olurdu.
		return nil
	}

	prev, seen := r.watermark[rec.Subscriber]
	if !seen {
		r.watermark[rec.Subscriber] = watermark{at: rec.Time, eventID: rec.EventID}
		return nil
	}

	backstep := prev.at.Sub(rec.Time)
	if backstep <= r.tolerance {
		if rec.Time.After(prev.at) {
			r.watermark[rec.Subscriber] = watermark{at: rec.Time, eventID: rec.EventID}
		}
		return nil
	}

	evidence := NewEvidence(integrityrule.TimeOrder, r.marginCap).
		Time("record_time", rec.Time).
		Time("watermark", prev.at).
		Float("backstep_s", backstep.Seconds()).
		Float("tolerance_s", r.tolerance.Seconds()).
		UUID("prev_event_id", prev.eventID).
		MustBuild()

	return []Hit{{
		Rule:     integrityrule.TimeOrder,
		EventID:  rec.EventID,
		Time:     rec.Time,
		Scenario: rec.Scenario,
		Margin:   backstep.Seconds() / r.tickLength.Seconds(),
		Evidence: evidence,
	}}
}

// Subscribers, izlenen abone sayısıdır (bellek sınırı teşhisi).
func (r *TimeOrderRule) Subscribers() int { return len(r.watermark) }

// errRuleSetup, kural kurulum hatası üretir.
func errRuleSetup(id integrityrule.ID, msg string) error {
	return fmt.Errorf("kural %s kurulamadı: %s", id, msg)
}
