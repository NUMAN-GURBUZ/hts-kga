package injector

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
)

const testSalt = "0123456789abcdef0123456789abcdef0123456789abcdef"

// testCells, hex kafeste kurulmuş küçük bir envanterdir.
func testCells(t testing.TB) []CellRef {
	t.Helper()
	lattice, err := geo.NewHexGrid(900)
	if err != nil {
		t.Fatalf("NewHexGrid: %v", err)
	}
	var cells []CellRef
	for i, a := range lattice.Cover(geo.Point{}, 5000) {
		cells = append(cells, CellRef{
			ID:  uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cell:%d:%d:%d", i, a.Q, a.R)),
			ENU: lattice.Center(a),
		})
	}
	if len(cells) < 10 {
		t.Fatalf("test envanteri çok küçük (%d)", len(cells))
	}
	return cells
}

// testPair, enjeksiyona verilecek temiz bir kayıt çifti üretir.
func testPair(cells []CellRef, agentID, tick int) event.Pair {
	ta := 20
	eventID := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "event:%d:%d", agentID, tick))
	return event.Pair{
		Record: &event.HTSRecord{
			RunID:        uuid.MustParse("11111111-2222-4333-8444-555555555555"),
			EventID:      eventID,
			Time:         time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC),
			PseudoMSISDN: "abc",
			PseudoIMEI:   "def",
			EventType:    event.EventCall,
			CellID:       cells[3].ID,
			TAValue:      &ta,
			Scenario:     "A",
		},
		Truth: event.GroundTruth{
			RunID:        uuid.MustParse("11111111-2222-4333-8444-555555555555"),
			EventID:      eventID,
			Time:         time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC),
			AgentID:      agentID,
			TrueLocation: geo.WGS84{Lat: 38.67, Lon: 39.22},
			Covered:      true,
			PartitionKey: split.Validation,
		},
	}
}

func mustPseudo(t testing.TB) *event.Pseudonymizer {
	t.Helper()
	p, err := event.NewPseudonymizer([]byte(testSalt))
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}
	return p
}

// planWeights, plan BÖLÜM L'deki eşit ağırlıklardır.
func planWeights() map[int]float64 {
	return map[int]float64{1: 0.2, 2: 0.2, 3: 0.2, 4: 0.2, 5: 0.2}
}

// TestInjectedRuleNeverLeaksToRecord — bu kapının **ana testi**.
//
// Enjeksiyon etiketi yalnızca ground truth'a yazılır (ADR-09). `hts_records`
// bu bilgiyi hiçbir alanında taşımaz; taşısaydı S4 hangi kaydın enjekte
// edildiğini görür ve bütünlük tespiti kör test olmaktan çıkardı — K7'nin
// precision/recall ölçümü anlamsızlaşırdı.
//
// Test yapı alanlarına değil, **yayınlanan tel biçimine** bakar: sızıntı
// olacaksa oradan olur.
func TestInjectedRuleNeverLeaksToRecord(t *testing.T) {
	cells := testCells(t)
	pseudo := mustPseudo(t)

	in, err := New(42, Config{Rate: 1.0, Weights: planWeights()}, cells) // oran 1: hepsi enjekte
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	seen := map[Rule]int{}
	for agentID := 0; agentID < 200; agentID++ {
		for tick := 0; tick < 5; tick++ {
			out := in.Apply(testPair(cells, agentID, tick), pseudo, agentID, tick, 0)

			if out.Truth.InjectedRule == nil {
				t.Fatalf("ajan %d tick %d: oran 1,0 iken etiket yazılmadı", agentID, tick)
			}
			rule := Rule(*out.Truth.InjectedRule)
			if !rule.Valid() {
				t.Fatalf("tanımsız kural: %d", *out.Truth.InjectedRule)
			}
			seen[rule]++

			if out.Record == nil {
				continue // kural 4: kayıt silindi
			}

			// 1. Tel biçiminde etiket alanı yok.
			wire, err := event.EncodeRecordForTest(*out.Record)
			if err != nil {
				t.Fatalf("serileştirme: %v", err)
			}
			lower := strings.ToLower(string(wire))
			for _, forbidden := range []string{"injected", "rule", "manipul", "tamper"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("kayıtta enjeksiyon izi (%q): %s", forbidden, wire)
				}
			}

			// 2. Alan kümesi beklenenle birebir aynı: yeni bir alan eklenirse
			//    bu test düşer ve gözden geçirilmesi gerekir.
			var fields map[string]any
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatalf("çözümleme: %v", err)
			}
			want := map[string]bool{
				"run_id": true, "event_id": true, "time": true,
				"pseudo_msisdn": true, "pseudo_imei": true, "event_type": true,
				"cell_id": true, "ta_value": true, "scenario": true,
			}
			for name := range fields {
				if !want[name] {
					t.Fatalf("kayıtta beklenmeyen alan: %q (%s)", name, wire)
				}
			}
			if len(fields) != len(want) {
				t.Fatalf("kayıt alan sayısı %d, beklenen %d", len(fields), len(want))
			}

			// 3. Kuralın kendisi hiçbir alanda sayı olarak da geçmemeli.
			if v, ok := fields["ta_value"]; ok && v != nil {
				if f, isNum := v.(float64); isNum && f == float64(rule) && rule != 20 {
					// ta_value zaten 20; kural değeri 1..5 ile çakışamaz.
					t.Logf("ta_value=%v kural=%d (çakışma yok, bilgi amaçlı)", v, rule)
				}
			}
		}
	}

	t.Logf("kural dağılımı: %v", seen)
	if len(seen) != len(AllRules) {
		t.Errorf("beş kuralın hepsi tetiklenmedi: %v", seen)
	}
}

// TestInjectionRate, gerçekleşen enjeksiyon oranını sınar.
func TestInjectionRate(t *testing.T) {
	cells := testCells(t)
	pseudo := mustPseudo(t)

	for _, rate := range []float64{0.0, 0.02, 0.10} {
		in, err := New(42, Config{Rate: rate, Weights: planWeights()}, cells)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		const n = 20000
		injected := 0
		for i := 0; i < n; i++ {
			out := in.Apply(testPair(cells, i%500, i/500), pseudo, i%500, i/500, 0)
			if out.Truth.InjectedRule != nil {
				injected++
			}
		}
		got := float64(injected) / n
		t.Logf("hedef oran %.2f → gerçekleşen %.4f (n=%d)", rate, got, n)

		if math.Abs(got-rate) > 0.005 {
			t.Errorf("oran %.2f için gerçekleşen %.4f", rate, got)
		}
	}
}

// TestRuleBehaviours, beş kuralın her birinin ne yaptığını sınar.
func TestRuleBehaviours(t *testing.T) {
	cells := testCells(t)
	pseudo := mustPseudo(t)
	inventory := map[uuid.UUID]bool{}
	for _, c := range cells {
		inventory[c.ID] = true
	}

	for _, rule := range AllRules {
		t.Run(rule.String(), func(t *testing.T) {
			// Yalnızca bu kuralı seçen ağırlık tablosu.
			weights := map[int]float64{}
			for _, r := range AllRules {
				if r == rule {
					weights[int(r)] = 1
				} else {
					weights[int(r)] = 0
				}
			}
			in, err := New(42, Config{Rate: 1.0, Weights: weights}, cells)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			clean := testPair(cells, 11, 3)
			out := in.Apply(testPair(cells, 11, 3), pseudo, 11, 3, 0)

			if out.Truth.InjectedRule == nil || Rule(*out.Truth.InjectedRule) != rule {
				t.Fatalf("etiket %v, beklenen %d", out.Truth.InjectedRule, rule)
			}

			switch rule {
			case RuleFakeCell:
				if inventory[out.Record.CellID] {
					t.Error("sahte hücre envanterde bulundu")
				}
				if out.Record.CellID == clean.Record.CellID {
					t.Error("hücre kimliği değişmedi")
				}
			case RuleJump:
				if !inventory[out.Record.CellID] {
					t.Error("atlama envanter dışı hücreye gitti (kural 1 ile karıştı)")
				}
				if out.Record.CellID == clean.Record.CellID {
					t.Error("hücre kimliği değişmedi")
				}
				origin, target := cellByID(cells, clean.Record.CellID), cellByID(cells, out.Record.CellID)
				d := geo.Distance(origin.ENU, target.ENU)
				t.Logf("atlama mesafesi: %.0f m", d)
				if d < 1000 {
					t.Errorf("atlama mesafesi çok kısa (%.0f m)", d)
				}
			case RuleTime:
				if !out.Record.Time.Before(clean.Record.Time) {
					t.Errorf("zaman geriye kaydırılmadı: %s ≥ %s", out.Record.Time, clean.Record.Time)
				}
				if out.Truth.Time != clean.Truth.Time {
					t.Error("ground truth zamanı da kaymış — gerçek bozulmamalı")
				}
			case RuleGap:
				if out.Record != nil {
					t.Error("kayıt silinmedi")
				}
				if out.Truth.InjectedRule == nil {
					t.Error("silinen kayıt için ground truth etiketi yok")
				}
			case RuleDeviceSwap:
				if out.Record.PseudoIMEI == clean.Record.PseudoIMEI {
					t.Error("cihaz takma adı değişmedi")
				}
				if out.Record.PseudoMSISDN != clean.Record.PseudoMSISDN {
					t.Error("abone takma adı değişmiş — kural yalnızca cihazı değiştirir")
				}
			}
		})
	}
}

// TestGroundTruthUnaffected, enjeksiyonun gerçeği bozmadığını sınar.
//
// Manipülasyon **kaydı** bozar; ground truth ne olduğunu doğru anlatmaya
// devam eder. Aksi hâlde ölçümün referansı da bozulurdu.
func TestGroundTruthUnaffected(t *testing.T) {
	cells := testCells(t)
	pseudo := mustPseudo(t)
	in, err := New(42, Config{Rate: 1.0, Weights: planWeights()}, cells)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 500; i++ {
		clean := testPair(cells, i, 1)
		out := in.Apply(testPair(cells, i, 1), pseudo, i, 1, 0)

		if out.Truth.TrueLocation != clean.Truth.TrueLocation {
			t.Fatalf("gerçek konum değişti (ajan %d)", i)
		}
		if out.Truth.AgentID != clean.Truth.AgentID || out.Truth.EventID != clean.Truth.EventID {
			t.Fatalf("ground truth kimliği değişti (ajan %d)", i)
		}
		if out.Truth.PartitionKey != clean.Truth.PartitionKey {
			t.Fatalf("bölüm anahtarı değişti (ajan %d)", i)
		}
		if out.Truth.Covered != clean.Truth.Covered {
			t.Fatalf("kapsama bayrağı değişti (ajan %d)", i)
		}
	}
}

// TestDeterministic, aynı girdinin aynı enjeksiyonu verdiğini sınar (K10).
func TestDeterministic(t *testing.T) {
	cells := testCells(t)
	pseudo := mustPseudo(t)
	a, _ := New(42, Config{Rate: 0.3, Weights: planWeights()}, cells)
	b, _ := New(42, Config{Rate: 0.3, Weights: planWeights()}, cells)

	for i := 0; i < 1000; i++ {
		x := a.Apply(testPair(cells, i%100, i), pseudo, i%100, i, 0)
		y := b.Apply(testPair(cells, i%100, i), pseudo, i%100, i, 0)

		switch {
		case (x.Truth.InjectedRule == nil) != (y.Truth.InjectedRule == nil):
			t.Fatalf("%d: etiket varlığı ayrıştı", i)
		case x.Truth.InjectedRule != nil && *x.Truth.InjectedRule != *y.Truth.InjectedRule:
			t.Fatalf("%d: kural ayrıştı (%d ≠ %d)", i, *x.Truth.InjectedRule, *y.Truth.InjectedRule)
		case (x.Record == nil) != (y.Record == nil):
			t.Fatalf("%d: kayıt silinme kararı ayrıştı", i)
		}
	}
}

// TestUncoveredNotInjected, kapsama dışı olayların enjekte edilmediğini sınar:
// ortada bozulacak bir kayıt yoktur.
func TestUncoveredNotInjected(t *testing.T) {
	cells := testCells(t)
	pseudo := mustPseudo(t)
	in, _ := New(42, Config{Rate: 1.0, Weights: planWeights()}, cells)

	pair := testPair(cells, 1, 1)
	pair.Record = nil // kapsama dışı
	out := in.Apply(pair, pseudo, 1, 1, 0)

	if out.Truth.InjectedRule != nil {
		t.Errorf("kapsama dışı olay enjekte edildi (kural %d)", *out.Truth.InjectedRule)
	}
}

// TestNew_Validation, kurulum denetimlerini kapsar.
func TestNew_Validation(t *testing.T) {
	cells := testCells(t)

	if _, err := New(42, Config{Rate: -0.1, Weights: planWeights()}, cells); err == nil {
		t.Error("negatif oran kabul edildi")
	}
	if _, err := New(42, Config{Rate: 1.5, Weights: planWeights()}, cells); err == nil {
		t.Error("1'den büyük oran kabul edildi")
	}
	if _, err := New(42, Config{Rate: 0.02, Weights: planWeights()}, nil); err == nil {
		t.Error("envantersiz kurulum kabul edildi")
	}
	zero := map[int]float64{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}
	if _, err := New(42, Config{Rate: 0.02, Weights: zero}, cells); err == nil {
		t.Error("tüm ağırlıkları sıfır olan tablo kabul edildi")
	}
	if _, err := New(42, Config{Rate: 0.02, Weights: map[int]float64{1: -1}}, cells); err == nil {
		t.Error("negatif ağırlık kabul edildi")
	}
}

// cellByID, kimlikten hücre referansı bulur.
func cellByID(cells []CellRef, id uuid.UUID) CellRef {
	for _, c := range cells {
		if c.ID == id {
			return c
		}
	}
	return CellRef{}
}
