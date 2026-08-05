// Toplu faz girdisi: `hts_records` abone bazlı sıralı taraması (ADR-27 faz 2).
//
// # İki önkoşul ve neden reddedilmeleri gerekiyor
//
//	(a) count(hts_records) = run_config.published_events
//	    → kayıtlar tam; records persister bitti
//	(b) run_config.inspected_records IS NOT NULL
//	    → akış fazı bitti; kural 1 ve 3 talepleri yazılı (ADR-28/6)
//
// (b) olmadan koşulursa kural 1'in dışlamaları eksik olur ve kural 2 bilinmeyen
// konumlu kayıtları zincire alır. Sessizce eksik veriyle koşmak, düşük recall'u
// bilimsel bulgu gibi gösterir — ADR-04'ün önkoşul disiplini burada da
// geçerlidir.

package source

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/detector"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
)

// BatchReader, toplu fazın veritabanı yüzeyidir.
//
// `*postgres.Pool` bunu uygular; arayüz olarak tanımlanması önkoşul mantığının
// veritabanı olmadan test edilebilmesi içindir.
type BatchReader interface {
	ForEachSubscriberSequence(ctx context.Context, runID uuid.UUID, fn func(postgres.SubscriberSequence) error) error
	CountHTSRecords(ctx context.Context, runID uuid.UUID) (int64, error)
	RunStatusOf(ctx context.Context, runID uuid.UUID) (postgres.RunStatus, error)
}

// BatchConfig, toplu fazın kurulum parametreleridir.
type BatchConfig struct {
	RunID uuid.UUID
	// SkipPrecondition, önkoşul denetimini atlar.
	//
	// Yalnızca birim/entegrasyon testleri içindir. Üretim yolunda **asla**
	// kullanılmaz: atlanması, eksik taleple koşmanın ve düşük recall'u
	// bilimsel bulgu sanmanın kapısını açar.
	SkipPrecondition bool
	Logger           *slog.Logger
}

// Batch, `hts_records` taramasını kural motoruna besler.
type Batch struct {
	reader BatchReader
	engine *detector.Engine
	cfg    BatchConfig
	log    *slog.Logger

	sequences int64
}

// NewBatch, toplu fazı kurar.
func NewBatch(cfg BatchConfig, reader BatchReader, engine *detector.Engine) (*Batch, error) {
	switch {
	case reader == nil:
		return nil, fmt.Errorf("bütünlük toplu fazı: okuyucu zorunlu")
	case engine == nil:
		return nil, fmt.Errorf("bütünlük toplu fazı: kural motoru zorunlu")
	case cfg.RunID == uuid.Nil:
		return nil, fmt.Errorf("bütünlük toplu fazı: run_id zorunlu (ADR-05)")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Batch{reader: reader, engine: engine, cfg: cfg, log: log}, nil
}

// CheckPrecondition, iki önkoşulu denetler ve sağlanmazsa hata döndürür.
func (b *Batch) CheckPrecondition(ctx context.Context) error {
	counters, err := b.reader.RunStatusOf(ctx, b.cfg.RunID)
	if err != nil {
		return err
	}

	if counters.PublishedEvents == nil {
		return fmt.Errorf(
			"toplu faz önkoşulu: koşu %s için published_events yazılmamış — "+
				"simülasyon tamamlanmadı (ADR-23)", b.cfg.RunID)
	}

	stored, err := b.reader.CountHTSRecords(ctx, b.cfg.RunID)
	if err != nil {
		return err
	}
	if stored != *counters.PublishedEvents {
		return fmt.Errorf(
			"toplu faz önkoşulu: hts_records eksik (%d/%d) — "+
				"kayıt kalıcılaştırıcısı bitmedi; eksik diziyle koşmak recall'u "+
				"sessizce düşürürdü", stored, *counters.PublishedEvents)
	}

	if counters.InspectedRecords == nil {
		return fmt.Errorf(
			"toplu faz önkoşulu: koşu %s için inspected_records yazılmamış — "+
				"akış fazı (kural 1 ve 3) koşmadı. Talep defteri eksik olurdu ve "+
				"kural 2 bilinmeyen konumlu kayıtları zincire alırdı (ADR-27/3, ADR-28/6)",
			b.cfg.RunID)
	}
	if *counters.InspectedRecords != *counters.PublishedEvents {
		return fmt.Errorf(
			"toplu faz önkoşulu: akış fazı yarım kaldı (%d/%d incelendi) — "+
				"düşük recall bilimsel bulgu gibi görünürdü (ADR-31/7)",
			*counters.InspectedRecords, *counters.PublishedEvents)
	}
	return nil
}

// Run, koşunun abone dizilerini sırayla motora verir.
func (b *Batch) Run(ctx context.Context) error {
	if !b.cfg.SkipPrecondition {
		if err := b.CheckPrecondition(ctx); err != nil {
			return err
		}
	} else {
		b.log.Warn("toplu faz önkoşulu atlanıyor — yalnızca test yolunda beklenir")
	}

	err := b.reader.ForEachSubscriberSequence(ctx, b.cfg.RunID,
		func(raw postgres.SubscriberSequence) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			b.sequences++
			return b.engine.Evaluate(ctx, toSequence(raw))
		})
	if err != nil {
		return fmt.Errorf("bütünlük toplu fazı: %w", err)
	}

	if err := b.engine.Flush(ctx); err != nil {
		return fmt.Errorf("bütünlük toplu fazı: %w", err)
	}

	st := b.engine.Stats()
	b.log.Info("toplu faz tamamlandı",
		"abone", b.sequences, "kayıt", st.Inspected,
		"kanonik", st.Canonical, "bastırılmış", st.Suppressed, "yazılan", st.Written)
	return nil
}

// Sequences, işlenen abone dizisi sayısıdır.
func (b *Batch) Sequences() int64 { return b.sequences }

// toSequence, veritabanı satırlarını dedektör dizisine çevirir.
func toSequence(raw postgres.SubscriberSequence) detector.Sequence {
	seq := detector.Sequence{
		Subscriber: raw.Subscriber,
		Records:    make([]detector.Record, 0, len(raw.Records)),
	}
	for _, r := range raw.Records {
		seq.Records = append(seq.Records, detector.Record{
			RunID:      r.RunID,
			EventID:    r.EventID,
			Time:       r.Time,
			Subscriber: r.PseudoMSISDN,
			Device:     r.PseudoIMEI,
			EventType:  r.EventType,
			CellID:     r.CellID,
			TAValue:    r.TAValue,
			Scenario:   r.Scenario,
		})
	}
	return seq
}
