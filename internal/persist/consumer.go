// Package persist, Kafka topic'lerini veritabanına yazan tüketicidir
// (G2 · T-E04-01, ADR-22 · ADR-04).
//
// İki rol aynı çekirdeği kullanır:
//
//	S1b — hts.records      → hts_records     (ADR-22)
//	S3a — hts.groundtruth  → ground_truth    (ADR-04)
//
// # Neden tek çekirdek
//
// İkisinin de yaptığı iş aynıdır: çöz, tamponla, yaz, offset ilerlet. Ayrı
// yazılsalardı offset disiplini iki yerde tekrarlanır ve zamanla ayrışırdı —
// biri tamponu commit'ten önce boşaltır, diğeri unuturdu. Fark yalnızca
// çözümleyici ve yazıcı fonksiyonlarındadır; ikisi tip parametresiyle verilir.
//
// # Offset disiplini
//
// Tampon **her zaman** offset commit'inden önce boşaltılır. Ters sırada bir
// çökme, işlenmiş sayılan ama yazılmamış kayıtları geri getirilemez biçimde
// kaybederdi; ADR-01 bütünlük denetimi bunu "eksik kayıt" olarak görür ama
// veriyi geri getiremez.
//
// # Yeniden çalıştırma
//
// Yazıcılar `ON CONFLICT DO NOTHING` kullanır. Tüketici çökerse commit
// edilmemiş kayıtlar yeniden işlenir (at-least-once); yinelenen satır hata
// değil, beklenen durumdur.
package persist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
)

// defaultBatchRows, tek yazma sorgusuna giden satır sayısıdır.
//
// `hts_records` satırı küçüktür (~100 bayt); 1.000'lik parti tek gidiş-dönüşte
// yazılır ve 300.000 olaylık koşuda 300 sorgu eder.
const defaultBatchRows = 1000

// Decoder, mesaj gövdesini satıra çevirir.
type Decoder[T any] func([]byte) (T, error)

// Writer, satırları veritabanına yazar ve eklenen satır sayısını döndürür.
type Writer[T any] func(ctx context.Context, rows []T) (int64, error)

// Config, tüketicinin kurulum parametreleridir.
type Config[T any] struct {
	// Brokers, Kafka broker adresleridir.
	Brokers []string
	// Topic, tüketilecek topic'tir.
	Topic string
	// Group, tüketici grubu kimliğidir.
	Group string
	// Decode, mesaj çözümleyicisidir.
	Decode Decoder[T]
	// Write, veritabanı yazıcısıdır.
	Write Writer[T]
	// BatchRows, tampon eşiğidir; 0 ise varsayılan kullanılır.
	BatchRows int
	// IdleTimeout, hiç mesaj gelmediğinde tüketicinin kendiliğinden
	// duracağı süredir; 0 ise süresiz çalışır.
	//
	// Toplu koşum (T-E04-08) için gereklidir: akış bittiğinde servisin
	// kendiliğinden çıkması, betiği "ne zaman durdurayım" tahmininden
	// kurtarır. Sürekli çalışan dağıtımlarda 0 bırakılır.
	IdleTimeout time.Duration
	// Logger, isteğe bağlıdır.
	Logger *slog.Logger
}

// Consumer, bir topic'i veritabanına yazar.
type Consumer[T any] struct {
	client   *kgo.Client
	cfg      Config[T]
	log      *slog.Logger
	batch    []T
	written  int64
	failed   int64
	lastSeen time.Time
}

// New, tüketiciyi kurar ve topic'e abone olur.
func New[T any](cfg Config[T]) (*Consumer[T], error) {
	switch {
	case len(cfg.Brokers) == 0:
		return nil, fmt.Errorf("persister: en az bir broker adresi gerekli")
	case cfg.Topic == "":
		return nil, fmt.Errorf("persister: topic zorunlu")
	case cfg.Group == "":
		return nil, fmt.Errorf("persister: grup kimliği zorunlu")
	case cfg.Decode == nil:
		return nil, fmt.Errorf("persister: çözümleyici zorunlu")
	case cfg.Write == nil:
		return nil, fmt.Errorf("persister: yazıcı zorunlu")
	}

	if cfg.BatchRows <= 0 {
		cfg.BatchRows = defaultBatchRows
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	// Kimlik ortamdan gelir (ADR-32/4): `records` rolü svc_records_persister,
	// `groundtruth` rolü svc_gt_persister olarak bağlanır.
	base, err := kafka.ClientOptions(cfg.Brokers, kafka.CredentialsFromEnv())
	if err != nil {
		return nil, fmt.Errorf("persister: %w", err)
	}
	client, err := kgo.NewClient(append(base,
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topic),
		// Offset yalnızca yazma bittikten sonra ilerletilir.
		kgo.DisableAutoCommit(),
	)...)
	if err != nil {
		return nil, fmt.Errorf("persister: bağlantı kurulamadı: %w", err)
	}

	return &Consumer[T]{
		client:   client,
		cfg:      cfg,
		log:      log,
		batch:    make([]T, 0, cfg.BatchRows),
		lastSeen: time.Now(),
	}, nil
}

// Written, yazılan satır sayısını döndürür.
func (c *Consumer[T]) Written() int64 { return c.written }

// Failed, çözümlenemeyen mesaj sayısını döndürür.
func (c *Consumer[T]) Failed() int64 { return c.failed }

// Run, akışı tüketir ve bağlam iptal edilene kadar yazar.
//
// Çözümlenemeyen mesaj akışı durdurmaz: sayaç artar, mesaj günlüğe geçer ve
// tüketim devam eder. Tek bir bozuk mesaj yüzünden koşunun tamamının
// yazılmaması kabul edilemez; sessizce yutulması da.
func (c *Consumer[T]) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return c.flush(context.WithoutCancel(ctx))
		}

		if c.cfg.IdleTimeout > 0 && time.Since(c.lastSeen) > c.cfg.IdleTimeout {
			c.log.Info("akış boşta, tüketici duruyor",
				"boşta", c.cfg.IdleTimeout, "yazılan", c.written)
			return c.flush(context.WithoutCancel(ctx))
		}

		// Yoklama süresi sınırlanır ki boşta kalma denetimi çalışabilsin.
		// `defer cancel()` kullanılmaz: döngü içinde birikir ve uzun ömürlü
		// tüketicide sızıntı olurdu.
		pollCtx, cancel := pollContext(ctx, c.cfg.IdleTimeout)
		fetches := c.client.PollFetches(pollCtx)
		cancel()
		if errs := fetches.Errors(); len(errs) > 0 {
			if errors.Is(errs[0].Err, context.DeadlineExceeded) && ctx.Err() == nil {
				continue // boşta yoklama: döngü başındaki süre denetimine dön
			}
			if errors.Is(errs[0].Err, context.Canceled) {
				return c.flush(context.WithoutCancel(ctx))
			}
			return fmt.Errorf("persister: getirme hatası (%s): %w", errs[0].Topic, errs[0].Err)
		}

		if fetches.NumRecords() > 0 {
			c.lastSeen = time.Now()
		}

		fetches.EachRecord(func(msg *kgo.Record) {
			row, err := c.cfg.Decode(msg.Value)
			if err != nil {
				c.failed++
				c.log.Error("mesaj çözümlenemedi, atlanıyor",
					"topic", msg.Topic, "partition", msg.Partition,
					"offset", msg.Offset, "hata", err)
				return
			}
			c.batch = append(c.batch, row)
		})

		// Tampon commit'ten ÖNCE boşaltılır.
		if err := c.flush(ctx); err != nil {
			return err
		}
		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			return fmt.Errorf("persister: offset commit edilemedi: %w", err)
		}
	}
}

// pollContext, yoklama için süreli bir bağlam üretir.
//
// IdleTimeout sıfırsa bağlam olduğu gibi döner ve yoklama süresiz bekler.
func pollContext(ctx context.Context, idle time.Duration) (context.Context, context.CancelFunc) {
	if idle <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, idle)
}

// flush, tamponu veritabanına yazar.
//
// # Zehirli satır (poison pill) yönetimi
//
// Tek bir satır bir CHECK kısıtını ihlal ederse PostgreSQL **tüm partiyi**
// reddeder. Hata yukarı verilip servis düşseydi, yeniden başlayan tüketici
// aynı partiyi yeniden okur ve aynı yerde düşerdi: 300.000 kaydın tamamı tek
// bozuk satır yüzünden yazılamaz hâle gelirdi.
//
// Bu yüzden parti düşerse satır satır yeniden denenir. Hâlâ reddedilen
// satırlar tam ayrıntısıyla günlüğe geçer ve `Failed()` sayacına eklenir.
// Kayıp **sessiz değildir**: `verify_integrity`'nin
// `published_vs_stored_records` denetimi (ADR-23) yayınlanan ile yazılan
// sayısını karşılaştırır ve farkı FAIL olarak raporlar.
func (c *Consumer[T]) flush(ctx context.Context) error {
	if len(c.batch) == 0 {
		return nil
	}

	for start := 0; start < len(c.batch); start += c.cfg.BatchRows {
		end := start + c.cfg.BatchRows
		if end > len(c.batch) {
			end = len(c.batch)
		}

		n, err := c.cfg.Write(ctx, c.batch[start:end])
		if err == nil {
			c.written += n
			continue
		}

		// Bağlam iptali bir veri hatası değildir; satır satır denemek anlamsız.
		if ctx.Err() != nil {
			return fmt.Errorf("persister: yazma kesildi (%d satır): %w", end-start, err)
		}

		c.log.Warn("parti reddedildi, satır satır yeniden deneniyor",
			"satır", end-start, "hata", err)
		if err := c.writeIndividually(ctx, c.batch[start:end]); err != nil {
			return err
		}
	}

	c.batch = c.batch[:0]
	return nil
}

// writeIndividually, partiyi satır satır yazar ve reddedilenleri raporlar.
func (c *Consumer[T]) writeIndividually(ctx context.Context, rows []T) error {
	for i := range rows {
		n, err := c.cfg.Write(ctx, rows[i:i+1])
		if err == nil {
			c.written += n
			continue
		}
		if ctx.Err() != nil {
			return fmt.Errorf("persister: yazma kesildi: %w", err)
		}
		c.failed++
		c.log.Error("satır yazılamadı, atlanıyor — bütünlük denetimi bu farkı raporlayacak",
			"satır", fmt.Sprintf("%+v", rows[i]), "hata", err)
	}
	return nil
}

// Close, tüketiciyi kapatır.
func (c *Consumer[T]) Close() {
	if c.client != nil {
		c.client.Close()
	}
}

// Topics, desteklenen topic adlarıdır (kolaylık sarmalayıcısı).
var (
	TopicRecords     = kafka.TopicRecords
	TopicGroundTruth = kafka.TopicGroundTruth
)
