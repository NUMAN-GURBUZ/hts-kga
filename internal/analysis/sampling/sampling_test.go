package sampling

import (
	"fmt"
	"math"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
)

const (
	testSeed  = 42
	testRatio = 0.80
)

// eventIDs, deterministik olay kimlikleri üretir (UUIDv5, ADR-01 gibi).
func eventIDs(n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("event:%d", i)))
	}
	return ids
}

func mustPolicy(t testing.TB, cfg Config) Policy {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}
	return p
}

// TestPolicy_ValidationSelectsOnlyV, doğrulama modunun yalnızca 'V' kümesini
// seçtiğini doğrular.
func TestPolicy_ValidationSelectsOnlyV(t *testing.T) {
	p := mustPolicy(t, Config{Mode: ModeValidation, Seed: testSeed, SplitRatio: testRatio})
	splitter, err := split.New(testSeed, testRatio)
	if err != nil {
		t.Fatalf("split.New: %v", err)
	}

	ids := eventIDs(20000)
	selected := 0
	for _, id := range ids {
		included := p.Includes(id)
		isV := splitter.Of(id) == split.Validation
		if included != isV {
			t.Fatalf("olay %s: seçildi=%v, 'V' üyeliği=%v", id, included, isV)
		}
		if included {
			selected++
		}
	}

	ratio := float64(selected) / float64(len(ids))
	t.Logf("'V' oranı: %.4f (beklenen %.2f)", ratio, 1-testRatio)
	if math.Abs(ratio-(1-testRatio)) > 0.02 {
		t.Errorf("'V' oranı %.4f, %.2f ± 0,02 beklenir", ratio, 1-testRatio)
	}
}

// TestPolicy_CalibrationHitsTarget, kalibrasyon modunun hedefe yakın sayıda
// olay seçtiğini doğrular.
func TestPolicy_CalibrationHitsTarget(t *testing.T) {
	const total, target = 100000, 5000

	p := mustPolicy(t, Config{
		Mode:                ModeCalibration,
		Seed:                testSeed,
		SplitRatio:          testRatio,
		TargetEvents:        target,
		ExpectedTotalEvents: total,
	})

	selected := 0
	for _, id := range eventIDs(total) {
		if p.Includes(id) {
			selected++
		}
	}

	rel := float64(selected-target) / float64(target)
	t.Logf("kalibrasyon örneklemi: %d olay (hedef %d, sapma %+.1f%%, keepRatio %.4f)",
		selected, target, rel*100, p.KeepRatio())

	if math.Abs(rel) > 0.10 {
		t.Errorf("örneklem %d, %d ± %%10 beklenir", selected, target)
	}
}

// TestPolicy_CalibrationAndValidationDisjoint, iki kümenin kesişmediğini
// doğrular — K4'ün kod düzeyindeki temeli.
//
// Kesişselerdi model kendi kalibrasyon verisinde ölçülürdü; bu, bilimsel
// geçersizliğin en yaygın biçimidir.
func TestPolicy_CalibrationAndValidationDisjoint(t *testing.T) {
	const total = 50000

	calib := mustPolicy(t, Config{
		Mode: ModeCalibration, Seed: testSeed, SplitRatio: testRatio,
		TargetEvents: 5000, ExpectedTotalEvents: total,
	})
	valid := mustPolicy(t, Config{Mode: ModeValidation, Seed: testSeed, SplitRatio: testRatio})

	overlap := 0
	for _, id := range eventIDs(total) {
		if calib.Includes(id) && valid.Includes(id) {
			overlap++
		}
	}
	if overlap != 0 {
		t.Fatalf("%d olay hem kalibrasyon hem doğrulama kümesinde — K4 ihlali", overlap)
	}
}

// TestPolicy_FullIncludesEverything, tam modun hiçbir olayı elemediğini
// doğrular (K9 ölçekleme ölçümü buna dayanır).
func TestPolicy_FullIncludesEverything(t *testing.T) {
	p := mustPolicy(t, Config{Mode: ModeFull, Seed: testSeed, SplitRatio: testRatio})

	for _, id := range eventIDs(1000) {
		if !p.Includes(id) {
			t.Fatalf("tam modda olay %s elendi", id)
		}
	}
}

// TestPolicy_Deterministic, aynı yapılandırmanın aynı kümeyi seçtiğini
// doğrular (K10).
func TestPolicy_Deterministic(t *testing.T) {
	cfg := Config{
		Mode: ModeCalibration, Seed: testSeed, SplitRatio: testRatio,
		TargetEvents: 1000, ExpectedTotalEvents: 20000,
	}
	ids := eventIDs(20000)

	first := mustPolicy(t, cfg)
	want := make([]bool, len(ids))
	for i, id := range ids {
		want[i] = first.Includes(id)
	}

	for iter := 0; iter < 5; iter++ {
		p := mustPolicy(t, cfg)
		for i, id := range ids {
			if p.Includes(id) != want[i] {
				t.Fatalf("iter %d: olay %s kararı değişti", iter, id)
			}
		}
	}
}

// TestPolicy_SeedChangesSample, farklı tohumun farklı örneklem verdiğini
// doğrular: seyreltme gerçekten tohuma bağlı olmalı.
func TestPolicy_SeedChangesSample(t *testing.T) {
	base := Config{
		Mode: ModeCalibration, SplitRatio: testRatio,
		TargetEvents: 2000, ExpectedTotalEvents: 20000,
	}
	ids := eventIDs(20000)

	a := base
	a.Seed = 42
	b := base
	b.Seed = 43

	pa, pb := mustPolicy(t, a), mustPolicy(t, b)

	same, both := 0, 0
	for _, id := range ids {
		ia, ib := pa.Includes(id), pb.Includes(id)
		if ia && ib {
			both++
		}
		if ia == ib {
			same++
		}
	}
	if both == 0 {
		t.Error("iki tohum hiç ortak olay seçmedi — bağımsızlık şüpheli")
	}
	if same == len(ids) {
		t.Error("tohum değişince örneklem hiç değişmedi")
	}
	t.Logf("tohum 42 ↔ 43: %d ortak olay", both)
}

// TestPolicy_Rejects, geçersiz yapılandırmayı reddeder.
func TestPolicy_Rejects(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"mod yok", Config{Seed: 1, SplitRatio: 0.8}},
		{"mod geçersiz", Config{Mode: "yok", Seed: 1, SplitRatio: 0.8}},
		{"oran geçersiz", Config{Mode: ModeValidation, Seed: 1, SplitRatio: 1.5}},
		{"kalibrasyon hedefi yok", Config{
			Mode: ModeCalibration, Seed: 1, SplitRatio: 0.8, ExpectedTotalEvents: 100,
		}},
		{"beklenen hacim yok", Config{
			Mode: ModeCalibration, Seed: 1, SplitRatio: 0.8, TargetEvents: 10,
		}},
	}
	for _, tc := range cases {
		if _, err := New(tc.cfg); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}

// TestParseMode, mod çözümlemesini doğrular.
func TestParseMode(t *testing.T) {
	for _, s := range []string{"validation", "calibration", "full"} {
		if _, err := ParseMode(s); err != nil {
			t.Errorf("%q reddedildi: %v", s, err)
		}
	}
	if _, err := ParseMode("hepsi"); err == nil {
		t.Error("geçersiz mod kabul edildi")
	}
}

// TestExactSample_ExactCount, tam N seçimini doğrular.
func TestExactSample_ExactCount(t *testing.T) {
	ids := eventIDs(10000)

	for _, target := range []int{1, 100, 5000, 10000, 20000} {
		got := ExactSample(ids, target, testSeed)
		want := target
		if want > len(ids) {
			want = len(ids)
		}
		if len(got) != want {
			t.Errorf("hedef %d: %d olay seçildi, %d beklenir", target, len(got), want)
		}

		seen := make(map[uuid.UUID]struct{}, len(got))
		for _, id := range got {
			if _, dup := seen[id]; dup {
				t.Fatalf("hedef %d: yinelenen olay %s", target, id)
			}
			seen[id] = struct{}{}
		}
	}
}

// TestExactSample_OrderIndependent, girdi sırasının seçimi etkilemediğini
// doğrular: veritabanı sırası değişse bile aynı örneklem çıkmalı.
func TestExactSample_OrderIndependent(t *testing.T) {
	ids := eventIDs(5000)
	want := ExactSample(ids, 500, testSeed)

	reversed := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		reversed[len(ids)-1-i] = id
	}
	got := ExactSample(reversed, 500, testSeed)

	if len(got) != len(want) {
		t.Fatalf("uzunluk %d ≠ %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d. olay ayrıştı: %s ≠ %s", i, got[i], want[i])
		}
	}
}

// TestExactSample_Deterministic, aynı tohumun aynı örneklemi verdiğini
// doğrular (K10) ve farklı tohumun farklı örneklem verdiğini gösterir.
func TestExactSample_Deterministic(t *testing.T) {
	ids := eventIDs(5000)

	first := ExactSample(ids, 400, testSeed)
	for i := 0; i < 3; i++ {
		got := ExactSample(ids, 400, testSeed)
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("iter %d: %d. olay ayrıştı", i, j)
			}
		}
	}

	other := ExactSample(ids, 400, testSeed+1)
	identical := true
	for j := range first {
		if other[j] != first[j] {
			identical = false
			break
		}
	}
	if identical {
		t.Error("farklı tohum aynı örneklemi verdi")
	}
}

// TestExactSample_Empty, sınır durumlarını doğrular.
func TestExactSample_Empty(t *testing.T) {
	if got := ExactSample(nil, 10, testSeed); got != nil {
		t.Errorf("boş girdide %d olay döndü", len(got))
	}
	if got := ExactSample(eventIDs(10), 0, testSeed); got != nil {
		t.Errorf("hedef 0 iken %d olay döndü", len(got))
	}
}

// TestFilterPartition, bölüm süzgecinin pkg/split ile aynı sonucu verdiğini
// doğrular.
func TestFilterPartition(t *testing.T) {
	p := mustPolicy(t, Config{Mode: ModeValidation, Seed: testSeed, SplitRatio: testRatio})
	splitter, err := split.New(testSeed, testRatio)
	if err != nil {
		t.Fatalf("split.New: %v", err)
	}

	ids := eventIDs(5000)
	calib := p.FilterPartition(ids, byte(split.Calibration))
	valid := p.FilterPartition(ids, byte(split.Validation))

	if len(calib)+len(valid) != len(ids) {
		t.Errorf("bölümler toplamı %d, %d beklenir", len(calib)+len(valid), len(ids))
	}
	for _, id := range calib {
		if splitter.Of(id) != split.Calibration {
			t.Fatalf("olay %s 'C' kümesinde değil", id)
		}
	}
	for _, id := range valid {
		if splitter.Of(id) != split.Validation {
			t.Fatalf("olay %s 'V' kümesinde değil", id)
		}
	}
}

// TestPBT_SamplingInvariants, örnekleme değişmezlerini rastgele
// yapılandırmalarda sınar (ADR-11).
//
//	#1 C ∩ V = ∅            (K4'ün temeli)
//	#2 tam mod her olayı içerir
//	#3 kalibrasyon örneklemi 'C' kümesinin alt kümesidir
//	#4 karar deterministiktir
func TestPBT_SamplingInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seed := rapid.Int64Range(-1000, 1000).Draw(t, "seed")
		ratio := rapid.Float64Range(0.5, 0.95).Draw(t, "oran")
		total := rapid.IntRange(100, 5000).Draw(t, "hacim")
		target := rapid.IntRange(1, total).Draw(t, "hedef")

		base := Config{Seed: seed, SplitRatio: ratio}

		calibCfg := base
		calibCfg.Mode = ModeCalibration
		calibCfg.TargetEvents = target
		calibCfg.ExpectedTotalEvents = total

		validCfg := base
		validCfg.Mode = ModeValidation

		fullCfg := base
		fullCfg.Mode = ModeFull

		calib, err := New(calibCfg)
		if err != nil {
			t.Fatalf("kalibrasyon politikası: %v", err)
		}
		valid, err := New(validCfg)
		if err != nil {
			t.Fatalf("doğrulama politikası: %v", err)
		}
		full, err := New(fullCfg)
		if err != nil {
			t.Fatalf("tam politika: %v", err)
		}

		splitter, err := split.New(seed, ratio)
		if err != nil {
			t.Fatalf("split.New: %v", err)
		}

		for _, id := range eventIDs(total) {
			inC := calib.Includes(id)
			inV := valid.Includes(id)

			if inC && inV {
				t.Fatalf("#1 ihlal: olay %s iki kümede de var", id)
			}
			if !full.Includes(id) {
				t.Fatalf("#2 ihlal: tam modda olay %s elendi", id)
			}
			if inC && splitter.Of(id) != split.Calibration {
				t.Fatalf("#3 ihlal: örneklem olayı %s 'C' kümesinde değil", id)
			}
			if calib.Includes(id) != inC || valid.Includes(id) != inV {
				t.Fatalf("#4 ihlal: olay %s kararı ikinci çağrıda değişti", id)
			}
		}
	})
}
