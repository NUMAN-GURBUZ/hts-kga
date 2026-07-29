// Package run, simülatörün koşu döngüsüdür (G1).
//
// Sprint 1–2'de yazılan parçaları tek bir akışta birleştirir:
//
//	envanter → ajan yerleşimi → tick döngüsü → best-server → Poisson olay
//	  → kayıt çifti (HTS + ground truth) → enjeksiyon → Kafka
//
// # Neden ayrı paket
//
// `cmd/simulator` yalnızca yapılandırma okur ve bu paketi çağırır. Döngünün
// kendisi test edilebilir olmalıdır: 10 ajanlık bir koşu, ana fonksiyonu
// çağırmadan, Kafka olmadan (bellekteki sink ile) koşturulabilir.
//
// # Determinizm (K10)
//
// Koşunun her rastgelelik kaynağı tohumdan türetilir ve **konumsaldır**:
//
//	ajan yerleşimi   : (seed, stream)               — agent/rng.go
//	rutin/hareket    : (seed, ajan, tick)           — agent/mobility.go
//	gölgeleme        : (site, konum) karma           — radio/shadowing.go
//	olay sayısı      : (seed, ajan, tick)            — event/generator.go
//	enjeksiyon       : (seed, ajan, tick, seq)       — event/injector
//
// Hiçbiri çağrı sırasına bağlı değildir; bu yüzden World.Tick'in 12 goroutine
// kullanması sonucu değiştirmez. Olayların **yayın sırası** ise tick ve ajan
// indeksine göre sabittir: aynı seed iki koşuda aynı diziyi verir.

package run

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/agent"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event/injector"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// publishBatch, tek seferde yayınlanan kayıt çifti sayısıdır.
//
// Yayın maliyeti mesaj başına sabit bir ek yük taşır; 500'lük partiler bu yükü
// amorti eder ama bellekte tutulan çift sayısını da sınırlar. Tick başına
// beklenen olay sayısı ~0,07 · 1000 = 70 olduğundan parti birkaç tick'te dolar.
const publishBatch = 500

// progressEveryTicks, ilerleme günlüğünün sıklığıdır (bir gün = 288 tick).
const progressEveryTicks = 288

// Stats, bir koşunun özetidir.
type Stats struct {
	// Ticks, yürütülen tick sayısıdır.
	Ticks int
	// Events, üretilen toplam olay sayısıdır (kapsama dışı dâhil değil).
	Events int64
	// PublishedRecords, `hts.records` topic'ine giden kayıt sayısıdır.
	//
	// Enjeksiyon kural 4 kaydı sildiği için Events'ten küçük olabilir
	// (ADR-09); run_config.published_events bu değeri alır (ADR-23).
	PublishedRecords int64
	// PublishedTruths, `hts.groundtruth` topic'ine giden satır sayısıdır.
	PublishedTruths int64
	// Injected, enjeksiyon uygulanan olay sayısıdır.
	Injected int64
	// Uncovered, kapsama dışı kalıp olay üretmeyen gözlem sayısıdır (ADR-08).
	Uncovered int64
	// Coverage, ajan-tick düzeyindeki kapsama istatistiğidir.
	Coverage agent.CoverageStats
	// Duration, koşu süresidir.
	Duration time.Duration
}

// Config, koşunun kurulum parametreleridir.
type Config struct {
	// RunID, koşu kimliğidir (ADR-05).
	RunID uuid.UUID
	// Scenario, yüklenmiş senaryo yapılandırmasıdır.
	Scenario *config.Scenario
	// Inventory, kurulmuş şebeke envanteridir.
	Inventory *inventory.Inventory
	// Projector, ENU izdüşümüdür (envanterle aynı başlangıç).
	Projector *geo.Projector
	// Pseudonymizer, takma ad üreticisidir (HMAC_SALT'tan).
	Pseudonymizer *event.Pseudonymizer
	// Sink, Kafka üreticisidir (ya da testte bellekteki toplayıcı).
	Sink event.Sink
	// RunStart, tick 0'ın takvim karşılığıdır.
	RunStart time.Time
	// Workers, ajan güncellemesinde kullanılacak goroutine sayısıdır; 0 ise
	// çekirdek sayısı kullanılır.
	Workers int
	// Logger, isteğe bağlıdır.
	Logger *slog.Logger
}

// Runner, bir koşuyu yürütür.
type Runner struct {
	cfg       Config
	world     *agent.World
	generator *event.Generator
	builder   *event.Builder
	injector  *injector.Injector
	publisher *event.Publisher
	clock     agent.Clock
	ticks     int
	log       *slog.Logger
}

// New, koşuyu kurar.
//
// Kurulum sırası bilinçlidir: şebeke → gölgeleme → seçici → ajan yerleşimi.
// Ajan yerleşimi (ADR-08 rejection sampling) kapsama sondası olarak seçiciyi
// kullanır, bu yüzden radyo katmanı önce hazır olmalıdır.
func New(cfg Config) (*Runner, error) {
	switch {
	case cfg.RunID == uuid.Nil:
		return nil, fmt.Errorf("koşu: run_id zorunlu (ADR-05)")
	case cfg.Scenario == nil:
		return nil, fmt.Errorf("koşu: senaryo zorunlu")
	case cfg.Inventory == nil:
		return nil, fmt.Errorf("koşu: envanter zorunlu")
	case cfg.Projector == nil:
		return nil, fmt.Errorf("koşu: izdüşüm zorunlu")
	case cfg.Pseudonymizer == nil:
		return nil, fmt.Errorf("koşu: takma ad üreticisi zorunlu")
	case cfg.Sink == nil:
		return nil, fmt.Errorf("koşu: yayın hedefi zorunlu")
	}

	scn := cfg.Scenario
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	net, err := buildNetwork(cfg.Inventory)
	if err != nil {
		return nil, err
	}
	field, err := radio.NewShadowingField(scn.Run.Seed, rf.UTHeightM)
	if err != nil {
		return nil, fmt.Errorf("koşu: gölgeleme alanı: %w", err)
	}
	selector, err := radio.NewSelector(net, field, scn.Network.RxSensitivityDBm, rf.UTHeightM)
	if err != nil {
		return nil, fmt.Errorf("koşu: best-server seçicisi: %w", err)
	}

	placement, err := agent.PlacementConfigFrom(scn, selector)
	if err != nil {
		return nil, fmt.Errorf("koşu: yerleşim yapılandırması: %w", err)
	}
	agents, err := agent.PlaceAgents(placement)
	if err != nil {
		return nil, fmt.Errorf("koşu: ajan yerleşimi: %w", err)
	}

	clock, err := agent.NewClock(scn.Simulation.TickMinutes)
	if err != nil {
		return nil, fmt.Errorf("koşu: saat: %w", err)
	}
	mobility := agent.NewMobility(scn.Run.Seed, clock)

	world, err := agent.NewWorld(agents, mobility, selector, cfg.Workers)
	if err != nil {
		return nil, fmt.Errorf("koşu: dünya: %w", err)
	}

	generator, err := event.NewGenerator(scn.Run.Seed, clock)
	if err != nil {
		return nil, fmt.Errorf("koşu: olay üreteci: %w", err)
	}

	builder, err := newBuilder(cfg, clock)
	if err != nil {
		return nil, err
	}

	inj, err := newInjector(cfg)
	if err != nil {
		return nil, err
	}

	publisher, err := event.NewPublisher(cfg.Sink)
	if err != nil {
		return nil, fmt.Errorf("koşu: yayıncı: %w", err)
	}

	return &Runner{
		cfg:       cfg,
		world:     world,
		generator: generator,
		builder:   builder,
		injector:  inj,
		publisher: publisher,
		clock:     clock,
		ticks:     scn.Simulation.DurationDays * clock.TicksPerDay(),
		log:       log,
	}, nil
}

// newBuilder, kayıt üreticisini kurar.
func newBuilder(cfg Config, clock agent.Clock) (*event.Builder, error) {
	scn := cfg.Scenario

	var tech ta.Technology
	if scn.TimingAdvance.Enabled {
		parsed, err := ta.Parse(scn.TimingAdvance.Technology)
		if err != nil {
			return nil, fmt.Errorf("koşu: TA teknolojisi: %w", err)
		}
		tech = parsed
	}

	splitter, err := split.New(scn.Run.Seed, scn.Calibration.SplitRatio)
	if err != nil {
		return nil, fmt.Errorf("koşu: bölümleyici: %w", err)
	}

	builder, err := event.NewBuilder(event.BuilderConfig{
		RunID:         cfg.RunID,
		Scenario:      scn.Run.Scenario,
		RunStart:      cfg.RunStart,
		Clock:         clock,
		Projector:     cfg.Projector,
		Pseudonymizer: cfg.Pseudonymizer,
		Splitter:      splitter,
		Technology:    tech,
		TAEnabled:     scn.TimingAdvance.Enabled,
	})
	if err != nil {
		return nil, fmt.Errorf("koşu: kayıt üreticisi: %w", err)
	}
	return builder, nil
}

// newInjector, enjeksiyon aşamasını kurar (ADR-09).
func newInjector(cfg Config) (*injector.Injector, error) {
	cells := make([]injector.CellRef, 0, len(cfg.Inventory.Cells))
	for _, c := range cfg.Inventory.Cells {
		cells = append(cells, injector.CellRef{ID: c.ID, ENU: c.ENU})
	}

	inj, err := injector.New(cfg.Scenario.Run.Seed, injector.Config{
		Rate:      cfg.Scenario.Integrity.InjectionRate,
		Weights:   cfg.Scenario.Integrity.RuleWeights,
		TimeShift: injector.DefaultTimeShift,
	}, cells)
	if err != nil {
		return nil, fmt.Errorf("koşu: enjektör: %w", err)
	}
	return inj, nil
}

// Ticks, koşunun tick sayısını döndürür.
func (r *Runner) Ticks() int { return r.ticks }

// AgentCount, popülasyon büyüklüğünü döndürür.
func (r *Runner) AgentCount() int { return r.world.AgentCount() }

// Run, koşuyu yürütür ve özet döndürür.
//
// Bağlam iptal edilirse koşu **yarıda kesilir** ve o ana kadarki özet hatayla
// birlikte döner: yarım koşu sessizce tam koşu gibi görünmemelidir. Yarım
// koşunun `run_config.finished_at`'i yazılmaz, doğrulama önkoşulu (ADR-04)
// bunu yakalar.
func (r *Runner) Run(ctx context.Context) (Stats, error) {
	defer r.world.Close()

	started := time.Now()
	stats := Stats{}
	batch := make([]event.Pair, 0, publishBatch)

	for tick := 0; tick < r.ticks; tick++ {
		if err := ctx.Err(); err != nil {
			stats.Duration = time.Since(started)
			return stats, fmt.Errorf("koşu tick %d/%d'de kesildi: %w", tick, r.ticks, err)
		}

		r.world.Tick(tick)

		for _, obs := range r.world.Observations() {
			count := r.generator.Count(obs.AgentID, tick)
			if count == 0 {
				continue
			}
			if !obs.Serving.Covered {
				// ADR-08/3: kapsama dışında olay yazılmaz; ground truth yine
				// üretilir ki kapsama dışı olaylar ölçülebilsin.
				stats.Uncovered += int64(count)
			}

			for seq := 0; seq < count; seq++ {
				pair, err := r.builder.Build(obs, seq)
				if err != nil {
					return stats, fmt.Errorf("koşu: kayıt üretimi (ajan %d, tick %d, seq %d): %w",
						obs.AgentID, tick, seq, err)
				}

				pair = r.injector.Apply(pair, r.cfg.Pseudonymizer, obs.AgentID, tick, seq)
				if pair.Truth.InjectedRule != nil {
					stats.Injected++
				}

				stats.Events++
				batch = append(batch, pair)

				if len(batch) >= publishBatch {
					if err := r.flush(ctx, batch, &stats); err != nil {
						return stats, err
					}
					batch = batch[:0]
				}
			}
		}

		if (tick+1)%progressEveryTicks == 0 {
			r.log.Info("koşu ilerliyor",
				"tick", tick+1, "toplam_tick", r.ticks,
				"gün", r.clock.DayIndex(tick)+1,
				"olay", stats.Events, "geçen", time.Since(started).Round(time.Second))
		}
	}

	if err := r.flush(ctx, batch, &stats); err != nil {
		return stats, err
	}

	stats.Ticks = r.ticks
	stats.Coverage = r.world.Stats()
	stats.Duration = time.Since(started)
	return stats, nil
}

// flush, biriken çiftleri yayınlar ve sayaçları günceller.
func (r *Runner) flush(ctx context.Context, batch []event.Pair, stats *Stats) error {
	if len(batch) == 0 {
		return nil
	}
	if err := r.publisher.Publish(ctx, batch...); err != nil {
		return fmt.Errorf("koşu: yayın başarısız (%d çift): %w", len(batch), err)
	}
	for _, p := range batch {
		if p.Record != nil {
			stats.PublishedRecords++
		}
		stats.PublishedTruths++
	}
	return nil
}
