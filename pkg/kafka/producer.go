// T-E02-18 — Kafka üreticisi.

package kafka

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// tracer, üretici span'lerini açan OTel tracer'ıdır (O-05, ADR-34/2-3).
var tracer = otel.Tracer("github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka")

// meter, yayınlanan mesaj sayacını tutar (ADR-34/2).
var meter = otel.Meter("github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka")

var messagesPublished = mustCounter(meter, "hts_kafka_messages_published_total",
	"Topic başına yayınlanan Kafka mesajı sayısı")

func mustCounter(m metric.Meter, name, desc string) metric.Int64Counter {
	c, err := m.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		panic("otel sayaç: " + err.Error())
	}
	return c
}

// Topic adları (plan C.2, scripts/kafka-setup.sh ile birebir).
const (
	// TopicRecords, operatör kayıtlarıdır. Anahtar: pseudo_msisdn.
	//
	// Anahtar seçimi keyfi değildir: aynı abonenin tüm kayıtları aynı
	// partition'a düşer, böylece S4'ün hız ve yörünge kuralları kayıtları
	// sırayla görebilir. Rastgele anahtarda sıra partition'lar arasına
	// dağılır ve ardışıklık kaybolurdu.
	TopicRecords = "hts.records"

	// TopicGroundTruth, gerçek konum kayıtlarıdır. Anahtar: agent_id.
	//
	// S2 ve S4 principal'leri bu topic'e ACL ile kapalıdır (kör test,
	// katman 1).
	TopicGroundTruth = "hts.groundtruth"
)

// Message, yayınlanacak tek bir kayıttır.
type Message struct {
	// Topic, hedef topic'tir.
	Topic string
	// Key, partition anahtarıdır.
	Key []byte
	// Value, serileştirilmiş gövdedir.
	Value []byte
}

// Producer, Kafka'ya yazan üreticidir.
type Producer struct {
	client *kgo.Client
}

// NewProducer, verilen broker adreslerine bağlanan bir üretici kurar.
func NewProducer(brokers []string, opts ...kgo.Opt) (*Producer, error) {
	// Kimlik ortamdan gelir (ADR-32/4): simülatör `svc_simulator` olarak
	// bağlanır ve yalnızca iki topic'e WRITE yetkisi taşır.
	base, err := ClientOptions(brokers, CredentialsFromEnv())
	if err != nil {
		return nil, fmt.Errorf("kafka üreticisi: %w", err)
	}
	base = append(base,
		// Idempotent üretim varsayılan olarak açıktır: yeniden deneme
		// yinelenen kayıt üretmez. ADR-01'in UNIQUE kısıtı yinelenen
		// event_id'yi zaten reddederdi, ama hatayı Kafka katmanında
		// önlemek koşuyu durdurmaktan iyidir.
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
	)

	client, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("kafka üreticisi: bağlantı kurulamadı: %w", err)
	}
	return &Producer{client: client}, nil
}

// Publish, mesajları yayınlar ve hepsi onaylanana kadar bekler.
//
// Bağlam (ctx) içindeki izleme bilgisi her kaydın `traceparent` başlığına
// yazılır (O-05): tüketici tarafındaki span'ler aynı ize bağlanır.
//
// Toplu yayın senkrondur: simülatör bir tick'in olaylarını yayınlamadan
// sonraki tick'e geçmemelidir, aksi hâlde hata durumunda hangi olayların
// yazıldığı belirsiz kalır.
func (p *Producer) Publish(ctx context.Context, messages ...Message) error {
	if len(messages) == 0 {
		return nil
	}

	ctx, span := tracer.Start(ctx, "kafka.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.Int("messaging.batch.message_count", len(messages))))
	defer span.End()

	records := make([]*kgo.Record, len(messages))
	for i, m := range messages {
		if m.Topic == "" {
			err := fmt.Errorf("kafka yayını: mesaj[%d] topic'siz", i)
			span.RecordError(err)
			return err
		}
		rec := &kgo.Record{Topic: m.Topic, Key: m.Key, Value: m.Value}
		propagator.Inject(ctx, NewHeaderCarrier(&rec.Headers))
		records[i] = rec
	}

	results := p.client.ProduceSync(ctx, records...)
	if err := results.FirstErr(); err != nil {
		err = fmt.Errorf("kafka yayını (%d mesaj): %w", len(messages), err)
		span.RecordError(err)
		return err
	}

	byTopic := make(map[string]int64, 2)
	for _, m := range messages {
		byTopic[m.Topic]++
	}
	for topic, n := range byTopic {
		messagesPublished.Add(ctx, n, metric.WithAttributes(attribute.String("topic", topic)))
	}
	return nil
}

// Close, üreticiyi kapatır ve bekleyen kayıtları boşaltır.
func (p *Producer) Close() {
	if p.client != nil {
		p.client.Close()
	}
}
