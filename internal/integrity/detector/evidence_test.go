package detector

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// TestEvidence_AlwaysVersioned, şema kısıtının (evidence ? 'v') karşılandığını
// sınar.
func TestEvidence_AlwaysVersioned(t *testing.T) {
	ev, err := NewEvidence(integrityrule.Inventory, DefaultMarginCap).
		Str("cell_id", "abc").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if v, ok := ev["v"]; !ok || v != EvidenceSchemaVersion {
		t.Errorf("sürüm alanı eksik veya yanlış: %v", ev["v"])
	}
	if r, ok := ev["rule"]; !ok || r != int(integrityrule.Inventory) {
		t.Errorf("kural alanı eksik veya yanlış: %v", ev["rule"])
	}
}

// TestEvidence_RejectsGroundTruthKeys, kör testin kanıt katmanındaki ifadesini
// sınar (ADR-31/5).
//
// Dedektör bu değerlere zaten erişemez (rol ACL'i). Ama erişemediğini teste
// bağlamak ayrı bir korumadır: bir gün biri hata ayıklama sırasında gerçek
// konumu kanıta yazarsa, bulgular ground truth taşımaya başlar ve K7 ölçümü
// kirlenir.
func TestEvidence_RejectsGroundTruthKeys(t *testing.T) {
	banned := []string{
		"lat", "lon", "true_lat", "true_location", "location",
		"agent_id", "agent", "injected_rule", "injected",
		"partition_key", "covered",
		"LAT", "True_Location", "AgentID", // büyük/küçük harf duyarsız
	}

	for _, key := range banned {
		t.Run(key, func(t *testing.T) {
			_, err := NewEvidence(integrityrule.Velocity, DefaultMarginCap).
				Str(key, "sızıntı").Build()
			if err == nil {
				t.Fatalf("%q anahtarı reddedilmeliydi (ADR-31/5)", key)
			}
			if !strings.Contains(err.Error(), "ground truth") {
				t.Errorf("hata mesajı gerekçeyi söylemeli: %v", err)
			}
		})
	}
}

// TestEvidence_RejectsReservedKeys, kurucunun kendi alanlarının ezilemediğini
// sınar.
func TestEvidence_RejectsReservedKeys(t *testing.T) {
	for _, key := range []string{"v", "rule", "capped", "V", "Rule"} {
		if _, err := NewEvidence(integrityrule.Inventory, DefaultMarginCap).
			Int(key, 99).Build(); err == nil {
			t.Errorf("%q ayrılmış anahtar reddedilmeliydi", key)
		}
	}
}

// TestEvidence_RejectsEmptyKey, boş anahtarın reddedildiğini sınar.
func TestEvidence_RejectsEmptyKey(t *testing.T) {
	if _, err := NewEvidence(integrityrule.Inventory, DefaultMarginCap).
		Str("", "x").Build(); err == nil {
		t.Error("boş anahtar reddedilmeliydi")
	}
}

// TestEvidence_ClampsInfinity, sonsuz hız durumunun kanıtı düşürmediğini sınar.
//
// Bu, Δt = 0 popülasyonudur ve kentsel koşuda >300 km/h isabetlerin tamamı
// buradadır. Koruma olmadan json.Marshal hata verir ve kural 2 kentselde sıfır
// bulgu üretir.
func TestEvidence_ClampsInfinity(t *testing.T) {
	ev, err := NewEvidence(integrityrule.Velocity, 1000).
		Float("velocity_kmh", math.Inf(1)).
		Bool("zero_interval", true).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := ev["velocity_kmh"]; got != 1000.0 {
		t.Errorf("velocity_kmh = %v, beklenen 1000 (kapılmış)", got)
	}
	if capped, ok := ev["capped"]; !ok || capped != true {
		t.Error("capped işareti konulmamış")
	}
	if _, err := json.Marshal(ev); err != nil {
		t.Errorf("kapılmış kanıt serileşemedi: %v", err)
	}
}

// TestEvidence_NoCappedFlagWhenFinite, sonlu değerlerde capped işaretinin
// konulmadığını sınar.
func TestEvidence_NoCappedFlagWhenFinite(t *testing.T) {
	ev, err := NewEvidence(integrityrule.Velocity, 1000).
		Float("velocity_kmh", 480.5).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := ev["capped"]; ok {
		t.Error("sonlu değerde capped işareti konmamalı")
	}
}

// TestEvidence_FieldTypes, her alan tipinin JSON'a düzgün gittiğini sınar.
func TestEvidence_FieldTypes(t *testing.T) {
	id := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	ev, err := NewEvidence(integrityrule.TimeOrder, DefaultMarginCap).
		UUID("prev_event_id", id).
		Time("record_time", when).
		Float("backstep_s", 7200).
		Int("imei_count", 2).
		Bool("strict", true).
		Str("attribution", "local_support").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back["prev_event_id"] != id.String() {
		t.Errorf("prev_event_id = %v", back["prev_event_id"])
	}
	if back["record_time"] != "2026-03-04T05:06:07Z" {
		t.Errorf("record_time = %v", back["record_time"])
	}
	if back["backstep_s"] != 7200.0 {
		t.Errorf("backstep_s = %v", back["backstep_s"])
	}
}

// TestEvidence_MustBuildSurfacesError, MustBuild'in hatayı gizlemediğini sınar.
//
// Panik atmak yerine kanıt gövdesine hata yazılır; motorun geçerlilik denetimi
// o bulguyu reddeder ve sayaca ekler. Böylece programlama hatası koşuyu
// durdurmaz ama sessiz de kalmaz.
func TestEvidence_MustBuildSurfacesError(t *testing.T) {
	ev := NewEvidence(integrityrule.Velocity, DefaultMarginCap).
		Str("agent_id", "sızıntı").MustBuild()

	if _, ok := ev["error"]; !ok {
		t.Fatal("MustBuild hatayı kanıt gövdesine yazmalı")
	}
	if _, ok := ev["agent_id"]; ok {
		t.Error("yasaklı alan gövdeye girmemeli")
	}
}

// TestClampFinite, sonluluk korumasının sınır davranışını sınar.
func TestClampFinite(t *testing.T) {
	tests := []struct {
		name       string
		in, cap    float64
		want       float64
		wantCapped bool
	}{
		{"normal", 42, 1000, 42, false},
		{"tam sınır", 1000, 1000, 1000, false},
		{"sınır üstü", 1001, 1000, 1000, true},
		{"+Inf", math.Inf(1), 1000, 1000, true},
		{"-Inf", math.Inf(-1), 1000, -1000, true},
		{"negatif sınır üstü", -1001, 1000, -1000, true},
		{"sıfır", 0, 1000, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, capped := ClampFinite(tc.in, tc.cap)
			if got != tc.want || capped != tc.wantCapped {
				t.Errorf("ClampFinite(%g, %g) = (%g, %v), beklenen (%g, %v)",
					tc.in, tc.cap, got, capped, tc.want, tc.wantCapped)
			}
		})
	}

	// NaN sıfıra değil cap'e çekilir: NaN "değer yok" değil "hesap bozuk"
	// işaretidir; sıfıra çevrilmesi bulguyu eşiğin altına düşürüp sessizce
	// yok ederdi.
	if got, capped := ClampFinite(math.NaN(), 1000); got != 1000 || !capped {
		t.Errorf("ClampFinite(NaN) = (%g, %v), beklenen (1000, true)", got, capped)
	}
}

// TestPBT_ClampFiniteAlwaysProducesFiniteValue, sonluluk korumasının hiçbir
// girdide kaçak bırakmadığını sınar.
func TestPBT_ClampFiniteAlwaysProducesFiniteValue(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := rapid.OneOf(
			rapid.Float64(),
			rapid.Just(math.Inf(1)),
			rapid.Just(math.Inf(-1)),
			rapid.Just(math.NaN()),
		).Draw(t, "v")
		cap := rapid.Float64Range(1e-6, 1e12).Draw(t, "cap")

		got, _ := ClampFinite(v, cap)
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Fatalf("ClampFinite(%g, %g) = %g (sonlu değil)", v, cap, got)
		}
		if math.Abs(got) > cap {
			t.Fatalf("ClampFinite(%g, %g) = %g (sınır dışı)", v, cap, got)
		}
	})
}

// TestPBT_ClampMarginSatisfiesSchemaCheck, ClampMargin çıktısının şema
// kısıtını (margin >= 1.0 AND margin <= cap) her girdide sağladığını sınar.
func TestPBT_ClampMarginSatisfiesSchemaCheck(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := rapid.OneOf(
			rapid.Float64(),
			rapid.Just(math.Inf(1)),
			rapid.Just(math.Inf(-1)),
			rapid.Just(math.NaN()),
			rapid.Just(0.0),
		).Draw(t, "v")
		cap := rapid.Float64Range(1, 1e12).Draw(t, "cap")

		got, _ := ClampMargin(v, cap)
		switch {
		case math.IsNaN(got) || math.IsInf(got, 0):
			t.Fatalf("ClampMargin(%g, %g) = %g (sonlu değil)", v, cap, got)
		case got < 1.0:
			t.Fatalf("ClampMargin(%g, %g) = %g < 1.0 (CHECK ihlali)", v, cap, got)
		case got > cap:
			t.Fatalf("ClampMargin(%g, %g) = %g > cap (CHECK ihlali)", v, cap, got)
		}
	})
}
