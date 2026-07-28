package event

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/agent"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

const (
	testSalt      = "0123456789abcdef0123456789abcdef0123456789abcdef" // 48 bayt
	testOriginLat = 38.6748
	testOriginLon = 39.2225
)

// newTestBuilder, TA'lı kentsel senaryonun kayıt üreticisini kurar.
func newTestBuilder(t *testing.T, taEnabled bool) *Builder {
	t.Helper()

	clock, err := agent.NewClock(5)
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	projector, err := geo.NewProjector(testOriginLat, testOriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	pseudo, err := NewPseudonymizer([]byte(testSalt))
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}
	splitter, err := split.New(42, split.DefaultCalibrationRatio)
	if err != nil {
		t.Fatalf("split.New: %v", err)
	}

	b, err := NewBuilder(BuilderConfig{
		RunID:         uuid.MustParse(testRunID),
		Scenario:      "A",
		RunStart:      time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), // Pazartesi
		Clock:         clock,
		Projector:     projector,
		Pseudonymizer: pseudo,
		Splitter:      splitter,
		Technology:    ta.LTE,
		TAEnabled:     taEnabled,
	})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	return b
}

// observation, verilen mesafede kapsanan bir gözlem üretir.
func observation(agentID, tick int, distanceM float64, covered bool) agent.Observation {
	return agent.Observation{
		AgentID: agentID,
		Tick:    tick,
		Pos:     geo.Point{X: distanceM, Y: 0},
		Serving: radio.Serving{
			CellID:    [16]byte{1, 2, 3},
			DistanceM: distanceM,
			Covered:   covered,
		},
	}
}

// TestBuild_TAComesFromPkgTA, TA'nın `pkg/ta`'dan geldiğini sınar.
//
// Simülatörün TA'sı ile analizin halkası ikizdir; bu test iki tarafın aynı
// tanımı kullandığını doğrular: üretilen her TA için halka gerçek mesafeyi
// **içermelidir**.
func TestBuild_TAComesFromPkgTA(t *testing.T) {
	b := newTestBuilder(t, true)

	for _, d := range []float64{0, 39, 78.12, 100, 1000, 1484.28, 4999, 6125} {
		pair, err := b.Build(observation(7, 12, d, true), 0)
		if err != nil {
			t.Fatalf("Build(d=%g): %v", d, err)
		}
		if pair.Record.TAValue == nil {
			t.Fatalf("d=%g: TA açıkken ta_value nil", d)
		}

		want, err := ta.FromDistance(d, ta.LTE)
		if err != nil {
			t.Fatalf("ta.FromDistance: %v", err)
		}
		if *pair.Record.TAValue != want {
			t.Errorf("d=%g: ta_value = %d, pkg/ta %d diyor", d, *pair.Record.TAValue, want)
		}

		ring, err := ta.NewRing(*pair.Record.TAValue, ta.LTE)
		if err != nil {
			t.Fatalf("ta.NewRing: %v", err)
		}
		if !ring.Contains(d) {
			t.Errorf("d=%g: halka [%g, %g) mesafeyi içermiyor", d, ring.InnerM, ring.OuterM)
		}
	}
}

// TestBuild_TADisabled, TA'sız senaryolarda (B, D) ta_value'nun NULL kaldığını
// sınar.
func TestBuild_TADisabled(t *testing.T) {
	b := newTestBuilder(t, false)
	pair, err := b.Build(observation(1, 1, 1500, true), 0)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pair.Record.TAValue != nil {
		t.Errorf("TA kapalıyken ta_value = %d, nil beklenir", *pair.Record.TAValue)
	}
}

// TestBuild_UncoveredProducesNoRecord, ADR-08/3'ü sınar: kapsama dışında olay
// üretilmez, yalnızca ground truth yazılır.
func TestBuild_UncoveredProducesNoRecord(t *testing.T) {
	b := newTestBuilder(t, true)
	pair, err := b.Build(observation(3, 9, 50000, false), 0)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pair.Record != nil {
		t.Error("kapsama dışında HTS kaydı üretildi")
	}
	if pair.Truth.Covered {
		t.Error("ground truth covered=true")
	}
	if pair.Truth.EventID == uuid.Nil {
		t.Error("ground truth event_id boş")
	}
}

// TestBuild_PairSharesEventID, kayıt çiftinin ADR-01 bağını sınar.
func TestBuild_PairSharesEventID(t *testing.T) {
	b := newTestBuilder(t, true)
	pair, err := b.Build(observation(5, 20, 800, true), 1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pair.Record.EventID != pair.Truth.EventID {
		t.Fatalf("event_id ayrışmış: %s ≠ %s", pair.Record.EventID, pair.Truth.EventID)
	}

	want, err := NewID(Key{RunID: uuid.MustParse(testRunID), AgentID: 5, Tick: 20, Seq: 1})
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if pair.Record.EventID != want {
		t.Errorf("event_id = %s, T-E02-14 %s diyor", pair.Record.EventID, want)
	}
}

// TestBuild_NoLocationInRecord, HTS kaydının konum taşımadığını sınar.
//
// Çalışmanın temel varsayımı budur: kayıt gerçek konumu bilmez.
func TestBuild_NoLocationInRecord(t *testing.T) {
	b := newTestBuilder(t, true)
	pair, err := b.Build(observation(2, 4, 2500, true), 0)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Ground truth konumu taşır...
	if pair.Truth.TrueLocation.Lat == 0 && pair.Truth.TrueLocation.Lon == 0 {
		t.Error("ground truth konumu boş")
	}
	// ...kayıt taşımaz. Yansıma yerine tel biçimi denetlenir: sızıntı
	// olacaksa oradan olur.
	wire, err := EncodeRecordForTest(*pair.Record)
	if err != nil {
		t.Fatalf("serileştirme: %v", err)
	}
	for _, forbidden := range []string{"lat", "lon", "location", "true_location", "agent_id"} {
		if strings.Contains(strings.ToLower(string(wire)), forbidden) {
			t.Errorf("HTS kaydının tel biçiminde %q alanı var: %s", forbidden, wire)
		}
	}
}

// TestBuild_PartitionKeyFromPkgSplit, bölüm anahtarının `pkg/split`'ten
// geldiğini sınar (İ-5 ikizi).
func TestBuild_PartitionKeyFromPkgSplit(t *testing.T) {
	b := newTestBuilder(t, true)
	splitter, err := split.New(42, split.DefaultCalibrationRatio)
	if err != nil {
		t.Fatalf("split.New: %v", err)
	}

	calib, valid := 0, 0
	for i := 0; i < 2000; i++ {
		pair, err := b.Build(observation(i%50, i, 900, true), 0)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if got, want := pair.Truth.PartitionKey, splitter.Of(pair.Truth.EventID); got != want {
			t.Fatalf("olay %s: bölüm %s, pkg/split %s diyor", pair.Truth.EventID, got, want)
		}
		switch pair.Truth.PartitionKey {
		case split.Calibration:
			calib++
		case split.Validation:
			valid++
		default:
			t.Fatalf("tanımsız bölüm: %v", pair.Truth.PartitionKey)
		}
	}

	ratio := float64(calib) / float64(calib+valid)
	t.Logf("bölüm dağılımı: C=%d V=%d (oran %.4f)", calib, valid, ratio)
	if ratio < 0.77 || ratio > 0.83 {
		t.Errorf("kalibrasyon oranı %.4f, ~0.80 beklenir", ratio)
	}
}

// TestBuild_Deterministic, aynı gözlemin aynı kayıt çiftini verdiğini sınar.
func TestBuild_Deterministic(t *testing.T) {
	b1, b2 := newTestBuilder(t, true), newTestBuilder(t, true)
	obs := observation(9, 33, 1234, true)

	first, err := b1.Build(obs, 2)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Karşılaştırma tel biçimi üzerinden yapılır: HTSRecord işaretçi alan
	// (ta_value) taşıdığı için yapı eşitliği işaretçi adresini karşılaştırır,
	// değeri değil. Zaten yayınlanan da tel biçimidir.
	firstWire, err := EncodeRecordForTest(*first.Record)
	if err != nil {
		t.Fatalf("serileştirme: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := b2.Build(obs, 2)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		gotWire, err := EncodeRecordForTest(*got.Record)
		if err != nil {
			t.Fatalf("serileştirme: %v", err)
		}
		if string(gotWire) != string(firstWire) {
			t.Fatalf("%d. çağrı farklı kayıt verdi:\n %s\n %s", i, gotWire, firstWire)
		}
		if got.Truth.EventID != first.Truth.EventID ||
			got.Truth.PartitionKey != first.Truth.PartitionKey ||
			got.Truth.TrueLocation != first.Truth.TrueLocation ||
			got.Truth.Covered != first.Truth.Covered {
			t.Fatalf("%d. çağrı farklı ground truth verdi", i)
		}
	}
}

// TestBuilder_Validation, kurulum denetimlerini kapsar.
func TestBuilder_Validation(t *testing.T) {
	clock, _ := agent.NewClock(5)
	projector, _ := geo.NewProjector(testOriginLat, testOriginLon)
	pseudo, _ := NewPseudonymizer([]byte(testSalt))
	splitter, _ := split.New(42, 0.8)
	ok := BuilderConfig{
		RunID: uuid.MustParse(testRunID), Scenario: "A", RunStart: time.Now(), Clock: clock,
		Projector: projector, Pseudonymizer: pseudo, Splitter: splitter,
		Technology: ta.LTE, TAEnabled: true,
	}

	bad := map[string]func(*BuilderConfig){
		"run_id boş":            func(c *BuilderConfig) { c.RunID = uuid.Nil },
		"senaryo çok harfli":    func(c *BuilderConfig) { c.Scenario = "AB" },
		"projeksiyon yok":       func(c *BuilderConfig) { c.Projector = nil },
		"takma ad yok":          func(c *BuilderConfig) { c.Pseudonymizer = nil },
		"başlangıç zamanı yok":  func(c *BuilderConfig) { c.RunStart = time.Time{} },
		"TA açık teknoloji yok": func(c *BuilderConfig) { c.Technology = ta.Unknown },
	}
	for name, mutate := range bad {
		cfg := ok
		mutate(&cfg)
		if _, err := NewBuilder(cfg); err == nil {
			t.Errorf("%s: hata bekleniyordu", name)
		}
	}
}

// TestPseudonym_Deterministic, takma adların kararlı olduğunu sınar.
func TestPseudonym_Deterministic(t *testing.T) {
	p1, err := NewPseudonymizer([]byte(testSalt))
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}
	p2, _ := NewPseudonymizer([]byte(testSalt))
	other, _ := NewPseudonymizer([]byte(strings.Repeat("z", 48)))

	for agentID := 0; agentID < 100; agentID++ {
		a, b := p1.PseudoMSISDN(agentID), p2.PseudoMSISDN(agentID)
		if a != b {
			t.Fatalf("ajan %d: aynı tuz farklı takma ad verdi", agentID)
		}
		if len(a) != 64 {
			t.Fatalf("takma ad uzunluğu %d, şema VARCHAR(64) bekliyor", len(a))
		}
		if strings.Contains(a, MSISDN(agentID)) {
			t.Fatalf("takma ad ham numarayı içeriyor: %s", a)
		}
		if other.PseudoMSISDN(agentID) == a {
			t.Fatalf("farklı tuz aynı takma adı verdi (ajan %d)", agentID)
		}
		if p1.PseudoIMEI(agentID) == a {
			t.Fatalf("ajan %d: MSISDN ve IMEI takma adları aynı", agentID)
		}
	}

	// Kısa tuz reddedilir.
	if _, err := NewPseudonymizer([]byte("kısa")); err == nil {
		t.Error("kısa tuz kabul edildi")
	}
}

// TestMSISDNFormat, üretilen sahte numaranın biçimini sınar.
func TestMSISDNFormat(t *testing.T) {
	got := MSISDN(42)
	if len(got) != 12 || !strings.HasPrefix(got, "9050") {
		t.Errorf("MSISDN(42) = %q, 9050 önekli 12 hane beklenir", got)
	}
	if MSISDN(1) == MSISDN(2) {
		t.Error("farklı ajanlar aynı numarayı aldı")
	}
	if len(IMEI(42)) != 15 {
		t.Errorf("IMEI(42) = %q, 15 hane beklenir", IMEI(42))
	}
}
