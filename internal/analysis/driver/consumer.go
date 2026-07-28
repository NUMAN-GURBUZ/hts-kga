// T-E03-01 — Kafka consumer + OTel bağlam çıkarımı (O-05).

package driver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
)

// Handler, üretilen kütlenin teslim edileceği yerdir.
//
// Sprint 3 kapsamında çıktı **bellekte kalır**: kontur, MULTIPOLYGON, centroid
// ve `estimates` yazımı S4'e aittir. Arayüz burada tanımlıdır ki S4 geldiğinde
// consumer'a dokunulmasın.
type Handler interface {
	Handle(ctx context.Context, rec htswire.Record, result core.Result) error
}

// Flusher, tamponlu bir işleyicinin boşaltma yeteneğidir.
//
// Tüketici, offset commit'inden **önce** çağırır: tampon dolu kalıp offset
// ilerleseydi, o andaki bir çökme yazılmamış tahminleri geri getirilemez
// biçimde kaybederdi. İşleyici bu arayüzü uygulamıyorsa adım atlanır.
type Flusher interface {
	Flush(ctx context.Context) error
}

// HandlerFunc, işlev tipindeki Handler uyarlayıcısıdır.
type HandlerFunc func(ctx context.Context, rec htswire.Record, result core.Result) error

// Handle, Handler arayüzünü uygular.
func (f HandlerFunc) Handle(ctx context.Context, rec htswire.Record, result core.Result) error {
	return f(ctx, rec, result)
}

// Consumer, `hts.records` akışını tüketip kütle üretir.
//
// `hts.groundtruth` topic'ine **abone olmaz** — Kafka ACL'i zaten reddederdi
// (kör test, katman 1), ama koda da yazılmaz: erişim denemesi bile
// yapılmaması tasarımın açık ifadesidir.
type Consumer struct {
	client  *kgo.Client
	engine  *Engine
	handler Handler
	log     *slog.Logger
}

// ConsumerConfig, tüketicinin kurulum parametreleridir.
type ConsumerConfig struct {
	// Brokers, Kafka broker adresleridir.
	Brokers []string
	// Group, tüketici grubu kimliğidir.
	Group string
	// Engine, kütle üreten sürücü çekirdeğidir.
	Engine *Engine
	// Handler, üretilen kütleyi alır.
	Handler Handler
	// Logger, isteğe bağlıdır; nil ise slog.Default kullanılır.
	Logger *slog.Logger
}

// NewConsumer, tüketiciyi kurar ve `hts.records` topic'ine abone olur.
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	switch {
	case len(cfg.Brokers) == 0:
		return nil, fmt.Errorf("tüketici: en az bir broker adresi gerekli")
	case cfg.Group == "":
		return nil, fmt.Errorf("tüketici: grup kimliği zorunlu")
	case cfg.Engine == nil:
		return nil, fmt.Errorf("tüketici: motor zorunlu")
	case cfg.Handler == nil:
		return nil, fmt.Errorf("tüketici: işleyici zorunlu")
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(kafka.TopicRecords),
		// Offset yalnızca işlem başarıyla bittikten sonra ilerletilir:
		// otomatik commit, hata durumunda işlenmemiş kaydı atlardı.
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("tüketici: bağlantı kurulamadı: %w", err)
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Consumer{client: client, engine: cfg.Engine, handler: cfg.Handler, log: log}, nil
}

// Run, bağlam iptal edilene kadar kayıtları tüketir.
//
// Her kayıt için:
//
//  1. `traceparent` başlığından izleme bağlamı çıkarılır (O-05)
//  2. Gövde çözülür (pkg/htswire sözleşmesi)
//  3. Saf çekirdek çağrılır (core.Estimate)
//  4. Sonuç işleyiciye verilir
//  5. Offset commit edilir
//
// Çözümlenemeyen kayıt akışı durdurmaz: sayaç artar, kayıt loglanır ve
// tüketim devam eder. Tek bir bozuk mesaj yüzünden 300.000 olayın işlenmemesi
// kabul edilemez; ama sessizce yutulması da kabul edilemez.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		fetches := c.client.PollFetches(ctx)
		if errs := fetches.Errors(); len(errs) > 0 {
			if errors.Is(errs[0].Err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("tüketici: getirme hatası (%s): %w", errs[0].Topic, errs[0].Err)
		}

		var failed int
		fetches.EachRecord(func(msg *kgo.Record) {
			msgCtx := kafka.Propagator().Extract(ctx, kafka.NewHeaderCarrier(&msg.Headers))

			rec, err := htswire.DecodeRecord(msg.Value)
			if err != nil {
				failed++
				c.log.Error("kayıt çözümlenemedi, atlanıyor",
					"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset, "hata", err)
				return
			}

			result, err := c.engine.Process(rec)
			if err != nil {
				failed++
				c.log.Error("kütle üretilemedi, atlanıyor",
					"event_id", rec.EventID, "cell_id", rec.CellID, "hata", err)
				return
			}

			if err := c.handler.Handle(msgCtx, rec, result); err != nil {
				failed++
				c.log.Error("kütle teslim edilemedi", "event_id", rec.EventID, "hata", err)
			}
		})

		if flusher, ok := c.handler.(Flusher); ok {
			if err := flusher.Flush(ctx); err != nil {
				return fmt.Errorf("tüketici: tampon boşaltılamadı, offset ilerletilmiyor: %w", err)
			}
		}

		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			return fmt.Errorf("tüketici: offset commit edilemedi: %w", err)
		}
		if failed > 0 {
			c.log.Warn("bu partide işlenemeyen kayıt var", "adet", failed)
		}
	}
}

// Close, tüketiciyi kapatır.
func (c *Consumer) Close() {
	if c.client != nil {
		c.client.Close()
	}
}
