// Package driver, saf çekirdeği (internal/analysis/core) iki ayrı kaynağa
// bağlar (G7):
//
//	Consumer : Kafka `hts.records` akışı        — T-E03-01
//	Replay   : bellekten/DB'den kayıt listesi   — S5 kalibrasyon döngüsü
//
// # Neden iki sürücü
//
// Kalibrasyon (ADR-02) aynı 5.000 olayı 12 farklı λ ile yeniden koşar. Bunu
// Kafka akışından yapmak mümkün değildir: akış bir kez tüketilir, geri sarmak
// offset yönetimi ve yeniden dengeleme demektir. Replay sürücüsü aynı kayıtları
// istenildiği kadar tekrar besler.
//
// İkisinin de tek bir saf fonksiyonu (`core.Estimate`) çağırması zorunludur:
// aksi hâlde kalibrasyonda bulunan λ*, ölçümde kullanılan motorla birebir aynı
// motora ait olmazdı. `TestDriverDeterminism` bunu 100 olayda bit düzeyinde
// doğrular — K10'un S3 karşılığı.
package driver

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// Engine, tel biçimindeki kayıtları kütleye çeviren sürücü çekirdeğidir.
//
// Değişmezdir; birden çok tüketici goroutine'i paylaşabilir.
type Engine struct {
	inventory *params.Inventory
	grid      *density.Grid
	options   core.Options
	tech      ta.Technology
	taEnabled bool
}

// EngineConfig, sürücü çekirdeğinin kurulum parametreleridir.
type EngineConfig struct {
	// Inventory, koşunun şebeke envanteridir (T-E03-02).
	Inventory *params.Inventory
	// Grid, kütle ızgarasıdır (T-E03-05).
	Grid *density.Grid
	// Options, kütle üretiminin ayarlarıdır (λ dâhil — T-E03-14).
	Options core.Options
	// Technology, TA teknolojisidir (senaryo config'i).
	Technology ta.Technology
	// TAEnabled, senaryoda TA olup olmadığıdır.
	TAEnabled bool
}

// NewEngine, sürücü çekirdeğini kurar.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	switch {
	case cfg.Inventory == nil:
		return nil, fmt.Errorf("sürücü: envanter zorunlu")
	case cfg.Grid == nil:
		return nil, fmt.Errorf("sürücü: ızgara zorunlu")
	case cfg.TAEnabled && !cfg.Technology.Valid():
		return nil, fmt.Errorf("sürücü: TA açık ama teknoloji geçersiz")
	}
	if err := cfg.Options.Config.Validate(); err != nil {
		return nil, fmt.Errorf("sürücü: %w", err)
	}

	return &Engine{
		inventory: cfg.Inventory,
		grid:      cfg.Grid,
		options:   cfg.Options,
		tech:      cfg.Technology,
		taEnabled: cfg.TAEnabled,
	}, nil
}

// Lambda, motorun kalibrasyon parametresini döndürür.
func (e *Engine) Lambda() float64 { return e.options.Config.Lambda }

// WithLambda, yalnızca λ'sı değişmiş bir kopya döndürür (ADR-02).
//
// Kalibrasyon döngüsü motoru yeniden kurmak yerine bunu çağırır: envanter ve
// ızgara paylaşılır, yalnızca parametre değişir. Kopya olduğu için eşzamanlı
// koşan başka bir sürücü etkilenmez.
func (e *Engine) WithLambda(lambda float64) (*Engine, error) {
	next := *e
	next.options.Config.Lambda = lambda
	if err := next.options.Config.Validate(); err != nil {
		return nil, fmt.Errorf("sürücü: λ=%g: %w", lambda, err)
	}
	return &next, nil
}

// Process, tel biçimindeki bir kaydı kütleye çevirir.
//
// TA senaryoda kapalıysa kayıttaki `ta_value` **yok sayılır**: senaryo
// tanımı kaydın içeriğinden önceliklidir, aksi hâlde bozuk bir üretici
// TA'sız senaryoya TA sızdırabilirdi.
func (e *Engine) Process(rec htswire.Record) (core.Result, error) {
	var taValue *int
	if e.taEnabled {
		taValue = rec.TAValue
	}

	return core.Estimate(core.Record{
		EventID:    rec.EventID,
		CellID:     rec.CellID,
		TAValue:    taValue,
		Technology: e.tech,
	}, e.inventory, e.grid, e.options)
}
