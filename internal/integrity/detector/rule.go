// Package detector, bütünlük tespit kurallarını ve kural motorunu içerir
// (E05, ADR-27..31).
//
// # Kör test bu pakette de geçerlidir
//
// Dedektörün gördüğü tek şey `hts_records`'tur: kaydın kendisi. Gerçek konum,
// ajan kimliği ve enjeksiyon etiketi `ground_truth`'tadır ve `svc_integrity`
// rolü o tabloya erişemez (ADR-09 katman 2). Bu paket ne
// `internal/simulator`'ı ne `internal/analysis`'i import eder (katman 4,
// `tests/isolation/import_graph_test.go`).
//
// # Kurallar bastırmayı bilmez
//
// Kural ham isabet (Hit) döndürür; öncelik ve bastırma **motorda** uygulanır ve
// Finding'e dönüşür (ADR-28/5). Öncelik mantığı kuralların içine yazılsaydı beş
// yerde tekrarlanır ve zamanla ayrışırdı — `internal/persist`'in tek çekirdekle
// iki rolü çözmesiyle aynı gerekçe. Ayrıca kural birim testleri öncelik durumunu
// kurmak zorunda kalır ve kuralın kendisini sınamazdı.
package detector

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// Record, dedektörün gördüğü kayıttır.
//
// `htswire.Record` ile alan alan aynıdır; ayrı bir tip olması iki şey sağlar:
// kurallar Kafka veya veritabanı olmadan test edilebilir, ve akış ile toplu
// fazlar **aynı** kural koduna aynı şekli verir (ADR-27).
//
// Konum alanı yoktur ve eklenmeyecektir: kaydın gerçek konumu bilmemesi
// çalışmanın temel varsayımıdır.
type Record struct {
	RunID      uuid.UUID
	EventID    uuid.UUID
	Time       time.Time
	Subscriber string // pseudo_msisdn
	Device     string // pseudo_imei
	EventType  string
	CellID     uuid.UUID
	TAValue    *int
	Scenario   string
}

// FromWire, Kafka tel biçimini dedektör kaydına çevirir (akış fazı).
func FromWire(r htswire.Record) Record {
	return Record{
		RunID:      r.RunID,
		EventID:    r.EventID,
		Time:       r.Time,
		Subscriber: r.PseudoMSISDN,
		Device:     r.PseudoIMEI,
		EventType:  r.EventType,
		CellID:     r.CellID,
		TAValue:    r.TAValue,
		Scenario:   r.Scenario,
	}
}

// Hit, bir kuralın ürettiği ham isabettir.
//
// Öncelik bilgisi taşımaz: hangi isabetin kanonik olduğuna motor karar verir.
type Hit struct {
	// Rule, isabeti üreten kuraldır.
	Rule integrityrule.ID
	// EventID, bulgunun çıpalandığı olaydır.
	//
	// Kural, **gördüğü** bir kaydın kimliğini yazar. Var olmayan bir olayın
	// kimliği üretilemez (ADR-30: kural 4 bu yüzden olay-çıpalı değildir).
	EventID uuid.UUID
	// Time, kaydın olay zamanıdır (bulgunun yazılma zamanı değil).
	Time time.Time
	// Scenario, kaydın senaryosudur.
	Scenario string
	// Margin, eşiğin aşım oranıdır (ADR-31/4). Sonlu ve ≥ 1,0 olmalıdır.
	Margin float64
	// Evidence, kuralı tetikleyen somut değerlerdir (adli açıklanabilirlik).
	Evidence Evidence
}

// Finding, motorun öncelik uyguladıktan sonra yazdığı satırdır.
type Finding struct {
	Hit

	// DetectedIn, bulguyu üreten fazdır (integrity_findings.detected_in).
	DetectedIn integrityrule.Class

	// SuppressedBy nil ise bulgu kanoniktir; F.5 kanonik ölçümü yalnızca
	// bunları sayar (ADR-28/4). Doluysa öncelik yarışını kazanan kuralın
	// kimliğidir.
	SuppressedBy *integrityrule.ID
}

// Canonical, bulgunun F.5 kanonik ölçümüne girip girmediğini bildirir.
func (f Finding) Canonical() bool { return f.SuppressedBy == nil }

// RuleName, bulgunun kanonik kural adıdır (integrity_findings.rule_name).
func (f Finding) RuleName() string { return f.Rule.Name() }

// StreamRule, varış sırasında tek kayıt gören kuraldır (ADR-27 ClassStream).
//
// Durum abone başına sabittir; kayıt sayısıyla büyüyen durum tutulamaz.
type StreamRule interface {
	// ID, kuralın kimliğidir; Class() ClassStream olmak zorundadır.
	ID() integrityrule.ID
	// Observe, kaydı görür ve sıfır veya bir isabet döndürür.
	//
	// Bir kural aynı olay için birden çok isabet üretemez: tekillik kısıtı
	// (run_id, event_id, rule_id) bunu şema düzeyinde de reddeder.
	Observe(rec Record) []Hit
}

// BatchRule, bir abonenin olay-zamanı sıralı dizisini gören kuraldır
// (ADR-27 ClassBatch).
type BatchRule interface {
	// ID, kuralın kimliğidir; Class() ClassBatch olmak zorundadır.
	ID() integrityrule.ID
	// Evaluate, abonenin tüm dizisini değerlendirir.
	Evaluate(seq Sequence) []Hit
}

// Sequence, bir abonenin olay zamanına göre sıralı kayıtlarıdır.
//
// Sıralama çağıranın sorumluluğudur (toplu faz SQL ORDER BY ile sağlar);
// kurallar sıralı olduğunu varsayar ve Validate ile denetlenebilir.
type Sequence struct {
	// Subscriber, pseudo_msisdn'dir.
	Subscriber string
	// Records, olay zamanına göre artan sıralı kayıtlardır.
	Records []Record
}

// Validate, dizinin tek aboneye ait ve sıralı olduğunu denetler.
//
// Kurallar bu değişmeze güveniyor; bozulursa hız kuralı negatif Δt hesaplar ve
// bulgu üretmez (sessiz kayıp). Toplu faz her diziyi bu kapıdan geçirir.
func (s Sequence) Validate() error {
	for i, rec := range s.Records {
		if rec.Subscriber != s.Subscriber {
			return fmt.Errorf("dizi %q: kayıt %d başka aboneye ait (%q)",
				s.Subscriber, i, rec.Subscriber)
		}
		if i > 0 && rec.Time.Before(s.Records[i-1].Time) {
			return fmt.Errorf("dizi %q: kayıt %d zaman sırasında değil (%s < %s)",
				s.Subscriber, i, rec.Time.Format(time.RFC3339), s.Records[i-1].Time.Format(time.RFC3339))
		}
	}
	return nil
}

// Len, dizideki kayıt sayısıdır.
func (s Sequence) Len() int { return len(s.Records) }
