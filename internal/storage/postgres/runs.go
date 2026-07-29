// ADR-05 · ADR-23 — Koşu yaşam döngüsü sütunları.
//
// `run_config` satırı koşu **başlarken** açılır (InsertRun, T-E02-06); bu
// dosya koşu ilerledikçe güncellenen alanları yönetir:
//
//	finished_at       simülasyon tamamlandı (ADR-04 önkoşulu)
//	published_events  Kafka'ya yayınlanan kayıt sayısı (ADR-23 denetim 3)
//	analyzed_events   örnekleme sonrası analiz edilen olay sayısı (ADR-23 denetim 4)
//	lambda            kalibre edilen λ* (ADR-02)
//
// Neden ayrı yazılıyorlar: her biri farklı bir servis tarafından, farklı
// zamanda belirlenir. Tek bir "koşu bitti" güncellemesi, sayıları bilmeyen
// servisin diğerini beklemesini gerektirirdi.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CompleteRun, simülasyonun tamamlandığını işaretler.
//
// `finished_at` yalnızca burada yazılır: yarım kalan bir koşuda NULL kalır ve
// doğrulama önkoşulu (ADR-04) çalışmayı reddeder. Bu, yarım veri üzerinde
// hesaplanmış bir metriğin sessizce rapora girmesini engelleyen ilk savunmadır.
func (p *Pool) CompleteRun(ctx context.Context, runID uuid.UUID, publishedEvents int64) error {
	if runID == uuid.Nil {
		return fmt.Errorf("koşu tamamlama: run_id boş (ADR-05)")
	}
	if publishedEvents < 0 {
		return fmt.Errorf("koşu tamamlama: yayınlanan kayıt sayısı negatif (%d)", publishedEvents)
	}

	tag, err := p.pool.Exec(ctx, `
        UPDATE run_config
           SET finished_at = now(), published_events = $2
         WHERE run_id = $1::uuid`, runID.String(), publishedEvents)
	if err != nil {
		return fmt.Errorf("koşu tamamlanamadı (%s): %w", runID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("koşu tamamlanamadı: run_config satırı yok (%s)", runID)
	}
	return nil
}

// SetAnalyzedEvents, analiz motorunun işlediği olay sayısını yazar (ADR-23).
//
// Örnekleme (ADR-14) yüzünden bu sayı `hts_records`'tan küçüktür; bütünlük
// denetimi `estimates = 5 × analyzed_events` beklentisini buradan kurar.
func (p *Pool) SetAnalyzedEvents(ctx context.Context, runID uuid.UUID, analyzed int64) error {
	if analyzed < 0 {
		return fmt.Errorf("analiz sayacı: negatif değer (%d)", analyzed)
	}
	tag, err := p.pool.Exec(ctx, `
        UPDATE run_config SET analyzed_events = $2 WHERE run_id = $1::uuid`,
		runID.String(), analyzed)
	if err != nil {
		return fmt.Errorf("analiz sayacı yazılamadı (%s): %w", runID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("analiz sayacı yazılamadı: run_config satırı yok (%s)", runID)
	}
	return nil
}

// SetLambda, kalibre edilen λ*'ı yazar (ADR-02).
func (p *Pool) SetLambda(ctx context.Context, runID uuid.UUID, lambda float64) error {
	if !(lambda > 0) {
		return fmt.Errorf("λ yazımı: pozitif olmalı (%g)", lambda)
	}
	tag, err := p.pool.Exec(ctx, `
        UPDATE run_config SET lambda = $2 WHERE run_id = $1::uuid`,
		runID.String(), lambda)
	if err != nil {
		return fmt.Errorf("λ yazılamadı (%s): %w", runID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("λ yazılamadı: run_config satırı yok (%s)", runID)
	}
	return nil
}

// RunStatus, bir koşunun yaşam döngüsü durumudur.
type RunStatus struct {
	RunID           uuid.UUID
	Scenario        string
	Seed            int64
	Lambda          *float64
	StartedAt       time.Time
	FinishedAt      *time.Time
	PublishedEvents *int64
	AnalyzedEvents  *int64
}

// Finished, simülasyonun tamamlanıp tamamlanmadığını bildirir.
func (s RunStatus) Finished() bool { return s.FinishedAt != nil }

// RunStatusOf, koşunun durumunu okur.
func (p *Pool) RunStatusOf(ctx context.Context, runID uuid.UUID) (RunStatus, error) {
	var s RunStatus
	err := p.pool.QueryRow(ctx, `
        SELECT run_id, scenario, seed, lambda, started_at, finished_at,
               published_events, analyzed_events
          FROM run_config
         WHERE run_id = $1::uuid`, runID.String()).
		Scan(&s.RunID, &s.Scenario, &s.Seed, &s.Lambda, &s.StartedAt, &s.FinishedAt,
			&s.PublishedEvents, &s.AnalyzedEvents)
	if err != nil {
		return RunStatus{}, fmt.Errorf("koşu durumu okunamadı (%s): %w", runID, err)
	}
	return s, nil
}
