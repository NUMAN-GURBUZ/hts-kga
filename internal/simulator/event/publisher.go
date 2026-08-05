// T-E02-18 — Kayıt çiftinin iki Kafka topic'ine yayınlanması.
//
// Serileştirme burada yapılır: `pkg/kafka` alan tiplerini tanımaz, bu paket
// de Kafka istemcisinin ayrıntılarını bilmez. Aradaki sözleşme `kafka.Message`
// (topic, anahtar, gövde) ve `Sink` arayüzüdür.
//
// # Anahtar seçimleri (plan C.1)
//
//	hts.records      → pseudo_msisdn  : aynı abonenin kayıtları aynı partition'da,
//	                                    S4'ün hız/yörünge kuralları sıra görür
//	hts.groundtruth  → agent_id       : S3a persister ajan bazlı işler
//
// # Tel biçimi
//
// JSON kullanılır. Alan adları veritabanı sütunlarıyla birebir aynıdır;
// böylece S3a persister ve analiz motoru için ayrı bir eşleme tablosu
// gerekmez ve kayıtlar `kafka-console-consumer` ile elle okunabilir kalır —
// hata ayıklamada ve savunmada bu değerlidir.

package event

import (
	"context"
	"fmt"
	"strconv"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
)

// Sink, yayın hedefidir.
//
// Arayüz tüketici tarafında tanımlanır: testler Kafka olmadan koşar, üretimde
// `*kafka.Producer` doğrudan uyar.
type Sink interface {
	Publish(ctx context.Context, messages ...kafka.Message) error
}

// Publisher, kayıt çiftlerini Kafka'ya yayınlar.
type Publisher struct {
	sink Sink
}

// NewPublisher, verilen hedefe yazan bir yayıncı kurar.
func NewPublisher(sink Sink) (*Publisher, error) {
	if sink == nil {
		return nil, fmt.Errorf("yayıncı: hedef zorunlu")
	}
	return &Publisher{sink: sink}, nil
}

// Publish, kayıt çiftlerini iki topic'e yayınlar.
//
// Ground truth **her zaman** yazılır: kapsama dışı tick'lerde de (ADR-08/3),
// enjeksiyon kural 4 kaydı sildiğinde de (ADR-09). HTS kaydı yalnızca varsa
// yazılır. `make verify-integrity` bu asimetriyi bilerek denetler.
func (p *Publisher) Publish(ctx context.Context, pairs ...Pair) error {
	messages := make([]kafka.Message, 0, len(pairs)*2)

	for i, pair := range pairs {
		truthValue, err := htswire.EncodeGroundTruth(toTruthWire(pair.Truth))
		if err != nil {
			return fmt.Errorf("yayın: ground truth serileştirilemedi (çift %d): %w", i, err)
		}
		messages = append(messages, kafka.Message{
			Topic: kafka.TopicGroundTruth,
			Key:   []byte(strconv.Itoa(pair.Truth.AgentID)),
			Value: truthValue,
		})

		if pair.Record == nil {
			continue
		}
		recordValue, err := htswire.EncodeRecord(toRecordWire(*pair.Record))
		if err != nil {
			return fmt.Errorf("yayın: kayıt serileştirilemedi (çift %d): %w", i, err)
		}
		messages = append(messages, kafka.Message{
			Topic: kafka.TopicRecords,
			Key:   []byte(pair.Record.PseudoMSISDN),
			Value: recordValue,
		})
	}

	return p.sink.Publish(ctx, messages...)
}

// toRecordWire, HTS kaydını tel biçimine çevirir (pkg/htswire sözleşmesi).
func toRecordWire(r HTSRecord) htswire.Record {
	return htswire.Record{
		RunID:        r.RunID,
		EventID:      r.EventID,
		Time:         r.Time.UTC(),
		PseudoMSISDN: r.PseudoMSISDN,
		PseudoIMEI:   r.PseudoIMEI,
		EventType:    string(r.EventType),
		CellID:       r.CellID,
		TAValue:      r.TAValue,
		Scenario:     r.Scenario,
	}
}

// toTruthWire, ground truth kaydını tel biçimine çevirir.
func toTruthWire(g GroundTruth) htswire.GroundTruth {
	key := g.PartitionKey
	if !key.Valid() {
		key = split.Validation // varsayılan: ölçüm kümesi
	}
	return htswire.GroundTruth{
		RunID:        g.RunID,
		EventID:      g.EventID,
		Time:         g.Time.UTC(),
		AgentID:      g.AgentID,
		Lat:          g.TrueLocation.Lat,
		Lon:          g.TrueLocation.Lon,
		Covered:      g.Covered,
		PartitionKey: key.String(),
		InjectedRule: g.InjectedRule,
	}
}

// EncodeRecordForTest, HTS kaydını tel biçiminde serileştirir.
//
// Testler tel biçimini doğrudan denetleyebilsin diye vardır: konum sızıntısı
// olacaksa yapı alanlarında değil, yayınlanan gövdede görünür.
func EncodeRecordForTest(r HTSRecord) ([]byte, error) {
	return htswire.EncodeRecord(toRecordWire(r))
}
