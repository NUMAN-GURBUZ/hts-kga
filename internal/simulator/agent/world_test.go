package agent

import (
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
)

// buildWorld, gerçek senaryo envanteriyle bir dünya kurar.
func buildWorld(t *testing.T, file string, agentCount, workers int) (*World, *config.Scenario) {
	t.Helper()

	scn := buildScenario(t, file)
	agents, sel := buildAgents(t, scn, agentCount)

	clock, err := NewClock(scn.Simulation.TickMinutes)
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	mob := NewMobility(scn.Run.Seed, clock)

	w, err := NewWorld(agents, mob, sel, workers)
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	t.Cleanup(w.Close)
	return w, scn
}

// TestNewWorld_Validation, eksik bağımlılıkların reddedildiğini sınar.
func TestNewWorld_Validation(t *testing.T) {
	scn := buildScenario(t, "urban_ta.yaml")
	agents, sel := buildAgents(t, scn, 5)
	clock, _ := NewClock(5)
	mob := NewMobility(42, clock)

	if _, err := NewWorld(agents, nil, sel, 1); err == nil {
		t.Error("nil hareket modeli için hata bekleniyordu")
	}
	if _, err := NewWorld(agents, mob, nil, 1); err == nil {
		t.Error("nil seçici için hata bekleniyordu")
	}
	if _, err := NewWorld(nil, mob, sel, 1); err == nil {
		t.Error("boş ajan listesi için hata bekleniyordu")
	}
}

// TestWorld_TickPipeline, sprint hedefinin uçtan uca çalıştığını sınar:
// konum → yol kaybı → gölgeleme → anten → best-server → kapsama.
func TestWorld_TickPipeline(t *testing.T) {
	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			w, scn := buildWorld(t, file, 100, 4)
			clock, _ := NewClock(scn.Simulation.TickMinutes)

			// Bir tam gün
			for tick := 0; tick < clock.TicksPerDay(); tick++ {
				w.Tick(tick)

				for _, obs := range w.Observations() {
					if obs.Tick != tick {
						t.Fatalf("gözlem tick'i yanlış: %d, beklenen %d", obs.Tick, tick)
					}
					if !obs.Phase.Valid() {
						t.Fatalf("ajan %d: geçersiz evre", obs.AgentID)
					}
					// Kapsama kararı eşikle tutarlı olmalı
					if obs.Serving.Covered && obs.Serving.RxDBm < scn.Network.RxSensitivityDBm {
						t.Fatalf("ajan %d: covered=true ama rx=%.2f < %.2f",
							obs.AgentID, obs.Serving.RxDBm, scn.Network.RxSensitivityDBm)
					}
				}
			}

			stats := w.Stats()
			ratio := stats.NoCoverageRatio()
			t.Logf("%s: %d ajan-tick, kapsama dışı oranı %%%.3f",
				file, stats.TotalTicks, ratio*100)

			// ADR-08/4: %5 uyarı eşiği
			if stats.ExceedsWarningThreshold() {
				t.Errorf("kapsama dışı oranı %%%.2f, ADR-08/4 uyarı eşiğini (%%5) aşıyor",
					ratio*100)
			}
		})
	}
}

// TestWorld_ParallelMatchesSerial, K10'un eşzamanlılıktan bağımsız olduğunu
// sınar: paralel ve seri güncelleme **birebir** aynı sonucu vermelidir.
//
// Bu, çift tampon tasarımının ana güvencesidir.
func TestWorld_ParallelMatchesSerial(t *testing.T) {
	const agentCount = 200
	const ticks = 300

	parallel, _ := buildWorld(t, "urban_ta.yaml", agentCount, 8)
	serial, _ := buildWorld(t, "urban_ta.yaml", agentCount, 1)

	for tick := 0; tick < ticks; tick++ {
		parallel.Tick(tick)
		serial.TickSerial(tick)

		pObs := parallel.Observations()
		sObs := serial.Observations()

		if len(pObs) != len(sObs) {
			t.Fatalf("tick %d: gözlem sayısı farklı", tick)
		}
		for i := range pObs {
			if pObs[i] != sObs[i] {
				t.Fatalf("tick %d ajan %d: paralel ve seri sonuç farklı\n  paralel: %+v\n  seri:    %+v",
					tick, i, pObs[i], sObs[i])
			}
		}
	}

	if parallel.Stats() != serial.Stats() {
		t.Errorf("istatistikler farklı: %+v vs %+v", parallel.Stats(), serial.Stats())
	}
	t.Logf("%d tick × %d ajan: paralel (8 goroutine) ve seri sonuçlar birebir aynı",
		ticks, agentCount)
}

// TestWorld_WorkerCountDoesNotAffectResult, goroutine sayısının sonucu
// değiştirmediğini sınar.
func TestWorld_WorkerCountDoesNotAffectResult(t *testing.T) {
	const agentCount = 120
	const ticks = 100

	reference, _ := buildWorld(t, "urban_ta.yaml", agentCount, 1)
	for tick := 0; tick < ticks; tick++ {
		reference.TickSerial(tick)
	}
	want := append([]Observation(nil), reference.Observations()...)

	for _, workers := range []int{2, 3, 7, 16, 64} {
		w, _ := buildWorld(t, "urban_ta.yaml", agentCount, workers)
		for tick := 0; tick < ticks; tick++ {
			w.Tick(tick)
		}
		for i, obs := range w.Observations() {
			if obs != want[i] {
				t.Fatalf("%d goroutine: ajan %d sonucu farklı\n  %+v\n  %+v",
					workers, i, obs, want[i])
			}
		}
	}
}

// TestWorld_AgentsAdvance, ajanların gerçekten hareket ettiğini sınar.
func TestWorld_AgentsAdvance(t *testing.T) {
	w, scn := buildWorld(t, "urban_ta.yaml", 50, 4)
	clock, _ := NewClock(scn.Simulation.TickMinutes)

	start := append([]State(nil), w.Agents()...)

	// İş saatine kadar ilerlet
	for tick := 0; tick <= 12*60/clock.TickMinutes(); tick++ {
		w.Tick(tick)
	}

	moved, atWork := 0, 0
	for i, s := range w.Agents() {
		if s.Pos != start[i].Pos {
			moved++
		}
		if s.Phase == PhaseWork {
			atWork++
		}
	}

	if moved == 0 {
		t.Error("hiçbir ajan hareket etmedi")
	}
	if atWork == 0 {
		t.Error("öğlen kimse işte değil — rutin uygulanmıyor olabilir")
	}
	t.Logf("öğlen: %d/%d ajan hareket etmiş, %d ajan işte", moved, len(start), atWork)
}

// TestWorld_CoverageStats, istatistik toplamayı sınar.
func TestWorld_CoverageStats(t *testing.T) {
	w, _ := buildWorld(t, "urban_ta.yaml", 20, 2)

	if got := w.Stats().TotalTicks; got != 0 {
		t.Errorf("başlangıçta toplam tick %d, beklenen 0", got)
	}
	if got := w.Stats().NoCoverageRatio(); got != 0 {
		t.Errorf("boş istatistikte oran %g, beklenen 0", got)
	}

	for tick := 0; tick < 10; tick++ {
		w.Tick(tick)
	}

	stats := w.Stats()
	if stats.TotalTicks != 200 {
		t.Errorf("toplam ajan-tick %d, beklenen 200 (20 ajan × 10 tick)", stats.TotalTicks)
	}
	if stats.UncoveredTicks > stats.TotalTicks {
		t.Errorf("kapsama dışı (%d) toplamdan (%d) büyük", stats.UncoveredTicks, stats.TotalTicks)
	}
	if r := stats.NoCoverageRatio(); r < 0 || r > 1 {
		t.Errorf("kapsama dışı oranı %g, [0,1] dışında", r)
	}
}

// ─── Çift tampon ─────────────────────────────────────────────────────────────

// TestDoubleBuffer_SwapsBuffers, tampon takasını sınar.
func TestDoubleBuffer_SwapsBuffers(t *testing.T) {
	initial := []State{
		{ID: 0, Phase: PhaseHome, CommuteMin: 10, DepartWorkMin: 480, DepartHomeMin: 1050},
		{ID: 1, Phase: PhaseHome, CommuteMin: 10, DepartWorkMin: 480, DepartHomeMin: 1050},
		{ID: 2, Phase: PhaseHome, CommuteMin: 10, DepartWorkMin: 480, DepartHomeMin: 1050},
	}

	b, err := NewDoubleBuffer(initial, 2)
	if err != nil {
		t.Fatalf("NewDoubleBuffer: %v", err)
	}
	t.Cleanup(b.Close)
	if b.Len() != 3 {
		t.Errorf("Len() = %d, beklenen 3", b.Len())
	}

	b.Step(func(s State, i int) State {
		s.Phase = PhaseWork
		return s
	})

	for i, s := range b.Current() {
		if s.Phase != PhaseWork {
			t.Errorf("ajan %d evresi güncellenmedi: %v", i, s.Phase)
		}
	}
}

// TestDoubleBuffer_ReadsSeeConsistentSnapshot, güncelleme sırasında okuma
// tamponunun değişmediğini sınar — çift tamponun varlık nedeni.
func TestDoubleBuffer_ReadsSeeConsistentSnapshot(t *testing.T) {
	const n = 500
	initial := make([]State, n)
	for i := range initial {
		initial[i] = State{ID: i, Phase: PhaseHome, CommuteMin: 10,
			DepartWorkMin: 480, DepartHomeMin: 1050}
	}

	b, err := NewDoubleBuffer(initial, 8)
	if err != nil {
		t.Fatalf("NewDoubleBuffer: %v", err)
	}
	t.Cleanup(b.Close)

	// Her ajan, güncelleme sırasında TÜM okuma tamponunu tarar.
	// Başka bir goroutine okuma tamponuna yazsaydı burada tutarsızlık görülürdü.
	b.Step(func(s State, i int) State {
		for j, other := range b.Current() {
			if other.ID != j || other.Phase != PhaseHome {
				t.Errorf("ajan %d güncellenirken okuma tamponu bozuldu: [%d] = %+v", i, j, other)
			}
		}
		s.Phase = PhaseWork
		return s
	})
}

// TestDoubleBuffer_NilUpdate, nil güncelleyicinin güvenle yok sayıldığını sınar.
func TestDoubleBuffer_NilUpdate(t *testing.T) {
	b, err := NewDoubleBuffer([]State{{ID: 0}}, 1)
	if err != nil {
		t.Fatalf("NewDoubleBuffer: %v", err)
	}
	t.Cleanup(b.Close)
	b.Step(nil)
	b.StepSerial(nil)
	if b.Current()[0].ID != 0 {
		t.Error("nil güncelleyici durumu bozdu")
	}
}

// TestDoubleBuffer_WorkerNormalization, goroutine sayısının sınırlandığını sınar.
func TestDoubleBuffer_WorkerNormalization(t *testing.T) {
	initial := []State{{ID: 0}, {ID: 1}}

	// Ajan sayısından fazla goroutine istenirse ajan sayısına indirilir
	b, err := NewDoubleBuffer(initial, 100)
	if err != nil {
		t.Fatalf("NewDoubleBuffer: %v", err)
	}
	t.Cleanup(b.Close)
	if b.Workers() > len(initial) {
		t.Errorf("Workers() = %d, ajan sayısını (%d) aşmamalı", b.Workers(), len(initial))
	}

	// Sıfır veya negatif → çekirdek sayısı
	b2, err := NewDoubleBuffer(initial, 0)
	if err != nil {
		t.Fatalf("NewDoubleBuffer: %v", err)
	}
	t.Cleanup(b2.Close)
	if b2.Workers() <= 0 {
		t.Errorf("Workers() = %d, pozitif olmalı", b2.Workers())
	}
}

// ─── Başarım ─────────────────────────────────────────────────────────────────

// BenchmarkWorldTick, tam ölçekli tick maliyetini ölçer.
//
// Gerçek koşu ölçeği: 1000 ajan × 109 site × 3 sektör.
// 30 gün × 288 tick = 8.640 tick beklenir.
func BenchmarkWorldTick(b *testing.B) {
	scn, err := config.Load(configsDir + "/urban_ta.yaml")
	if err != nil {
		b.Fatalf("config.Load: %v", err)
	}

	sel := benchSelector(b, scn)
	cfg, err := PlacementConfigFrom(scn, sel)
	if err != nil {
		b.Fatalf("PlacementConfigFrom: %v", err)
	}
	agents, err := PlaceAgents(cfg)
	if err != nil {
		b.Fatalf("PlaceAgents: %v", err)
	}

	clock, err := NewClock(scn.Simulation.TickMinutes)
	if err != nil {
		b.Fatalf("NewClock: %v", err)
	}
	w, err := NewWorld(agents, NewMobility(scn.Run.Seed, clock), sel, 0)
	if err != nil {
		b.Fatalf("NewWorld: %v", err)
	}
	defer w.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		w.Tick(i)
	}
	b.StopTimer()
	b.ReportMetric(float64(w.AgentCount()), "ajan")
}

// benchSelector, benchmark için gerçek envanterden seçici kurar.
func benchSelector(b *testing.B, scn *config.Scenario) *radio.Selector {
	b.Helper()
	t := &testing.T{}
	sel := buildSelector(t, scn)
	if t.Failed() {
		b.Fatal("seçici kurulamadı")
	}
	return sel
}
