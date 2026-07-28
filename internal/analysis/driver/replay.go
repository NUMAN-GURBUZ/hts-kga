// S5 hazırlığı — Replay sürücüsü (G7).
//
// Kalibrasyon döngüsü (ADR-02) aynı olay kümesini 12 farklı λ ile yeniden
// koşar. Kafka akışı bunu yapamaz; replay sürücüsü kayıtları bellekten (ya da
// ileride `hts_records` tablosundan) istenildiği kadar tekrar besler.
//
// Sürücü, Consumer ile **aynı** `Engine.Process` çağrısını yapar: kalibrasyonda
// bulunan λ*, ölçümde kullanılan motorla birebir aynı motora aittir.

package driver

import (
	"context"
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
)

// Replay, sabit bir kayıt kümesi üzerinde motoru koşturur.
type Replay struct {
	engine  *Engine
	records []htswire.Record
}

// NewReplay, verilen kayıtlar üzerinde çalışan bir sürücü kurar.
//
// Kayıt sırası korunur: aynı sıra, aynı kayan nokta sonuçları (K10).
func NewReplay(engine *Engine, records []htswire.Record) (*Replay, error) {
	if engine == nil {
		return nil, fmt.Errorf("replay: motor zorunlu")
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("replay: kayıt kümesi boş")
	}
	return &Replay{engine: engine, records: append([]htswire.Record(nil), records...)}, nil
}

// Len, kümedeki kayıt sayısını döndürür.
func (r *Replay) Len() int { return len(r.records) }

// Engine, sürücünün motorunu döndürür.
func (r *Replay) Engine() *Engine { return r.engine }

// WithLambda, aynı kayıtlar üzerinde farklı λ ile çalışan bir sürücü döndürür.
//
// Kalibrasyonun bisection döngüsü her iterasyonda bunu çağırır; kayıt kümesi
// kopyalanmaz, paylaşılır.
func (r *Replay) WithLambda(lambda float64) (*Replay, error) {
	engine, err := r.engine.WithLambda(lambda)
	if err != nil {
		return nil, err
	}
	return &Replay{engine: engine, records: r.records}, nil
}

// Run, tüm kayıtları işler ve sonuçları işleyiciye verir.
//
// Consumer'dan farklı olarak hata **yutulmaz**: replay bir ölçüm koşusudur,
// eksik sonuç kalibrasyonu sessizce bozardı.
func (r *Replay) Run(ctx context.Context, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("replay: işleyici zorunlu")
	}

	for i, rec := range r.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := r.engine.Process(rec)
		if err != nil {
			return fmt.Errorf("replay: kayıt[%d] (olay %s): %w", i, rec.EventID, err)
		}
		if err := handler.Handle(ctx, rec, result); err != nil {
			return fmt.Errorf("replay: kayıt[%d] teslim edilemedi: %w", i, err)
		}
	}
	return nil
}

// Collect, tüm kayıtların sonuçlarını olay kimliğine göre toplar.
//
// Kalibrasyon ve determinizm testleri için kullanılır.
func (r *Replay) Collect(ctx context.Context) (map[string]core.Result, error) {
	out := make(map[string]core.Result, len(r.records))
	err := r.Run(ctx, HandlerFunc(func(_ context.Context, rec htswire.Record, res core.Result) error {
		out[rec.EventID.String()] = res
		return nil
	}))
	if err != nil {
		return nil, err
	}
	return out, nil
}
