package detector

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// ─── Test yardımcıları ────────────────────────────────────────────────────────

// memSink, bulguları bellekte toplayan yazıcıdır.
type memSink struct {
	findings []Finding
	seen     map[string]bool // (event_id, rule_id) — şemanın tekillik kısıtını taklit eder
	err      error
}

func newMemSink() *memSink { return &memSink{seen: make(map[string]bool)} }

func (s *memSink) WriteFindings(_ context.Context, findings []Finding) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	var inserted int64
	for _, f := range findings {
		key := f.EventID.String() + ":" + f.Rule.String()
		if s.seen[key] {
			continue // ON CONFLICT DO NOTHING
		}
		s.seen[key] = true
		s.findings = append(s.findings, f)
		inserted++
	}
	return inserted, nil
}

func (s *memSink) canonicalOf(eventID uuid.UUID) (Finding, bool) {
	for _, f := range s.findings {
		if f.EventID == eventID && f.Canonical() {
			return f, true
		}
	}
	return Finding{}, false
}

// fakeStreamRule, verilen olaylarda isabet üreten sahte akış kuralıdır.
type fakeStreamRule struct {
	id       integrityrule.ID
	hitOn    map[uuid.UUID]bool
	margin   float64
	evidence func(integrityrule.ID) Evidence
}

func (r fakeStreamRule) ID() integrityrule.ID { return r.id }

func (r fakeStreamRule) Observe(rec Record) []Hit {
	if !r.hitOn[rec.EventID] {
		return nil
	}
	margin := r.margin
	if margin == 0 {
		margin = 1.0
	}
	ev := NewEvidence(r.id, DefaultMarginCap).Str("kaynak", "test").MustBuild()
	if r.evidence != nil {
		ev = r.evidence(r.id)
	}
	return []Hit{{
		Rule:     r.id,
		EventID:  rec.EventID,
		Time:     rec.Time,
		Scenario: rec.Scenario,
		Margin:   margin,
		Evidence: ev,
	}}
}

// fakeBatchRule, dizinin verilen indekslerinde isabet üreten sahte toplu kuraldır.
type fakeBatchRule struct {
	id    integrityrule.ID
	hitOn map[uuid.UUID]bool
}

func (r fakeBatchRule) ID() integrityrule.ID { return r.id }

func (r fakeBatchRule) Evaluate(seq Sequence) []Hit {
	var hits []Hit
	for _, rec := range seq.Records {
		if !r.hitOn[rec.EventID] {
			continue
		}
		hits = append(hits, Hit{
			Rule:     r.id,
			EventID:  rec.EventID,
			Time:     rec.Time,
			Scenario: rec.Scenario,
			Margin:   2.0,
			Evidence: NewEvidence(r.id, DefaultMarginCap).Str("kaynak", "test").MustBuild(),
		})
	}
	return hits
}

var baseTime = time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

func record(t *testing.T, subscriber string, offset time.Duration) Record {
	t.Helper()
	return Record{
		RunID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		EventID:    uuid.New(),
		Time:       baseTime.Add(offset),
		Subscriber: subscriber,
		Device:     "imei-" + subscriber,
		EventType:  "MOC",
		CellID:     uuid.New(),
		Scenario:   "A",
	}
}

// ─── Faz ve sınıf kısıtı (ADR-27/1) ───────────────────────────────────────────

// TestNewStreamEngine_RejectsBatchRule, ADR-27'nin sınıf kısıtının kurulum
// zamanında uygulandığını sınar.
//
// Kural 5'in akış fazına alınması precision'ı %100'den ~%50'ye düşürürdü
// (ilk-kayıt tuzağı). Bu yüzden kısıt yorumla değil kurulum hatasıyla ifade
// edilir.
func TestNewStreamEngine_RejectsBatchRule(t *testing.T) {
	_, err := NewStreamEngine(Config{Sink: newMemSink()},
		fakeStreamRule{id: integrityrule.Activity}) // ClassBatch
	if err == nil {
		t.Fatal("toplu kuralın akış fazına alınması reddedilmeliydi (ADR-27)")
	}
	if !strings.Contains(err.Error(), "ADR-27") {
		t.Errorf("hata mesajı kararı işaret etmeli: %v", err)
	}
}

// TestNewBatchEngine_RejectsStreamRule, ters yönü sınar.
func TestNewBatchEngine_RejectsStreamRule(t *testing.T) {
	_, err := NewBatchEngine(Config{Sink: newMemSink()},
		fakeBatchRule{id: integrityrule.TimeOrder}) // ClassStream
	if err == nil {
		t.Fatal("akış kuralının toplu faza alınması reddedilmeliydi (ADR-27)")
	}
}

// TestNewEngine_RejectsAggregateRule, kural 4'ün hiçbir faza alınamadığını
// sınar (ADR-30).
func TestNewEngine_RejectsAggregateRule(t *testing.T) {
	if _, err := NewStreamEngine(Config{Sink: newMemSink()},
		fakeStreamRule{id: integrityrule.Trajectory}); err == nil {
		t.Error("kural 4 akış fazına alınmamalı (ADR-30)")
	}
	if _, err := NewBatchEngine(Config{Sink: newMemSink()},
		fakeBatchRule{id: integrityrule.Trajectory}); err == nil {
		t.Error("kural 4 toplu faza alınmamalı (ADR-30)")
	}
}

// TestNewEngine_RequiresSink, yazıcısız motorun kurulmadığını sınar.
func TestNewEngine_RequiresSink(t *testing.T) {
	if _, err := NewStreamEngine(Config{}); err == nil {
		t.Error("yazıcısız motor kurulmamalı")
	}
}

// TestObserve_RejectedInBatchPhase, faz karışmasının yakalandığını sınar.
func TestObserve_RejectedInBatchPhase(t *testing.T) {
	e, err := NewBatchEngine(Config{Sink: newMemSink()})
	if err != nil {
		t.Fatalf("NewBatchEngine: %v", err)
	}
	if err := e.Observe(context.Background(), record(t, "a", 0)); err == nil {
		t.Error("toplu fazda Observe reddedilmeli")
	}

	s, err := NewStreamEngine(Config{Sink: newMemSink()})
	if err != nil {
		t.Fatalf("NewStreamEngine: %v", err)
	}
	if err := s.Evaluate(context.Background(), Sequence{Subscriber: "a"}); err == nil {
		t.Error("akış fazında Evaluate reddedilmeli")
	}
}

// ─── Öncelik ve tek-atıf (ADR-28) ─────────────────────────────────────────────

// TestResolve_SingleCanonicalPerEvent, ADR-28/2'nin ana değişmezini sınar.
//
// Kural 1 (öncelik 0) ve kural 3 (öncelik 2) aynı olayı yakaladığında kanonik
// bulgu kural 1'in olmalı; kural 3'ün isabeti bastırılmış yazılmalı ama
// **atılmamalı** (karışıklık matrisi onu kullanır).
func TestResolve_SingleCanonicalPerEvent(t *testing.T) {
	sink := newMemSink()
	rec := record(t, "abone-1", 0)
	hitSet := map[uuid.UUID]bool{rec.EventID: true}

	e, err := NewStreamEngine(Config{Sink: sink},
		fakeStreamRule{id: integrityrule.TimeOrder, hitOn: hitSet},
		fakeStreamRule{id: integrityrule.Inventory, hitOn: hitSet},
	)
	if err != nil {
		t.Fatalf("NewStreamEngine: %v", err)
	}

	if err := e.Observe(context.Background(), rec); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(sink.findings) != 2 {
		t.Fatalf("2 bulgu bekleniyordu (1 kanonik + 1 bastırılmış), %d yazıldı", len(sink.findings))
	}

	canonical, ok := sink.canonicalOf(rec.EventID)
	if !ok {
		t.Fatal("kanonik bulgu yok")
	}
	if canonical.Rule != integrityrule.Inventory {
		t.Errorf("kanonik kural %s, beklenen %s (ADR-28 öncelik)",
			canonical.Rule, integrityrule.Inventory)
	}

	var suppressedCount int
	for _, f := range sink.findings {
		if f.Canonical() {
			continue
		}
		suppressedCount++
		if *f.SuppressedBy != integrityrule.Inventory {
			t.Errorf("bastıran kural %s, beklenen %s", *f.SuppressedBy, integrityrule.Inventory)
		}
		if f.Rule != integrityrule.TimeOrder {
			t.Errorf("bastırılan kural %s, beklenen %s", f.Rule, integrityrule.TimeOrder)
		}
	}
	if suppressedCount != 1 {
		t.Errorf("%d bastırılmış bulgu, beklenen 1", suppressedCount)
	}

	st := e.Stats()
	if st.Canonical != 1 || st.Suppressed != 1 || st.Invalid != 0 {
		t.Errorf("sayaçlar beklenmedik: %+v", st)
	}
}

// TestResolve_RuleOrderDoesNotMatter, kuralların motora hangi sırada
// verildiğinin sonucu değiştirmediğini sınar.
//
// Motor kuralları öncelik sırasına diziyor; dizmeseydi kanonik bulgu kurulum
// sırasına bağlı olur ve determinizm (K10) bozulurdu.
func TestResolve_RuleOrderDoesNotMatter(t *testing.T) {
	for _, name := range []string{"envanter-önce", "zaman-önce"} {
		t.Run(name, func(t *testing.T) {
			sink := newMemSink()
			rec := record(t, "abone-1", 0)
			hitSet := map[uuid.UUID]bool{rec.EventID: true}

			inv := fakeStreamRule{id: integrityrule.Inventory, hitOn: hitSet}
			tim := fakeStreamRule{id: integrityrule.TimeOrder, hitOn: hitSet}

			var e *Engine
			var err error
			if name == "envanter-önce" {
				e, err = NewStreamEngine(Config{Sink: sink}, inv, tim)
			} else {
				e, err = NewStreamEngine(Config{Sink: sink}, tim, inv)
			}
			if err != nil {
				t.Fatalf("NewStreamEngine: %v", err)
			}

			if err := e.Observe(context.Background(), rec); err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if err := e.Flush(context.Background()); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			canonical, ok := sink.canonicalOf(rec.EventID)
			if !ok || canonical.Rule != integrityrule.Inventory {
				t.Errorf("kanonik kural %v, beklenen %s", canonical.Rule, integrityrule.Inventory)
			}
		})
	}
}

// TestResolve_FirstClaimWinsAcrossPhases, ADR-28/7'yi sınar.
//
// Kural 5 (öncelik 1) kural 3'ten (öncelik 2) güçlüdür ama sonraki fazda koşar.
// Akış fazının talebi durduğu için toplu fazın isabeti — güçlü olmasına
// rağmen — bastırılmış yazılır. Ve bu sessiz kalmaz: `demoted` sayacı artar.
func TestResolve_FirstClaimWinsAcrossPhases(t *testing.T) {
	claims := NewClaims()
	rec := record(t, "abone-1", 0)
	hitSet := map[uuid.UUID]bool{rec.EventID: true}

	// Faz 1: akış, kural 3 talep eder.
	streamSink := newMemSink()
	stream, err := NewStreamEngine(Config{Sink: streamSink, Claims: claims},
		fakeStreamRule{id: integrityrule.TimeOrder, hitOn: hitSet})
	if err != nil {
		t.Fatalf("NewStreamEngine: %v", err)
	}
	if err := stream.Observe(context.Background(), rec); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := stream.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if f, ok := streamSink.canonicalOf(rec.EventID); !ok || f.Rule != integrityrule.TimeOrder {
		t.Fatalf("akış fazı kanonik bulgusu beklenen kural değil: %v", f.Rule)
	}

	// Faz 2: toplu, kural 5 aynı olayı yakalar.
	batchSink := newMemSink()
	batch, err := NewBatchEngine(Config{Sink: batchSink, Claims: claims},
		fakeBatchRule{id: integrityrule.Activity, hitOn: hitSet})
	if err != nil {
		t.Fatalf("NewBatchEngine: %v", err)
	}
	if err := batch.Evaluate(context.Background(), Sequence{
		Subscriber: rec.Subscriber, Records: []Record{rec},
	}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := batch.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(batchSink.findings) != 1 {
		t.Fatalf("1 bulgu bekleniyordu, %d", len(batchSink.findings))
	}
	f := batchSink.findings[0]
	if f.Canonical() {
		t.Error("toplu fazın isabeti kanonik olmamalı: olay zaten talep edilmişti (ADR-28/7)")
	}
	if f.SuppressedBy == nil || *f.SuppressedBy != integrityrule.TimeOrder {
		t.Errorf("bastıran kural %v, beklenen %s", f.SuppressedBy, integrityrule.TimeOrder)
	}
	if f.DetectedIn != integrityrule.ClassBatch {
		t.Errorf("detected_in = %q, beklenen %q", f.DetectedIn, integrityrule.ClassBatch)
	}

	// Sessiz kalmamalı.
	if st := batch.Stats(); st.Demoted != 1 {
		t.Errorf("demoted = %d, beklenen 1 (ADR-28/7 gerekçe 3)", st.Demoted)
	}
}

// TestResolve_DistinctEventsDoNotInterfere, farklı olayların birbirinin
// talebini etkilemediğini sınar.
func TestResolve_DistinctEventsDoNotInterfere(t *testing.T) {
	sink := newMemSink()
	a := record(t, "abone-1", 0)
	b := record(t, "abone-1", time.Hour)

	e, err := NewStreamEngine(Config{Sink: sink},
		fakeStreamRule{id: integrityrule.Inventory, hitOn: map[uuid.UUID]bool{a.EventID: true}},
		fakeStreamRule{id: integrityrule.TimeOrder, hitOn: map[uuid.UUID]bool{b.EventID: true}},
	)
	if err != nil {
		t.Fatalf("NewStreamEngine: %v", err)
	}

	for _, rec := range []Record{a, b} {
		if err := e.Observe(context.Background(), rec); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if st := e.Stats(); st.Canonical != 2 || st.Suppressed != 0 {
		t.Errorf("iki ayrı olay iki kanonik bulgu vermeliydi: %+v", st)
	}
	if fa, _ := sink.canonicalOf(a.EventID); fa.Rule != integrityrule.Inventory {
		t.Errorf("a olayının kuralı %s", fa.Rule)
	}
	if fb, _ := sink.canonicalOf(b.EventID); fb.Rule != integrityrule.TimeOrder {
		t.Errorf("b olayının kuralı %s", fb.Rule)
	}
}

// ─── Geçerlilik denetimi (ADR-31) ─────────────────────────────────────────────

// TestNormalise_RejectsInvalidHits, geçersiz isabetlerin yazılmadığını sınar.
//
// Veritabanı bunları zaten reddederdi; ama reddedilen parti diğer bulguları da
// düşürürdü (zehirli satır). Motor onları kapıda tutar ve sayar.
func TestNormalise_RejectsInvalidHits(t *testing.T) {
	valid := NewEvidence(integrityrule.Inventory, DefaultMarginCap).Str("k", "v").MustBuild()

	tests := []struct {
		name string
		hit  Hit
	}{
		{"tanımsız kural", Hit{Rule: 9, EventID: uuid.New(), Time: baseTime, Scenario: "A", Margin: 1, Evidence: valid}},
		{"kural 4 (olay-çıpalı değil)", Hit{Rule: integrityrule.Trajectory, EventID: uuid.New(), Time: baseTime, Scenario: "A", Margin: 1, Evidence: valid}},
		{"boş event_id", Hit{Rule: integrityrule.Inventory, Time: baseTime, Scenario: "A", Margin: 1, Evidence: valid}},
		{"boş zaman", Hit{Rule: integrityrule.Inventory, EventID: uuid.New(), Scenario: "A", Margin: 1, Evidence: valid}},
		{"boş senaryo", Hit{Rule: integrityrule.Inventory, EventID: uuid.New(), Time: baseTime, Margin: 1, Evidence: valid}},
		{"boş kanıt", Hit{Rule: integrityrule.Inventory, EventID: uuid.New(), Time: baseTime, Scenario: "A", Margin: 1}},
		{"sürümsüz kanıt", Hit{Rule: integrityrule.Inventory, EventID: uuid.New(), Time: baseTime, Scenario: "A", Margin: 1, Evidence: Evidence{"k": "v"}}},
		{"hatalı kanıt", Hit{Rule: integrityrule.Inventory, EventID: uuid.New(), Time: baseTime, Scenario: "A", Margin: 1, Evidence: Evidence{"v": 1, "error": "bozuk"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := newMemSink()
			e, err := NewStreamEngine(Config{Sink: sink})
			if err != nil {
				t.Fatalf("NewStreamEngine: %v", err)
			}

			e.resolve(tc.hit.EventID, []Hit{tc.hit})
			if err := e.Flush(context.Background()); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			if len(sink.findings) != 0 {
				t.Errorf("geçersiz isabet yazıldı: %+v", sink.findings[0])
			}
			if st := e.Stats(); st.Invalid != 1 {
				t.Errorf("invalid = %d, beklenen 1", st.Invalid)
			}
		})
	}
}

// TestNormalise_ClampsMargin, margin'in şema kısıtına (>= 1.0, <= cap)
// çekildiğini sınar.
func TestNormalise_ClampsMargin(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"normal", 2.5, 2.5},
		{"sonsuz (Δt=0)", math.Inf(1), 1000},
		{"NaN", math.NaN(), 1000},
		{"eşiğin altında", 0.3, 1.0},
		{"tam sınırda", 1000, 1000},
		{"sınırın üstünde", 5000, 1000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := newMemSink()
			e, err := NewStreamEngine(Config{Sink: sink, MarginCap: 1000})
			if err != nil {
				t.Fatalf("NewStreamEngine: %v", err)
			}

			id := uuid.New()
			e.resolve(id, []Hit{{
				Rule: integrityrule.Inventory, EventID: id, Time: baseTime, Scenario: "A",
				Margin:   tc.in,
				Evidence: NewEvidence(integrityrule.Inventory, 1000).Str("k", "v").MustBuild(),
			}})
			if err := e.Flush(context.Background()); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			if len(sink.findings) != 1 {
				t.Fatalf("bulgu yazılmadı (invalid=%d)", e.Stats().Invalid)
			}
			if got := sink.findings[0].Margin; got != tc.want {
				t.Errorf("margin = %g, beklenen %g", got, tc.want)
			}
		})
	}
}

// ─── Tampon ve idempotanslık ──────────────────────────────────────────────────

// TestFlush_BufferedUntilThreshold, tamponun eşiğe kadar tutulduğunu sınar.
func TestFlush_BufferedUntilThreshold(t *testing.T) {
	sink := newMemSink()
	e, err := NewStreamEngine(Config{Sink: sink, BufferSize: 3})
	if err != nil {
		t.Fatalf("NewStreamEngine: %v", err)
	}

	for i := 0; i < 2; i++ {
		id := uuid.New()
		e.resolve(id, []Hit{{
			Rule: integrityrule.Inventory, EventID: id, Time: baseTime, Scenario: "A", Margin: 1,
			Evidence: NewEvidence(integrityrule.Inventory, DefaultMarginCap).Str("k", "v").MustBuild(),
		}})
	}
	if err := e.maybeFlush(context.Background()); err != nil {
		t.Fatalf("maybeFlush: %v", err)
	}
	if len(sink.findings) != 0 {
		t.Errorf("eşik altında yazılmamalı, %d yazıldı", len(sink.findings))
	}
	if e.Pending() != 2 {
		t.Errorf("tamponda %d bulgu, beklenen 2", e.Pending())
	}

	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(sink.findings) != 2 || e.Pending() != 0 {
		t.Errorf("Flush sonrası: yazılan %d, bekleyen %d", len(sink.findings), e.Pending())
	}
}

// TestFlush_ReprocessingIsIdempotent, aynı kayıtların iki kez işlenmesinin
// satır sayısını değiştirmediğini sınar (ADR-31/2).
//
// Kafka at-least-once semantiğinde tüketici çökerse parti yeniden işlenir.
// Yinelenen bulgu F.5'in precision denominatörünü şişirir ve ölçümü SESSİZCE
// düşürür.
func TestFlush_ReprocessingIsIdempotent(t *testing.T) {
	sink := newMemSink()
	records := []Record{record(t, "a", 0), record(t, "a", time.Hour)}
	hitSet := map[uuid.UUID]bool{records[0].EventID: true, records[1].EventID: true}

	process := func() {
		e, err := NewStreamEngine(Config{Sink: sink, Claims: NewClaims()},
			fakeStreamRule{id: integrityrule.Inventory, hitOn: hitSet})
		if err != nil {
			t.Fatalf("NewStreamEngine: %v", err)
		}
		for _, rec := range records {
			if err := e.Observe(context.Background(), rec); err != nil {
				t.Fatalf("Observe: %v", err)
			}
		}
		if err := e.Flush(context.Background()); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}

	process()
	first := len(sink.findings)
	process() // yeniden işleme
	if len(sink.findings) != first {
		t.Errorf("yeniden işleme satır sayısını değiştirdi: %d → %d", first, len(sink.findings))
	}
	if first != 2 {
		t.Errorf("2 bulgu bekleniyordu, %d", first)
	}
}

// ─── Dizi doğrulaması ─────────────────────────────────────────────────────────

// TestEvaluate_RejectsUnsortedSequence, toplu fazın sıralama değişmezini
// koruduğunu sınar.
//
// Kurallar sıranın artan olduğunu varsayıyor; bozulursa hız kuralı negatif Δt
// hesaplar ve bulgu üretmez — sessiz kayıp.
func TestEvaluate_RejectsUnsortedSequence(t *testing.T) {
	e, err := NewBatchEngine(Config{Sink: newMemSink()})
	if err != nil {
		t.Fatalf("NewBatchEngine: %v", err)
	}

	late := record(t, "a", time.Hour)
	early := record(t, "a", 0)

	err = e.Evaluate(context.Background(), Sequence{
		Subscriber: "a", Records: []Record{late, early},
	})
	if err == nil {
		t.Error("sırasız dizi reddedilmeliydi")
	}

	// Yabancı abone de reddedilmeli.
	other := record(t, "b", 0)
	err = e.Evaluate(context.Background(), Sequence{
		Subscriber: "a", Records: []Record{early, other},
	})
	if err == nil {
		t.Error("başka aboneye ait kayıt reddedilmeliydi")
	}
}

// ─── PBT değişmezleri ─────────────────────────────────────────────────────────

// TestPBT_AtMostOneCanonicalPerEvent, PBT #9: bir olay için en çok bir kanonik
// bulgu yazılır (ADR-28/2).
func TestPBT_AtMostOneCanonicalPerEvent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Rastgele bir kural alt kümesi aynı olayı yakalasın.
		streamRules := []integrityrule.ID{integrityrule.Inventory, integrityrule.TimeOrder}
		var chosen []integrityrule.ID
		for _, id := range streamRules {
			if rapid.Bool().Draw(t, "hit-"+id.Name()) {
				chosen = append(chosen, id)
			}
		}

		eventID := uuid.New()
		rec := Record{
			RunID: uuid.New(), EventID: eventID, Time: baseTime,
			Subscriber: "a", Scenario: "A",
		}
		hitSet := map[uuid.UUID]bool{eventID: true}

		sink := newMemSink()
		rules := make([]StreamRule, 0, len(chosen))
		for _, id := range chosen {
			rules = append(rules, fakeStreamRule{id: id, hitOn: hitSet})
		}
		e, err := NewStreamEngine(Config{Sink: sink}, rules...)
		if err != nil {
			t.Fatalf("NewStreamEngine: %v", err)
		}
		if err := e.Observe(context.Background(), rec); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if err := e.Flush(context.Background()); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		var canonical int
		for _, f := range sink.findings {
			if f.Canonical() {
				canonical++
			}
		}
		if canonical > 1 {
			t.Fatalf("%d kanonik bulgu; en çok 1 olmalı (ADR-28/2)", canonical)
		}
		if len(chosen) > 0 && canonical != 1 {
			t.Fatalf("isabet varken kanonik bulgu yok (%d isabet)", len(chosen))
		}
		if len(sink.findings) != len(chosen) {
			t.Fatalf("%d bulgu yazıldı, %d isabet vardı (bastırılanlar atılmamalı)",
				len(sink.findings), len(chosen))
		}
	})
}

// TestPBT_MarginIsFiniteAndAtLeastOne, PBT #10: yazılan her bulgunun margin'i
// sonlu, NaN değil ve ≥ 1,0.
func TestPBT_MarginIsFiniteAndAtLeastOne(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		margin := rapid.OneOf(
			rapid.Float64Range(-1e12, 1e12),
			rapid.Just(math.Inf(1)),
			rapid.Just(math.Inf(-1)),
			rapid.Just(math.NaN()),
			rapid.Just(0.0),
		).Draw(t, "margin")
		cap := rapid.Float64Range(1, 1e9).Draw(t, "cap")

		sink := newMemSink()
		e, err := NewStreamEngine(Config{Sink: sink, MarginCap: cap})
		if err != nil {
			t.Fatalf("NewStreamEngine: %v", err)
		}

		id := uuid.New()
		e.resolve(id, []Hit{{
			Rule: integrityrule.Inventory, EventID: id, Time: baseTime, Scenario: "A",
			Margin:   margin,
			Evidence: NewEvidence(integrityrule.Inventory, cap).Float("v_kmh", margin).MustBuild(),
		}})
		if err := e.Flush(context.Background()); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		if len(sink.findings) != 1 {
			t.Fatalf("bulgu yazılmadı (margin=%g cap=%g)", margin, cap)
		}
		got := sink.findings[0].Margin
		switch {
		case math.IsNaN(got):
			t.Fatalf("margin NaN (girdi %g)", margin)
		case math.IsInf(got, 0):
			t.Fatalf("margin sonsuz (girdi %g)", margin)
		case got < 1.0:
			t.Fatalf("margin %g < 1.0 (şema CHECK ihlali)", got)
		case got > cap:
			t.Fatalf("margin %g > cap %g (şema CHECK ihlali)", got, cap)
		}
	})
}

// TestPBT_FindingsAreSerialisable, PBT #11: yazılan her bulgunun kanıt gövdesi
// json.Marshal'dan hatasız geçer.
//
// Geçmezse bulgu veritabanına hiç gitmez ve kayıp SESSİZ olur.
func TestPBT_FindingsAreSerialisable(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := rapid.Float64().Draw(t, "f")
		s := rapid.String().Draw(t, "s")
		n := rapid.Int().Draw(t, "n")

		ev, err := NewEvidence(integrityrule.Velocity, DefaultMarginCap).
			Float("velocity_kmh", f).
			Str("attribution", s).
			Int("delta_s", n).
			Bool("zero_interval", n == 0).
			Build()
		if err != nil {
			t.Fatalf("kanıt kurulamadı: %v", err)
		}
		if _, err := json.Marshal(ev); err != nil {
			t.Fatalf("kanıt serileşemedi: %v", err)
		}
	})
}
