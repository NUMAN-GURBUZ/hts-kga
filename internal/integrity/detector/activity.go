// T-E05-04 — Kural 5: cihaz aktivitesi tutarlılığı (ADR-27 ClassBatch).
//
// # Kuralın iddiası
//
// Abonenin kayıtlarının ezici çoğunluğu bir cihazdan (`pseudo_imei`) geliyor;
// bir azınlık başka bir cihazdan. Azınlık kayıtlar bulgudur.
//
// # Ölçüm
//
// Sprint 5 verisinde dört koşuda **precision %100 / recall %100** (1.171
// bulgu). Enjektör `otherAgent()` ile `agentID + 1..999` seçiyor; 1000 ajanlı
// koşuda bu çoğunlukla **var olmayan** bir ajanın IMEI'sidir ve abone başına
// yalnızca bir kez görünür.
//
// # Ters yön yazılmaz
//
// "Bir IMEI → birden çok MSISDN" kuralı hiç tetiklenmez: sahte IMEI var olmayan
// bir ajana ait olduğu için başka bir abonede görünmez. Yazılsa ölü kod olurdu.
//
// # Neden toplu faz — akışta precision %50'ye düşer
//
// Akışta referans belirlenemez. İlk görülen IMEI referans alınırsa ve bir
// abonenin *ilk* kaydı enjekte edilmişse, o abonenin sonraki ~293 temiz kaydı
// bulgu üretir. Beklenen etki 1000 abone × %0,4 ≈ 4 abone × 293 kayıt ≈ **1170
// hatalı bulgu** — precision %100'den ~%50'ye iner.
//
// Çoğunluk oyuna geçmek ise erken yazılmış bulguların **geri alınmasını**
// gerektirir; `integrity_findings` ekle-yalnız bir adli kayıttır ve
// `svc_integrity`'nin UPDATE/DELETE yetkisi yoktur (ADR-27 reddedilen
// alternatif B). Bu yüzden kural `ClassBatch`'tir ve kısıt tipte ifade
// edilmiştir: motor bu kuralı akış fazına **alamaz**.

package detector

import (
	"fmt"
	"sort"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// ActivityRule, abone başına modal cihaz kimliğinden sapan kayıtları tespit
// eder.
//
// Durumsuzdur: her abone dizisi bağımsız değerlendirilir.
type ActivityRule struct {
	// minSupport, referans IMEI'nin desteklenmesi gereken asgari kayıt
	// sayısıdır (integrity.detection.activity_min_support).
	minSupport int
	marginCap  float64
}

// NewActivityRule, kuralı kurar.
func NewActivityRule(minSupport int, marginCap float64) (*ActivityRule, error) {
	if minSupport < 1 {
		return nil, errRuleSetup(integrityrule.Activity,
			fmt.Sprintf("asgari destek ≥ 1 olmalı (%d)", minSupport))
	}
	if !(marginCap > 0) {
		marginCap = DefaultMarginCap
	}
	return &ActivityRule{minSupport: minSupport, marginCap: marginCap}, nil
}

// ID, kural kimliğidir.
func (r *ActivityRule) ID() integrityrule.ID { return integrityrule.Activity }

// Evaluate, abonenin cihaz dağılımını çıkarır ve azınlık kayıtları işaretler.
func (r *ActivityRule) Evaluate(seq Sequence) []Hit {
	counts := make(map[string]int)
	for _, rec := range seq.Records {
		if rec.Device == "" {
			// `pseudo_imei` şemada NULL'a izin veriyor (VARCHAR(64), NOT NULL
			// yok). Boş cihaz modal hesabına girmez: "cihaz bilinmiyor" ile
			// "cihaz değişti" farklı şeylerdir ve ikincisi iddia edilemez.
			continue
		}
		counts[rec.Device]++
	}
	if len(counts) < 2 {
		// Tek cihaz (veya hiç) → sapma yok.
		return nil
	}

	modal, support := modalDevice(counts)
	if support < r.minSupport {
		// Referans yeterince desteklenmiyor: tek kayıtlı bir abonede "modal"
		// cihaz kavramı boştur ve hangi kaydın sapma olduğu söylenemez.
		// Karar verilemezlik → bulgu yok (ADR-28/3 ile aynı ilke).
		return nil
	}

	var hits []Hit
	for _, rec := range seq.Records {
		if rec.Device == "" || rec.Device == modal {
			continue
		}

		evidence := NewEvidence(integrityrule.Activity, r.marginCap).
			Str("imei", rec.Device).
			Str("modal_imei", modal).
			Int("imei_count", len(counts)).
			Int("support", support).
			Int("deviating_records", counts[rec.Device]).
			MustBuild()

		hits = append(hits, Hit{
			Rule:     integrityrule.Activity,
			EventID:  rec.EventID,
			Time:     rec.Time,
			Scenario: rec.Scenario,
			// ADR-31/4: abonenin farklı IMEI sayısı. İki cihaz → 2,0.
			Margin:   float64(len(counts)),
			Evidence: evidence,
		})
	}
	return hits
}

// modalDevice, en çok kayda sahip cihazı ve destek sayısını döndürür.
//
// Eşitlik durumunda kimliğe göre küçük olan seçilir: harita yineleme sırası
// rastgeledir ve eşitlikte seçim koşudan koşuya değişirse aynı tohum farklı
// bulgular üretir (K10). Eşitlik gerçek koşuda beklenmez (~293 vs 1) ama
// determinizm tesadüfe bırakılmaz.
func modalDevice(counts map[string]int) (string, int) {
	devices := make([]string, 0, len(counts))
	for d := range counts {
		devices = append(devices, d)
	}
	sort.Strings(devices)

	modal, best := devices[0], counts[devices[0]]
	for _, d := range devices[1:] {
		if counts[d] > best {
			modal, best = d, counts[d]
		}
	}
	return modal, best
}
