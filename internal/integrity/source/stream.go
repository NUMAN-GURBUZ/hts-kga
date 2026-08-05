// Akış fazı girdisi: Kafka `hts.records` tüketicisi (ADR-27 faz 1).
//
// Desen `internal/persist/consumer.go` ve `internal/analysis/driver/consumer.go`
// ile aynıdır: offset yalnızca işleme bittikten sonra ilerletilir, çözümlenemeyen
// mesaj akışı durdurmaz, boşta kalma süresiyle kendiliğinden çıkılır.
//
// # hts.groundtruth topic'ine abone olunmaz
//
// Kafka ACL'i zaten reddederdi (kör test katman 1), ama koda da yazılmaz:
// erişim denemesi bile yapılmaması tasarımın açık ifadesidir. Analiz
// tüketicisinde de aynı yorum vardır.

package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/detector"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
)

// tracer, üreticinin span'ine bağlanan tüketici span'lerini açar (O-05).
var tracer = otel.Tracer("github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/source")

// meter, consumer lag ölçerini kaydeden OTel meter'dır (ADR-34/2).
var meter = otel.Meter("github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/source")

// StreamConfig, akış tüketicisinin kurulum parametreleridir.
type StreamConfig struct {
	// Brokers, Kafka broker adresleridir.
	Brokers []string
	// Group, tüketici grubu kimliğidir.
	//
	// Persister'lardan **farklı** olmak zorundadır: akış fazı aynı topic'i
	// onlarla paralel tüketir (ADR-27/2) ve aynı grupta olsalardı kayıtlar
	// aralarında bölünürdü.
	Group string
	// RunID, işlenecek koşudur (ADR-05).
	//
	// Topic koşular arasında paylaşılır ve geçmiş koşuların kayıtları
	// silinmez. Süzgeç olmasaydı başka koşuların kayıtları abone su işaretini
	// kirletir ve kural 3 yanlış pozitif üretirdi (Sprint 5 hatası #3).
	RunID uuid.UUID
	// IdleTimeout, akış boşta kalınca çıkış süresidir; 0 ise süresiz.
	IdleTimeout time.Duration
	// Logger, isteğe bağlıdır.
	Logger *slog.Logger
}

// Stream, `hts.records` akışını kural motoruna besler.
type Stream struct {
	client *kgo.Client
	engine *detector.Engine
	runID  uuid.UUID
	idle   time.Duration
	seen   time.Time
	log    *slog.Logger

	foreign int64
	failed  int64
}

// NewStream, tüketiciyi kurar ve topic'e abone olur.
func NewStream(cfg StreamConfig, engine *detector.Engine) (*Stream, error) {
	switch {
	case len(cfg.Brokers) == 0:
		return nil, fmt.Errorf("bütünlük akışı: en az bir broker adresi gerekli")
	case cfg.Group == "":
		return nil, fmt.Errorf("bütünlük akışı: grup kimliği zorunlu")
	case cfg.RunID == uuid.Nil:
		return nil, fmt.Errorf("bütünlük akışı: run_id zorunlu (ADR-05)")
	case engine == nil:
		return nil, fmt.Errorf("bütünlük akışı: kural motoru zorunlu")
	}

	// Kimlik ortamdan gelir (ADR-32/4): `svc_integrity` yalnızca hts.records
	// üzerinde READ taşır. hts.groundtruth'a abone olmayı denemiyoruz (aşağıdaki
	// paket yorumu) ve denesek de broker reddederdi (kör test katman 1).
	base, err := kafka.ClientOptions(cfg.Brokers, kafka.CredentialsFromEnv())
	if err != nil {
		return nil, fmt.Errorf("bütünlük akışı: %w", err)
	}
	client, err := kgo.NewClient(append(base,
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(kafka.TopicRecords),
		kgo.DisableAutoCommit(),
	)...)
	if err != nil {
		return nil, fmt.Errorf("bütünlük akışı: bağlantı kurulamadı: %w", err)
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	if err := kafka.RegisterLagGauge(meter, client, cfg.Group); err != nil {
		log.Warn("kafka lag ölçer kaydedilemedi", "hata", err)
	}
	return &Stream{
		client: client, engine: engine, runID: cfg.RunID,
		idle: cfg.IdleTimeout, seen: time.Now(), log: log,
	}, nil
}

// Run, akışı tüketir ve her kaydı motora verir.
//
// Offset disiplini: motor tamponu commit'ten **önce** boşaltılır. Ters sırada
// bir çökme, işlenmiş sayılan ama yazılmamış bulguları geri getirilemez biçimde
// kaybederdi.
func (s *Stream) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return s.engine.Flush(context.WithoutCancel(ctx))
		}

		if s.idle > 0 && time.Since(s.seen) > s.idle {
			st := s.engine.Stats()
			s.log.Info("akış boşta, bütünlük tüketicisi duruyor",
				"boşta", s.idle, "incelenen", st.Inspected,
				"kanonik", st.Canonical, "bastırılmış", st.Suppressed)
			return s.engine.Flush(context.WithoutCancel(ctx))
		}

		pollCtx, cancel := pollContext(ctx, s.idle)
		fetches := s.client.PollFetches(pollCtx)
		cancel()

		if errs := fetches.Errors(); len(errs) > 0 {
			if errors.Is(errs[0].Err, context.DeadlineExceeded) && ctx.Err() == nil {
				continue
			}
			if errors.Is(errs[0].Err, context.Canceled) {
				return s.engine.Flush(context.WithoutCancel(ctx))
			}
			return fmt.Errorf("bütünlük akışı: getirme hatası (%s): %w", errs[0].Topic, errs[0].Err)
		}

		hadRecords := fetches.NumRecords() > 0

		var loopErr error
		fetches.EachRecord(func(msg *kgo.Record) {
			if loopErr != nil {
				return
			}
			msgCtx := kafka.Propagator().Extract(ctx, kafka.NewHeaderCarrier(&msg.Headers))
			msgCtx, span := tracer.Start(msgCtx, "kafka.consume",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(
					attribute.String("messaging.destination.name", msg.Topic),
					attribute.Int64("messaging.kafka.partition", int64(msg.Partition)),
					attribute.Int64("messaging.kafka.offset", msg.Offset),
				))
			defer span.End()

			rec, err := htswire.DecodeRecord(msg.Value)
			if err != nil {
				s.failed++
				s.log.Error("kayıt çözümlenemedi, atlanıyor",
					"topic", msg.Topic, "partition", msg.Partition,
					"offset", msg.Offset, "hata", err)
				return
			}
			if rec.RunID != s.runID {
				s.foreign++
				return
			}
			if err := s.engine.Observe(msgCtx, detector.FromWire(rec)); err != nil {
				loopErr = err
			}
		})
		if loopErr != nil {
			return fmt.Errorf("bütünlük akışı: %w", loopErr)
		}

		// Tampon commit'ten ÖNCE boşaltılır.
		if err := s.engine.Flush(ctx); err != nil {
			return fmt.Errorf("bütünlük akışı: tampon boşaltılamadı, offset ilerletilmiyor: %w", err)
		}
		if err := s.client.CommitUncommittedOffsets(ctx); err != nil {
			return fmt.Errorf("bütünlük akışı: offset commit edilemedi: %w", err)
		}

		// Boşta sayacı parti işlendikten sonra sıfırlanır (Sprint 5 hatası #4).
		if hadRecords {
			s.seen = time.Now()
		}
	}
}

// Foreign, başka koşulara ait olduğu için atlanan kayıt sayısıdır.
func (s *Stream) Foreign() int64 { return s.foreign }

// Failed, çözümlenemeyen mesaj sayısıdır.
func (s *Stream) Failed() int64 { return s.failed }

// Close, tüketiciyi kapatır.
func (s *Stream) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

// pollContext, yoklama için süreli bağlam üretir (bkz. internal/persist).
func pollContext(ctx context.Context, idle time.Duration) (context.Context, context.CancelFunc) {
	if idle <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, idle)
}
