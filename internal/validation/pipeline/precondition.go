// T-E04-02 — Koşu tamamlanma önkoşulu (ADR-04, ADR-23).
//
// # Neden bir önkoşul
//
// Yarım koşunun üzerinde hesaplanan metrik, yanlış olduğunu söylemeden
// yanlıştır. Simülasyon 8.640 tick'in 6.000'inde kesilmişse kapsama oranı
// yine bir sayı üretir; o sayı rapora girer ve kimse fark etmez.
//
// ADR-04 doğrulamayı bilinçli olarak toplu işe (batch) çevirdi ve yarış
// koşulunu "önce bitmesini bekle" diyerek çözdü. Bu dosya o beklemenin
// denetlenebilir hâlidir: her koşul ayrı ayrı ölçülür ve hangisinin
// kırıldığı sayılarıyla birlikte raporlanır.
//
// # Kafka lag yerine sayım denkliği (ADR-23/3)
//
// ADR-04 "consumer lag = 0" diyordu. Lag sıfır olması mesajın **işlendiğini**
// söyler, veritabanına **yazıldığını** söylemez; ayrıca tüketici çökmüşse de
// sıfır olabilir. Bunun yerine yayınlanan ile yazılan satır sayısı
// karşılaştırılır: doğrudan sorulan sorunun doğrudan cevabı budur.

package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
)

// Precondition, bir önkoşul denetiminin sonucudur.
type Precondition struct {
	Name   string
	Passed bool
	Detail string
}

// PreconditionError, karşılanmayan önkoşulları taşır.
type PreconditionError struct {
	RunID  uuid.UUID
	Checks []Precondition
}

// Error, hangi denetimlerin kırıldığını sayılarıyla birlikte bildirir.
func (e *PreconditionError) Error() string {
	var failed []string
	for _, c := range e.Checks {
		if !c.Passed {
			failed = append(failed, fmt.Sprintf("%s (%s)", c.Name, c.Detail))
		}
	}
	return fmt.Sprintf("koşu %s doğrulamaya hazır değil: %s",
		e.RunID, strings.Join(failed, "; "))
}

// CheckPreconditions, koşunun doğrulanmaya hazır olup olmadığını denetler.
//
// Denetimler **tamamı** koşulur ve hepsi raporlanır: ilk hatada durmak,
// kullanıcıyı hataları teker teker düzeltmeye zorlardı.
func CheckPreconditions(ctx context.Context, pool *postgres.Pool,
	runID uuid.UUID) ([]Precondition, error) {

	status, err := pool.RunStatusOf(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("önkoşul: koşu durumu okunamadı: %w", err)
	}

	records, err := pool.CountHTSRecords(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("önkoşul: %w", err)
	}
	truths, err := pool.CountGroundTruth(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("önkoşul: %w", err)
	}
	estimates, err := pool.CountEstimates(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("önkoşul: %w", err)
	}

	checks := []Precondition{
		simulationFinished(status),
		recordsPersisted(status, records),
		truthPersisted(records, truths),
		analysisComplete(status, estimates),
	}

	for _, c := range checks {
		if !c.Passed {
			return checks, &PreconditionError{RunID: runID, Checks: checks}
		}
	}
	return checks, nil
}

// simulationFinished, simülasyonun tamamlandığını denetler.
func simulationFinished(s postgres.RunStatus) Precondition {
	if s.Finished() {
		return Precondition{
			Name:   "simülasyon_tamamlandı",
			Passed: true,
			Detail: s.FinishedAt.UTC().Format("2006-01-02 15:04:05Z"),
		}
	}
	return Precondition{
		Name:   "simülasyon_tamamlandı",
		Passed: false,
		Detail: "finished_at boş — koşu yarıda kalmış ya da hâlâ sürüyor",
	}
}

// recordsPersisted, yayınlanan kayıtların tamamının yazıldığını denetler
// (ADR-23 denetim 3).
func recordsPersisted(s postgres.RunStatus, stored int64) Precondition {
	const name = "kayıtlar_yazıldı"

	if s.PublishedEvents == nil {
		return Precondition{Name: name, Passed: false,
			Detail: "published_events boş — simülatör koşuyu kapatmamış"}
	}
	if stored != *s.PublishedEvents {
		return Precondition{Name: name, Passed: false,
			Detail: fmt.Sprintf("yayınlanan %d, yazılan %d — persister geride ya da satır düşmüş",
				*s.PublishedEvents, stored)}
	}
	return Precondition{Name: name, Passed: true,
		Detail: fmt.Sprintf("%d kayıt", stored)}
}

// truthPersisted, ground truth satırlarının kayıtlardan az olmadığını
// denetler.
//
// Eşitlik **beklenmez**: enjeksiyon kural 4 kaydı siler ve yalnızca ground
// truth bırakır (ADR-09); kapsama dışı olaylar da yalnızca GT üretir
// (ADR-08/3). Bu yüzden ölçüt `GT ≥ kayıt`tır.
func truthPersisted(records, truths int64) Precondition {
	const name = "ground_truth_yazıldı"

	if truths < records {
		return Precondition{Name: name, Passed: false,
			Detail: fmt.Sprintf("ground truth %d < kayıt %d — GT persister geride",
				truths, records)}
	}
	return Precondition{Name: name, Passed: true,
		Detail: fmt.Sprintf("%d satır (%d kayıtsız: kural 4 + kapsama dışı)",
			truths, truths-records)}
}

// analysisComplete, analizin örnekleme kümesini bitirdiğini denetler
// (ADR-23 denetim 4).
//
// Ölçüt `estimates = 5 × analyzed_events`'tir: olay başına B0, B1 ve üç M
// satırı yazılır. Örnekleme oranı ne olursa olsun bu oran sabittir — ADR-04'ün
// özgün `estimates = 5 × hts_records` ölçütü ADR-14 örneklemesiyle
// çelişiyordu (ADR-23).
func analysisComplete(s postgres.RunStatus, estimates int64) Precondition {
	const name = "analiz_tamamlandı"
	const rowsPerEvent = 5

	if s.AnalyzedEvents == nil {
		return Precondition{Name: name, Passed: false,
			Detail: "analyzed_events boş — analiz motoru kapanışta sayacı yazmamış"}
	}
	want := *s.AnalyzedEvents * rowsPerEvent
	if estimates != want {
		return Precondition{Name: name, Passed: false,
			Detail: fmt.Sprintf("estimates %d, beklenen %d (%d olay × %d satır)",
				estimates, want, *s.AnalyzedEvents, rowsPerEvent)}
	}
	return Precondition{Name: name, Passed: true,
		Detail: fmt.Sprintf("%d olay, %d tahmin satırı", *s.AnalyzedEvents, estimates)}
}
