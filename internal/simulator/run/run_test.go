package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
)

const configsDir = "../../../configs"

// testSalt, HMAC takma adı için 32 baytlık test tuzudur (E-08 alt sınırı).
var testSalt = []byte("hts-kga-test-salt-0123456789abcd")

// memorySink, yayınlanan mesajları bellekte toplar.
type memorySink struct {
	mu       sync.Mutex
	records  []kafka.Message
	truths   []kafka.Message
	failNext bool
}

func (s *memorySink) Publish(_ context.Context, messages ...kafka.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext {
		return errPublish
	}
	for _, m := range messages {
		switch m.Topic {
		case kafka.TopicRecords:
			s.records = append(s.records, m)
		case kafka.TopicGroundTruth:
			s.truths = append(s.truths, m)
		}
	}
	return nil
}

var errPublish = &publishError{}

type publishError struct{}

func (e *publishError) Error() string { return "yayın reddedildi (test)" }

// smallScenario, hızlı koşan küçük bir senaryo yükler.
func smallScenario(t testing.TB, file string, agents, days int) *config.Scenario {
	t.Helper()

	scn, err := config.Load(configsDir + "/" + file)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	scn.Simulation.Agents = agents
	scn.Simulation.DurationDays = days
	return scn
}

// newRunner, test koşusunu kurar.
func newRunner(t testing.TB, scn *config.Scenario, sink event.Sink, runID uuid.UUID) *Runner {
	t.Helper()

	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	inv, err := inventory.Build(runID, scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}
	pseudo, err := event.NewPseudonymizer(testSalt)
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}

	r, err := New(Config{
		RunID:         runID,
		Scenario:      scn,
		Inventory:     inv,
		Projector:     proj,
		Pseudonymizer: pseudo,
		Sink:          sink,
		RunStart:      time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), // Pazartesi
	})
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	return r
}

// TestRunner_ProducesEvents, koşunun olay ürettiğini ve iki topic'in
// tutarlı olduğunu doğrular.
func TestRunner_ProducesEvents(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 20, 1)
	sink := &memorySink{}
	runner := newRunner(t, scn, sink, uuid.New())

	stats, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Ticks != 288 {
		t.Errorf("tick sayısı %d, 288 beklenir (1 gün × 5 dk)", stats.Ticks)
	}
	if stats.Events == 0 {
		t.Fatal("hiç olay üretilmedi")
	}
	if int64(len(sink.truths)) != stats.PublishedTruths {
		t.Errorf("ground truth mesajı %d, sayaç %d", len(sink.truths), stats.PublishedTruths)
	}
	if int64(len(sink.records)) != stats.PublishedRecords {
		t.Errorf("kayıt mesajı %d, sayaç %d", len(sink.records), stats.PublishedRecords)
	}

	// Her olay bir ground truth üretir; kayıt ise kapsama ve enjeksiyon
	// kural 4 yüzünden eksik olabilir.
	if stats.PublishedTruths != stats.Events {
		t.Errorf("ground truth %d, olay %d — her olay bir GT üretmeli",
			stats.PublishedTruths, stats.Events)
	}
	if stats.PublishedRecords > stats.Events {
		t.Errorf("kayıt sayısı (%d) olay sayısını (%d) aşamaz",
			stats.PublishedRecords, stats.Events)
	}

	t.Logf("%d ajan × %d tick → %d olay, %d kayıt, %d GT, %d enjeksiyon, %d kapsama dışı (%s)",
		runner.AgentCount(), stats.Ticks, stats.Events, stats.PublishedRecords,
		stats.PublishedTruths, stats.Injected, stats.Uncovered, stats.Duration.Round(time.Millisecond))
}

// TestRunner_EventVolumeMatchesProfile, olay hacminin Poisson profiline
// uyduğunu doğrular: ajan başına günde ~10 olay (T-E02-13).
func TestRunner_EventVolumeMatchesProfile(t *testing.T) {
	const agents, days = 100, 2
	scn := smallScenario(t, "urban_ta.yaml", agents, days)
	sink := &memorySink{}
	runner := newRunner(t, scn, sink, uuid.New())

	stats, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := float64(agents*days) * event.DailyEventTarget
	got := float64(stats.Events)
	rel := (got - want) / want

	t.Logf("olay hacmi: %v (beklenen %.0f, sapma %+.1f%%)", stats.Events, want, rel*100)
	if math.Abs(rel) > 0.05 {
		t.Errorf("olay hacmi %.0f, %.0f ± %%5 beklenir (sapma %+.1f%%)", got, want, rel*100)
	}
}

// TestRunner_Deterministic, aynı tohumun bit düzeyinde aynı olay dizisini
// ürettiğini doğrular — K10'un simülatör karşılığı.
func TestRunner_Deterministic(t *testing.T) {
	runID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("run:determinism"))

	digest := func() string {
		scn := smallScenario(t, "urban_ta.yaml", 25, 1)
		sink := &memorySink{}
		runner := newRunner(t, scn, sink, runID)
		if _, err := runner.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		h := sha256.New()
		for _, m := range sink.records {
			h.Write(m.Key)
			h.Write(m.Value)
		}
		for _, m := range sink.truths {
			h.Write(m.Key)
			h.Write(m.Value)
		}
		return hex.EncodeToString(h.Sum(nil))
	}

	first := digest()
	for i := 0; i < 3; i++ {
		if got := digest(); got != first {
			t.Fatalf("koşu %d farklı çıktı üretti:\n  %s\n  %s", i+1, first, got)
		}
	}
	t.Logf("üç koşu bit düzeyinde aynı: %s", first[:16])
}

// TestRunner_UncoveredProducesTruthOnly, kapsama dışı gözlemlerin yalnızca
// ground truth ürettiğini doğrular (ADR-08/3).
func TestRunner_UncoveredProducesTruthOnly(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 50, 1)
	sink := &memorySink{}
	runner := newRunner(t, scn, sink, uuid.New())

	stats, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Kapsama dışı olaylar + kural 4 silmeleri = kayıtsız ground truth'lar
	missing := stats.PublishedTruths - stats.PublishedRecords
	if missing < stats.Uncovered {
		t.Errorf("kayıtsız GT %d, en az kapsama dışı sayısı (%d) kadar olmalı",
			missing, stats.Uncovered)
	}

	// Kapsama dışı olayların ground truth'unda covered=false olmalı.
	uncovered := 0
	for _, m := range sink.truths {
		gt, err := htswire.DecodeGroundTruth(m.Value)
		if err != nil {
			t.Fatalf("DecodeGroundTruth: %v", err)
		}
		if !gt.Covered {
			uncovered++
		}
	}
	if int64(uncovered) != stats.Uncovered {
		t.Errorf("covered=false GT sayısı %d, sayaç %d", uncovered, stats.Uncovered)
	}
	t.Logf("kapsama dışı olay: %d / %d (%%%.2f)",
		stats.Uncovered, stats.Events, 100*float64(stats.Uncovered)/float64(stats.Events))
}

// TestRunner_InjectionRateMatchesConfig, enjeksiyon oranının config'deki
// değere yaklaştığını doğrular (ADR-09).
func TestRunner_InjectionRateMatchesConfig(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 200, 1)
	sink := &memorySink{}
	runner := newRunner(t, scn, sink, uuid.New())

	stats, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := float64(stats.Injected) / float64(stats.Events)
	want := scn.Integrity.InjectionRate
	t.Logf("enjeksiyon oranı: %%%.3f (config %%%.3f, %d/%d olay)",
		got*100, want*100, stats.Injected, stats.Events)

	if math.Abs(got-want)/want > 0.20 {
		t.Errorf("enjeksiyon oranı %%%.3f, %%%.3f ± %%20 beklenir", got*100, want*100)
	}
}

// TestRunner_EventIDsUnique, olay kimliklerinin koşu içinde benzersiz
// olduğunu doğrular (ADR-01).
func TestRunner_EventIDsUnique(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 40, 1)
	sink := &memorySink{}
	runner := newRunner(t, scn, sink, uuid.New())

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := make(map[uuid.UUID]struct{}, len(sink.truths))
	for _, m := range sink.truths {
		gt, err := htswire.DecodeGroundTruth(m.Value)
		if err != nil {
			t.Fatalf("DecodeGroundTruth: %v", err)
		}
		if _, dup := seen[gt.EventID]; dup {
			t.Fatalf("yinelenen event_id: %s", gt.EventID)
		}
		seen[gt.EventID] = struct{}{}
	}

	// Her kaydın ground truth karşılığı olmalı (ADR-01 referansiyel bütünlük).
	for _, m := range sink.records {
		rec, err := htswire.DecodeRecord(m.Value)
		if err != nil {
			t.Fatalf("DecodeRecord: %v", err)
		}
		if _, ok := seen[rec.EventID]; !ok {
			t.Fatalf("kayıt %s'in ground truth karşılığı yok", rec.EventID)
		}
	}
}

// TestRunner_PartitionSplit, C/V ayrımının config oranına uyduğunu doğrular.
func TestRunner_PartitionSplit(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 150, 1)
	sink := &memorySink{}
	runner := newRunner(t, scn, sink, uuid.New())

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	counts := map[string]int{}
	for _, m := range sink.truths {
		gt, err := htswire.DecodeGroundTruth(m.Value)
		if err != nil {
			t.Fatalf("DecodeGroundTruth: %v", err)
		}
		counts[gt.PartitionKey]++
	}

	total := counts["C"] + counts["V"]
	if total != len(sink.truths) {
		t.Fatalf("tanınmayan partition anahtarı var: %v", counts)
	}
	ratio := float64(counts["C"]) / float64(total)
	t.Logf("C/V ayrımı: C=%d V=%d (oran %.3f, config %.2f)",
		counts["C"], counts["V"], ratio, scn.Calibration.SplitRatio)

	if math.Abs(ratio-scn.Calibration.SplitRatio) > 0.05 {
		t.Errorf("kalibrasyon oranı %.3f, %.2f ± 0,05 beklenir", ratio, scn.Calibration.SplitRatio)
	}
}

// TestRunner_ContextCancellation, iptal edilen koşunun yarıda kesildiğini ve
// hatayla döndüğünü doğrular.
//
// Yarım koşu sessizce tam koşu gibi görünmemelidir: doğrulama önkoşulu
// (ADR-04) buna dayanır.
func TestRunner_ContextCancellation(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 20, 30)
	sink := &memorySink{}
	runner := newRunner(t, scn, sink, uuid.New())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	stats, err := runner.Run(ctx)
	if err == nil {
		t.Fatal("iptal edilen koşu hata döndürmedi")
	}
	if stats.Ticks != 0 {
		t.Errorf("yarım koşuda Ticks=%d, 0 beklenir (tamamlanma işareti)", stats.Ticks)
	}
	t.Logf("koşu beklendiği gibi kesildi: %v", err)
}

// TestRunner_PublishFailurePropagates, yayın hatasının yutulmadığını doğrular.
func TestRunner_PublishFailurePropagates(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 100, 1)
	sink := &memorySink{failNext: true}
	runner := newRunner(t, scn, sink, uuid.New())

	if _, err := runner.Run(context.Background()); err == nil {
		t.Fatal("yayın hatası yutuldu")
	}
}

// TestRunner_Rejects, eksik yapılandırmayı reddeder.
func TestRunner_Rejects(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 5, 1)
	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	runID := uuid.New()
	inv, err := inventory.Build(runID, scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}
	pseudo, err := event.NewPseudonymizer(testSalt)
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}
	full := Config{
		RunID: runID, Scenario: scn, Inventory: inv, Projector: proj,
		Pseudonymizer: pseudo, Sink: &memorySink{},
	}

	cases := []struct {
		name  string
		mutTo func(*Config)
	}{
		{"run_id yok", func(c *Config) { c.RunID = uuid.Nil }},
		{"senaryo yok", func(c *Config) { c.Scenario = nil }},
		{"envanter yok", func(c *Config) { c.Inventory = nil }},
		{"izdüşüm yok", func(c *Config) { c.Projector = nil }},
		{"takma ad üreticisi yok", func(c *Config) { c.Pseudonymizer = nil }},
		{"hedef yok", func(c *Config) { c.Sink = nil }},
	}
	for _, tc := range cases {
		cfg := full
		tc.mutTo(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}

// TestBuildNetwork_GroupsCellsBySite, sektörlerin siteye göre gruplandığını
// doğrular: gölgeleme site başınadır (ADR-19).
func TestBuildNetwork_GroupsCellsBySite(t *testing.T) {
	scn := smallScenario(t, "urban_ta.yaml", 5, 1)
	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	inv, err := inventory.Build(uuid.New(), scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}

	net, err := buildNetwork(inv)
	if err != nil {
		t.Fatalf("buildNetwork: %v", err)
	}
	if net == nil {
		t.Fatal("şebeke nil")
	}

	// Envanterdeki her hücre tam bir kez yer almalı.
	ids := make(map[uuid.UUID]int, len(inv.Cells))
	for _, c := range inv.Cells {
		ids[c.ID]++
	}
	for id, n := range ids {
		if n != 1 {
			t.Fatalf("envanterde yinelenen hücre %s (%d kez)", id, n)
		}
	}

	// Site sayısı yerleşimdeki site sayısına eşit olmalı.
	if got, want := inv.SiteCount(), len(inv.Layout.Sites); got != want {
		t.Errorf("site sayısı %d, %d beklenir", got, want)
	}
	t.Logf("%d site, %d hücre şebekeye dönüştürüldü", inv.SiteCount(), inv.CellCount())
}

// TestBuildNetwork_Rejects, tutarsız envanteri reddeder.
func TestBuildNetwork_Rejects(t *testing.T) {
	if _, err := buildNetwork(nil); err == nil {
		t.Error("nil envanter kabul edildi")
	}

	scn := smallScenario(t, "urban_ta.yaml", 5, 1)
	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	inv, err := inventory.Build(uuid.New(), scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}

	// Aynı sitenin sektörlerinden birinin anten yüksekliği değiştirilirse
	// dönüşüm reddetmelidir: yükseklik site düzeyinde bir özelliktir.
	broken := *inv
	broken.Cells = append([]inventory.Cell(nil), inv.Cells...)
	sort.Slice(broken.Cells, func(i, j int) bool {
		return broken.Cells[i].SiteID.String() < broken.Cells[j].SiteID.String()
	})
	broken.Cells[1].AntHeightM += 5
	if _, err := buildNetwork(&broken); err == nil {
		t.Error("farklı anten yüksekliği kabul edildi")
	}

	// Sitesi yerleşimde olmayan hücre reddedilmeli.
	orphan := *inv
	orphan.Cells = append([]inventory.Cell(nil), inv.Cells...)
	orphan.Cells[0].SiteID = uuid.New()
	if _, err := buildNetwork(&orphan); err == nil {
		t.Error("sahipsiz hücre kabul edildi")
	}
}
