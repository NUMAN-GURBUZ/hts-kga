// T-E02-19 — Altın senaryo ve uçtan uca determinizm.
//
// # Neden bellek içi (İ-6)
//
// Altın senaryo config'den koşmaz, veritabanına ve Kafka'ya dokunmaz. İki
// somut engel vardı: `config.Validate()` `shadowing_sigma_db > 0` şartı koyar
// (altın senaryo gölgelemesizdir) ve `run.scenario` yalnızca A/B/C/D olabilir
// (DB CHECK kısıtı). Bu kısıtları gevşetmek üretim şemasını altın senaryo
// uğruna esnetmek olurdu; senaryo bu yüzden testte elle kurulur.
//
// # Zincir
//
//	tek site → best-server → olay → HTS kaydı (+TA) → yayın (tel biçimi)
//	        → çözümleme → analiz motoru → olasılık kütlesi
//
// Zincirin iki ucu bağımsız olarak doğrulanabilir: kaydın TA'sı `pkg/ta`'dan,
// analizin halkası da aynı paketten gelir; gerçek konum kütlenin taşındığı
// bölgede olmalıdır.
package golden

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/driver"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/agent"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// Altın senaryo: tek site, başlangıçta, ADR-17 kentsel profil parametreleri.
const (
	goldenLat        = 38.6748
	goldenLon        = 39.2225
	goldenAntHeight  = 25.0
	goldenTilt       = 6.0
	goldenEIRP       = 58.0
	goldenBeamWidth  = 65.0
	goldenFreqMHz    = 2100
	goldenRxSensDBm  = -110.0
	goldenSigmaDB    = 7.0
	goldenSalt       = "0123456789abcdef0123456789abcdef0123456789abcdef"
	goldenResolution = 100.0

	// shadowSafeRangeM, kapsamanın gölgelemeden bağımsız garanti olduğu
	// mesafedir. Bu mesafede gölgelemesiz marj ~16 dB, yani 2,3σ'dır;
	// σ=7 dB'lik bir çekilişin eşiği aşması pratikte imkânsızdır.
	shadowSafeRangeM = 2000.0
)

var (
	goldenRunID  = uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	goldenSiteID = uuid.MustParse("11111111-1111-4111-8111-111111111111")
)

// goldenCellID, verilen sektörün hücre kimliğidir.
func goldenCellID(sector int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("golden:cell:"+string(rune('0'+sector))))
}

// goldenRMax, tek sitenin link budget'tan çözülen kapsama yarıçapıdır.
func goldenRMax(t testing.TB) float64 {
	t.Helper()
	model, err := rf.ModelFor(config.ModelUMa)
	if err != nil {
		t.Fatalf("rf.ModelFor: %v", err)
	}
	r, err := rf.CoverageRangeM(model, rf.RangeSpec{
		EIRPdBm:          goldenEIRP,
		RxSensitivityDBm: goldenRxSensDBm,
		HBSm:             goldenAntHeight,
		HUTm:             rf.UTHeightM,
		FreqMHz:          goldenFreqMHz,
		BeamWidthDeg:     goldenBeamWidth,
		TiltDeg:          goldenTilt,
	})
	if err != nil {
		t.Fatalf("rf.CoverageRangeM: %v", err)
	}
	return r
}

// buildSelector, tek siteli radyo seçicisini kurar (simülatör tarafı).
func buildSelector(t testing.TB, rMax float64) *radio.Selector {
	t.Helper()

	model, err := rf.ModelFor(config.ModelUMa)
	if err != nil {
		t.Fatalf("rf.ModelFor: %v", err)
	}

	cells := make([]radio.Cell, 3)
	for i := range cells {
		cells[i] = radio.Cell{
			ID:           [16]byte(goldenCellID(i)),
			AzimuthDeg:   float64(i) * 120,
			BeamWidthDeg: goldenBeamWidth,
			TiltDeg:      goldenTilt,
			EIRPdBm:      goldenEIRP,
			FreqMHz:      goldenFreqMHz,
			RMaxM:        rMax,
		}
	}

	net, err := radio.NewNetwork([]radio.Site{{
		Source:     radio.Source{Key: 1, ENU: geo.Point{}, Model: model},
		AntHeightM: goldenAntHeight,
		Cells:      cells,
	}})
	if err != nil {
		t.Fatalf("radio.NewNetwork: %v", err)
	}
	field, err := radio.NewShadowingField(42, rf.UTHeightM)
	if err != nil {
		t.Fatalf("radio.NewShadowingField: %v", err)
	}
	sel, err := radio.NewSelector(net, field, goldenRxSensDBm, rf.UTHeightM)
	if err != nil {
		t.Fatalf("radio.NewSelector: %v", err)
	}
	return sel
}

// staticSource, analiz envanterini besleyen sahte kaynaktır.
type staticSource struct{ cells []redis.CellParams }

func (s staticSource) ScanCells(context.Context, uuid.UUID) ([]redis.CellParams, error) {
	return s.cells, nil
}

// buildInventory, analiz tarafının envanterini kurar (aynı tek site).
func buildInventory(t testing.TB, rMax float64) *params.Inventory {
	t.Helper()

	cells := make([]redis.CellParams, 3)
	for i := range cells {
		cells[i] = redis.CellParams{
			CellID:     goldenCellID(i),
			SiteID:     goldenSiteID,
			Azimuth:    float64(i) * 120,
			BeamWidth:  goldenBeamWidth,
			FreqMHz:    goldenFreqMHz,
			EIRPdBm:    goldenEIRP,
			AntHeightM: goldenAntHeight,
			TiltDeg:    goldenTilt,
			RMaxM:      rMax,
			Morphology: "urban",
			ModelType:  "UMa",
			Lat:        goldenLat,
			Lon:        goldenLon,
		}
	}

	inv, err := params.Load(context.Background(), staticSource{cells: cells}, goldenRunID, goldenLat, goldenLon)
	if err != nil {
		t.Fatalf("params.Load: %v", err)
	}
	return inv
}

// buildBuilder, simülatörün kayıt üreticisini kurar.
func buildBuilder(t testing.TB) *event.Builder {
	t.Helper()

	clock, err := agent.NewClock(5)
	if err != nil {
		t.Fatalf("agent.NewClock: %v", err)
	}
	projector, err := geo.NewProjector(goldenLat, goldenLon)
	if err != nil {
		t.Fatalf("geo.NewProjector: %v", err)
	}
	pseudo, err := event.NewPseudonymizer([]byte(goldenSalt))
	if err != nil {
		t.Fatalf("event.NewPseudonymizer: %v", err)
	}
	splitter, err := split.New(42, split.DefaultCalibrationRatio)
	if err != nil {
		t.Fatalf("split.New: %v", err)
	}

	b, err := event.NewBuilder(event.BuilderConfig{
		RunID:         goldenRunID,
		Scenario:      "A",
		RunStart:      time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		Clock:         clock,
		Projector:     projector,
		Pseudonymizer: pseudo,
		Splitter:      splitter,
		Technology:    ta.LTE,
		TAEnabled:     true,
	})
	if err != nil {
		t.Fatalf("event.NewBuilder: %v", err)
	}
	return b
}

// captureSink, yayınlanan mesajları toplayan sahte Kafka hedefidir.
type captureSink struct{ messages []kafka.Message }

func (s *captureSink) Publish(_ context.Context, messages ...kafka.Message) error {
	s.messages = append(s.messages, messages...)
	return nil
}

// TestGolden_SingleSiteChain, altın senaryonun uçtan uca zincirini koşar.
//
// Elle doğrulanabilir çapalar:
//   - r_max link budget'tan çözülür ve o mesafede alınan güç = duyarlılık
//   - hüzme ekseninde **yakın** mesafelerde kapsama tamdır
//     (planın "σ=0 → kapsama %100" çapası için aşağıdaki nota bakın)
//   - kaydın TA'sı `pkg/ta`'dan gelir ve halkası gerçek mesafeyi içerir
//   - analiz kütlesi Σ = 1 ve gerçek konumun hücresi kütlede yer alır
func TestGolden_SingleSiteChain(t *testing.T) {
	rMax := goldenRMax(t)
	t.Logf("altın senaryo r_max = %.1f m (link budget)", rMax)

	selector := buildSelector(t, rMax)
	builder := buildBuilder(t)
	inventory := buildInventory(t, rMax)

	grid, err := density.NewGrid(goldenResolution)
	if err != nil {
		t.Fatalf("density.NewGrid: %v", err)
	}
	engine, err := driver.NewEngine(driver.EngineConfig{
		Inventory: inventory,
		Grid:      grid,
		Options: core.DefaultOptions(density.Config{
			RxSensitivityDBm: goldenRxSensDBm,
			SigmaNominalDB:   goldenSigmaDB,
			Lambda:           1,
			UTHeightM:        rf.UTHeightM,
		}, 8),
		Technology: ta.LTE,
		TAEnabled:  true,
	})
	if err != nil {
		t.Fatalf("driver.NewEngine: %v", err)
	}

	publisher, err := event.NewPublisher(&captureSink{})
	if err != nil {
		t.Fatalf("event.NewPublisher: %v", err)
	}
	_ = publisher

	covered, total := 0, 0
	for _, d := range []float64{200, 500, 1000, 2000, 3000, 4000, 5000, 6000} {
		if d > rMax {
			continue
		}
		// Hüzme ekseni: sektör 0 kuzeye bakar.
		pos := geo.Point{X: 0, Y: d}
		serving := selector.Select(pos)
		total++
		if !serving.Covered {
			// r_max, gölgelemesiz **medyan** geçiş noktasıdır (T-E02-05).
			// Simülatör gerçekleşmiş gölgelemeyi (σ=7 dB) ve gerçekleşmiş LOS
			// durumunu kullandığından, r_max yakınında noktaların yaklaşık
			// yarısı eşiğin altına düşer — beklenen davranıştır.
			//
			// Planın altın senaryosu bunu σ=0 ile aşar; mevcut radyo API'si
			// σ'yı modelden aldığı için σ=0 kurulamıyor (bkz. kapı raporu).
			// Bu yüzden kapsama iddiası marjın birkaç σ üstünde olduğu yakın
			// mesafelerle sınırlanmıştır.
			t.Logf("d=%.0f m: gölgeleme eşiğin altına indirdi (rx=%.2f dBm, marj %.2f dB)",
				d, serving.RxDBm, serving.RxDBm-goldenRxSensDBm)
			if d <= shadowSafeRangeM {
				t.Errorf("d=%.0f m: %0.f m içinde kapsama garanti olmalıydı (rx=%.2f dBm)",
					d, shadowSafeRangeM, serving.RxDBm)
			}
			continue
		}
		covered++

		obs := agent.Observation{AgentID: 1, Tick: int(d), Pos: pos, Serving: serving}
		pair, err := builder.Build(obs, 0)
		if err != nil {
			t.Fatalf("builder.Build: %v", err)
		}

		// TA ikizi: kaydın TA'sının halkası gerçek mesafeyi içermeli.
		ring, err := ta.NewRing(*pair.Record.TAValue, ta.LTE)
		if err != nil {
			t.Fatalf("ta.NewRing: %v", err)
		}
		if !ring.Contains(serving.DistanceM) {
			t.Errorf("d=%.0f m: TA halkası [%.2f, %.2f) gerçek mesafeyi (%.2f) içermiyor",
				d, ring.InnerM, ring.OuterM, serving.DistanceM)
		}

		// Yayın → tel biçimi → çözümleme → analiz
		sink := &captureSink{}
		pub, err := event.NewPublisher(sink)
		if err != nil {
			t.Fatalf("event.NewPublisher: %v", err)
		}
		if err := pub.Publish(context.Background(), pair); err != nil {
			t.Fatalf("yayın: %v", err)
		}

		wire := recordMessage(t, sink.messages)
		decoded, err := htswire.DecodeRecord(wire.Value)
		if err != nil {
			t.Fatalf("çözümleme: %v", err)
		}
		if decoded.EventID != pair.Record.EventID {
			t.Fatalf("tel biçiminde event_id ayrıştı")
		}

		result, err := engine.Process(decoded)
		if err != nil {
			t.Fatalf("analiz: %v", err)
		}
		if sum := density.TotalMass(result.Mass); math.Abs(sum-1) > density.MassTolerance {
			t.Errorf("d=%.0f m: Σ mass = %.15f", d, sum)
		}
		if !result.TAUsed {
			t.Errorf("d=%.0f m: ta_used false — halka bölgeyle kesişmeliydi", d)
		}

		// Gerçek konumun hücresi kütlede olmalı: analiz gerçeği dışlamamalı.
		trueCell := gridCellAt(grid, pos)
		if result.Mass[trueCell] <= 0 {
			t.Errorf("d=%.0f m: gerçek konumun hücresi kütlede yok (%d hücre)", d, len(result.Mass))
		}
	}

	t.Logf("hüzme ekseni kapsaması: %d/%d nokta (r_max = %.0f m, gölgeleme açık)",
		covered, total, rMax)
	if covered == 0 {
		t.Fatal("hiçbir noktada kapsama yok")
	}
}

// TestGolden_DriverDeterminism, consumer yolu ile replay sürücüsünün aynı
// kütleyi ürettiğini sınar (İ-7, K10'un S3 karşılığı).
//
// Kafka'ya bağlanılmaz: consumer'ın kayıt başına yaptığı iş
// (tel biçimi → çözümleme → motor) burada birebir tekrarlanır. Karşılaştırılan
// şey iki sürücünün **aynı saf çekirdeği** çağırdığıdır.
func TestGolden_DriverDeterminism(t *testing.T) {
	rMax := goldenRMax(t)
	selector := buildSelector(t, rMax)
	builder := buildBuilder(t)
	inventory := buildInventory(t, rMax)

	grid, err := density.NewGrid(250) // testte kaba ızgara: değişmez çözünürlükten bağımsız
	if err != nil {
		t.Fatalf("density.NewGrid: %v", err)
	}
	engine, err := driver.NewEngine(driver.EngineConfig{
		Inventory: inventory,
		Grid:      grid,
		Options: core.DefaultOptions(density.Config{
			RxSensitivityDBm: goldenRxSensDBm,
			SigmaNominalDB:   goldenSigmaDB,
			Lambda:           1,
			UTHeightM:        rf.UTHeightM,
		}, 8),
		Technology: ta.LTE,
		TAEnabled:  true,
	})
	if err != nil {
		t.Fatalf("driver.NewEngine: %v", err)
	}

	// 100 olay üret ve yayınla.
	sink := &captureSink{}
	pub, err := event.NewPublisher(sink)
	if err != nil {
		t.Fatalf("event.NewPublisher: %v", err)
	}

	var pairs []event.Pair
	for i := 0; i < 100; i++ {
		angle := float64(i) * 3.6 * math.Pi / 180
		radius := 300 + float64(i%20)*180
		pos := geo.Point{X: radius * math.Sin(angle), Y: radius * math.Cos(angle)}

		serving := selector.Select(pos)
		if !serving.Covered {
			continue
		}
		pair, err := builder.Build(agent.Observation{AgentID: i, Tick: i * 3, Pos: pos, Serving: serving}, 0)
		if err != nil {
			t.Fatalf("builder.Build: %v", err)
		}
		pairs = append(pairs, pair)
	}
	if len(pairs) < 90 {
		t.Fatalf("yalnızca %d olay üretildi, ≥90 bekleniyordu", len(pairs))
	}
	if err := pub.Publish(context.Background(), pairs...); err != nil {
		t.Fatalf("yayın: %v", err)
	}

	// Consumer yolu: yayınlanan baytları çöz, motoru çağır.
	var records []htswire.Record
	consumerResults := make(map[string]map[string]float64, len(pairs))
	for _, msg := range sink.messages {
		if msg.Topic != kafka.TopicRecords {
			continue
		}
		rec, err := htswire.DecodeRecord(msg.Value)
		if err != nil {
			t.Fatalf("consumer yolu çözümleme: %v", err)
		}
		records = append(records, rec)

		res, err := engine.Process(rec)
		if err != nil {
			t.Fatalf("consumer yolu analiz: %v", err)
		}
		consumerResults[rec.EventID.String()] = flatten(res)
	}

	// Replay yolu: aynı kayıtlar, aynı motor.
	replay, err := driver.NewReplay(engine, records)
	if err != nil {
		t.Fatalf("driver.NewReplay: %v", err)
	}
	replayResults, err := replay.Collect(context.Background())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if len(consumerResults) != len(replayResults) {
		t.Fatalf("olay sayısı ayrıştı: consumer %d, replay %d",
			len(consumerResults), len(replayResults))
	}

	for eventID, consumerMass := range consumerResults {
		replayMass := flatten(replayResults[eventID])
		if len(consumerMass) != len(replayMass) {
			t.Fatalf("olay %s: hücre sayısı ayrıştı (%d ≠ %d)",
				eventID, len(consumerMass), len(replayMass))
		}
		for cell, m := range consumerMass {
			if replayMass[cell] != m {
				t.Fatalf("olay %s hücre %s: %.17g ≠ %.17g (bit düzeyinde eşit olmalı)",
					eventID, cell, replayMass[cell], m)
			}
		}
	}

	t.Logf("consumer ve replay sürücüleri %d olayda bit-identical kütle üretti", len(consumerResults))
}

// flatten, kütle haritasını karşılaştırılabilir biçime çevirir.
func flatten(res core.Result) map[string]float64 {
	out := make(map[string]float64, len(res.Mass))
	for a, m := range res.Mass {
		out[axialKey(a)] = m
	}
	return out
}

func axialKey(a geo.Axial) string {
	return string(rune(a.Q+1000)) + ":" + string(rune(a.R+1000))
}

// gridCellAt, verilen noktanın düştüğü ızgara hücresini döndürür.
func gridCellAt(g *density.Grid, p geo.Point) geo.Axial {
	hex, err := geo.NewHexGrid(g.ResolutionM())
	if err != nil {
		panic(err)
	}
	return hex.At(p)
}

// recordMessage, yayınlanan mesajlar arasından HTS kaydını bulur.
func recordMessage(t testing.TB, messages []kafka.Message) kafka.Message {
	t.Helper()
	for _, m := range messages {
		if m.Topic == kafka.TopicRecords {
			return m
		}
	}
	t.Fatal("hts.records mesajı bulunamadı")
	return kafka.Message{}
}
