// T-E03-01 — Kafka consumer + OTel bağlam çıkarımı (O-05).

package driver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/sampling"
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
	sampler sampling.Policy
	runID   uuid.UUID
	idle    time.Duration
	seen    time.Time
	log     *slog.Logger

	analyzed int64
	skipped  int64
	foreign  int64
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
	// RunID, işlenecek koşudur (ADR-05).
	//
	// Topic koşular arasında paylaşılır ve geçmiş koşuların kayıtları
	// silinmez (retention 7 gün). Başka bir koşunun kaydı bu koşunun
	// envanterinde bulunmayan bir hücreye işaret eder ve kütle üretimi
	// "hücre envanterde yok" diye düşer — sessiz bir hata değil, ama
	// gürültülü ve yanlış: o kayıt bu servise ait değildir.
	//
	// Ayrıca sayaçları bozar: analyzed_events başka koşuların olaylarını da
	// sayarsa bütünlük denetimi (ADR-23) tutmaz.
	RunID uuid.UUID
	// Sampler, hangi olayların işleneceğini belirler (ADR-14, ADR-24).
	//
	// Filtre kütle hesabından **önce** uygulanır: elenen olay için ne kütle
	// üretilir ne de satır yazılır. Sıfır değeri geçersizdir; çağıran açıkça
	// bir mod seçmelidir — sessiz bir "hepsini işle" varsayımı, örnekleme
	// kararını (ADR-14) kazara devre dışı bırakırdı.
	Sampler sampling.Policy
	// IdleTimeout, hiç kayıt gelmediğinde tüketicinin kendiliğinden duracağı
	// süredir; 0 ise süresiz çalışır (toplu koşum için — T-E04-08).
	IdleTimeout time.Duration
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
	case !cfg.Sampler.Mode().Valid():
		return nil, fmt.Errorf("tüketici: örnekleme politikası zorunlu (ADR-14)")
	case cfg.RunID == uuid.Nil:
		return nil, fmt.Errorf("tüketici: run_id zorunlu (ADR-05)")
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
	return &Consumer{
		client:  client,
		engine:  cfg.Engine,
		handler: cfg.Handler,
		sampler: cfg.Sampler,
		runID:   cfg.RunID,
		idle:    cfg.IdleTimeout,
		seen:    time.Now(),
		log:     log,
	}, nil
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

		if c.idle > 0 && time.Since(c.seen) > c.idle {
			c.log.Info("akış boşta, tüketici duruyor",
				"boşta", c.idle, "işlenen", c.analyzed, "elenen", c.skipped)
			return nil
		}

		pollCtx, cancel := pollContext(ctx, c.idle)
		fetches := c.client.PollFetches(pollCtx)
		cancel()

		if errs := fetches.Errors(); len(errs) > 0 {
			if errors.Is(errs[0].Err, context.DeadlineExceeded) && ctx.Err() == nil {
				continue
			}
			if errors.Is(errs[0].Err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("tüketici: getirme hatası (%s): %w", errs[0].Topic, errs[0].Err)
		}

		hadRecords := fetches.NumRecords() > 0

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

			if rec.RunID != c.runID {
				c.foreign++
				return
			}
			if !c.sampler.Includes(rec.EventID) {
				c.skipped++
				return
			}

			result, err := c.engine.Process(rec)
			if err != nil {
				// Enjeksiyon kural 1 (sahte hücre) envanterde olmayan bir
				// cell_id yazar (ADR-09); o kayıt için kütle üretilemez ve
				// bu **beklenen** bir durumdur. Sayaç artmaz: analyzed_events
				// yalnızca gerçekten tahmin üretilen olayları saymalıdır,
				// yoksa bütünlük denetimi (ADR-23) tutmaz.
				failed++
				c.log.Warn("kütle üretilemedi, atlanıyor",
					"event_id", rec.EventID, "cell_id", rec.CellID, "hata", err)
				return
			}

			if err := c.handler.Handle(msgCtx, rec, result); err != nil {
				failed++
				c.log.Error("kütle teslim edilemedi", "event_id", rec.EventID, "hata", err)
				return
			}
			c.analyzed++
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

		// Boşta sayacı parti **işlendikten sonra** sıfırlanır.
		//
		// Başta sıfırlansaydı, işleme süresi boşta süresi sayılırdı: kırsal
		// TA'sız senaryoda tek bir parti ~20 sn sürüyor ve eşik 20 sn olduğu
		// için tüketici, akışta 277.000 kayıt dururken kendini boşta sanıp
		// çıkıyordu (senaryo D ilk koşumda 3.000 yerine 225 olay analiz etti).
		if hadRecords {
			c.seen = time.Now()
		}
	}
}

// pollContext, yoklama için süreli bir bağlam üretir (bkz. internal/persist).
func pollContext(ctx context.Context, idle time.Duration) (context.Context, context.CancelFunc) {
	if idle <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, idle)
}

// Stats, örnekleme sonrası işlenen ve elenen olay sayılarını döndürür.
//
// İşlenen sayısı `run_config.analyzed_events` sütununa yazılır (ADR-23):
// bütünlük denetimi `estimates = 5 × analyzed_events` beklentisini buradan
// kurar.
func (c *Consumer) Stats() (analyzed, skipped int64) {
	return c.analyzed, c.skipped
}

// Foreign, başka koşulara ait olduğu için atlanan kayıt sayısını döndürür.
func (c *Consumer) Foreign() int64 { return c.foreign }

// Close, tüketiciyi kapatır.
func (c *Consumer) Close() {
	if c.client != nil {
		c.client.Close()
	}
}
