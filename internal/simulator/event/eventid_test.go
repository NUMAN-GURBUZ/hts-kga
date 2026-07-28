package event

import (
	"math"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// testRunID, bu testlerde kullanılan keyfi ama sabit koşu kimliğidir.
const testRunID = "11111111-2222-4333-8444-555555555555"

// mustID, olay kimliğini türetir; anahtar geçersizse testi düşürür.
func mustID(t *testing.T, k Key) uuid.UUID {
	t.Helper()
	id, err := NewID(k)
	if err != nil {
		t.Fatalf("NewID(%+v): %v", k, err)
	}
	return id
}

// key, testRunID üzerinde bir Key kurar.
func key(agentID, tick, seq int) Key {
	return Key{RunID: uuid.MustParse(testRunID), AgentID: agentID, Tick: tick, Seq: seq}
}

// TestEventIDGoldenVector, kodlamayı dondurur.
//
// Bu değerler uygulamadan bağımsız olarak hesaplanmıştır (RFC 4122 §4.3,
// belgelenen bayt düzenine elle uygulanarak). Test düşerse, üretilmiş her
// olayın kimliği değişmiş demektir: namespace, bir alan genişliği veya bayt
// sırası değiştirilmiştir. Bu asla bir düzeltme değildir — mevcut koşularda
// hts_records, ground_truth ve estimates arasındaki bağı koparır ve K10
// tekrarlanabilirliğini geçersiz kılar.
func TestEventIDGoldenVector(t *testing.T) {
	cases := []struct {
		name  string
		key   Key
		wantS string
	}{
		{"referans", key(42, 8639, 0), "6e89a177-fb74-5d45-aa75-cd6d47b2a41f"},
		{"tick içindeki ikinci olay", key(42, 8639, 1), "3afa79dc-78e3-51c6-a136-7fde9d5c7220"},
		{"ajan 1 tick 0", key(1, 0, 0), "f97a725f-773d-522a-a4f3-19799e145606"},
		{"ajan 0 tick 1", key(0, 1, 0), "e2445eb0-ef03-5bae-8784-d8c6a6320579"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustID(t, tc.key)
			if got.String() != tc.wantS {
				t.Fatalf("olay kimliği değişti:\n bulunan  %s\n beklenen %s", got, tc.wantS)
			}
		})
	}
}

// TestNamespaceFrozen, namespace sabitinin kendisini korur.
func TestNamespaceFrozen(t *testing.T) {
	if got := Namespace().String(); got != "29b15779-62b7-4b8d-889e-417227237e61" {
		t.Fatalf("namespace değişti: %s", got)
	}
}

// TestEventIDDeterministic, tüm tasarımın var oluş nedeni olan özelliği
// denetler.
func TestEventIDDeterministic(t *testing.T) {
	k := key(7, 1234, 2)
	first := mustID(t, k)
	for i := 0; i < 1000; i++ {
		if got := mustID(t, k); got != first {
			t.Fatalf("%d. yineleme %s üretti, beklenen %s", i, got, first)
		}
	}
}

// TestEventIDIsUUIDv5, şemanın UUID sütunlarının ve dışarıdaki her tüketicinin
// dayandığı RFC 4122 sürüm ve variant bitlerini denetler.
func TestEventIDIsUUIDv5(t *testing.T) {
	for _, k := range []Key{key(0, 0, 0), key(42, 8639, 0), key(999, 1, 7)} {
		id := mustID(t, k)
		if v := id.Version(); v != 5 {
			t.Errorf("%+v: sürüm = %d, beklenen 5", k, v)
		}
		if v := id.Variant(); v != uuid.RFC4122 {
			t.Errorf("%+v: variant = %v, beklenen RFC4122", k, v)
		}
	}
}

// TestEventIDDistinctPerComponent, her bileşenin kimliğe gerçekten katıldığını
// denetler; değişken genişlikli bir kodlamanın çakıştıracağı çift dâhil.
func TestEventIDDistinctPerComponent(t *testing.T) {
	base := key(1, 1, 1)
	other := uuid.MustParse("99999999-8888-4777-8666-555555555555")

	variants := map[string]Key{
		"koşu kimliği": {RunID: other, AgentID: 1, Tick: 1, Seq: 1},
		"ajan kimliği": key(2, 1, 1),
		"tick":         key(1, 2, 1),
		"sıra":         key(1, 1, 2),
	}

	baseID := mustID(t, base)
	for name, v := range variants {
		if got := mustID(t, v); got == baseID {
			t.Errorf("%s kimliği etkilemiyor (%s)", name, got)
		}
	}

	// Birleştirme belirsizliği: sabit genişlik olmasaydı (1,0) ile (0,1)
	// aynı ada kodlanırdı.
	if mustID(t, key(1, 0, 0)) == mustID(t, key(0, 1, 0)) {
		t.Error("ajan/tick sınırı belirsiz: (1,0) ile (0,1) çakışıyor")
	}
}

// TestEventIDRunIsolation, aynı senaryonun iki koşusunun hiçbir kimliği
// paylaşmadığını denetler; tek veritabanının tüm koşuları tutabilmesi buna
// dayanır (ADR-05).
func TestEventIDRunIsolation(t *testing.T) {
	runA := uuid.MustParse(testRunID)
	runB := uuid.MustParse("99999999-8888-4777-8666-555555555555")

	seen := make(map[uuid.UUID]bool, 2048)
	for agentID := 0; agentID < 32; agentID++ {
		for tick := 0; tick < 32; tick++ {
			seen[mustID(t, Key{RunID: runA, AgentID: agentID, Tick: tick})] = true
		}
	}
	for agentID := 0; agentID < 32; agentID++ {
		for tick := 0; tick < 32; tick++ {
			id := mustID(t, Key{RunID: runB, AgentID: agentID, Tick: tick})
			if seen[id] {
				t.Fatalf("B koşusu A koşusunun kimliğini yeniden kullandı (ajan %d, tick %d)",
					agentID, tick)
			}
		}
	}
}

// TestKeyValidate, kodlamanın sınırlarını kapsar.
func TestKeyValidate(t *testing.T) {
	runID := uuid.MustParse(testRunID)

	valid := []Key{
		{RunID: runID},
		{RunID: runID, AgentID: math.MaxInt32, Tick: math.MaxInt32, Seq: maxSeq},
	}
	for _, k := range valid {
		if err := k.Validate(); err != nil {
			t.Errorf("%+v: beklenmeyen hata: %v", k, err)
		}
	}

	invalid := map[string]Key{
		"boş koşu kimliği": {AgentID: 1},
		"negatif ajan":     {RunID: runID, AgentID: -1},
		"negatif tick":     {RunID: runID, Tick: -1},
		"negatif sıra":     {RunID: runID, Seq: -1},
		"ajan taşması":     {RunID: runID, AgentID: math.MaxInt32 + 1},
		"sıra taşması":     {RunID: runID, Seq: maxSeq + 1},
	}
	for name, k := range invalid {
		if err := k.Validate(); err == nil {
			t.Errorf("%s: hata bekleniyordu, gelmedi", name)
		}
		if _, err := NewID(k); err == nil {
			t.Errorf("%s: NewID geçersiz anahtarı kabul etti", name)
		}
	}
}

// TestPBT_EventIDInjective, anahtar uzayını örnekler ve farklı anahtarların
// hiçbir zaman aynı kimliği paylaşmadığını denetler. Buradaki bir çakışma,
// estimates ⋈ ground_truth birleşiminde iki olayı sessizce tek olaya indirger.
func TestPBT_EventIDInjective(t *testing.T) {
	runIDs := []uuid.UUID{
		uuid.MustParse(testRunID),
		uuid.MustParse("99999999-8888-4777-8666-555555555555"),
	}

	rapid.Check(t, func(rt *rapid.T) {
		seen := make(map[uuid.UUID]Key, 256)
		for i := 0; i < 256; i++ {
			k := Key{
				RunID:   runIDs[rapid.IntRange(0, len(runIDs)-1).Draw(rt, "run")],
				AgentID: rapid.IntRange(0, 999).Draw(rt, "agent"),
				Tick:    rapid.IntRange(0, 8639).Draw(rt, "tick"),
				Seq:     rapid.IntRange(0, 3).Draw(rt, "seq"),
			}
			id, err := NewID(k)
			if err != nil {
				rt.Fatalf("NewID(%+v): %v", k, err)
			}
			if prev, ok := seen[id]; ok && prev != k {
				rt.Fatalf("çakışma: %+v ile %+v aynı kimliğe düşüyor (%s)", prev, k, id)
			}
			seen[id] = k
		}
	})
}

// BenchmarkNewID, olay başına sıcak yoldaki maliyeti ölçer.
func BenchmarkNewID(b *testing.B) {
	k := key(42, 8639, 0)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := NewID(k); err != nil {
			b.Fatal(err)
		}
	}
}
