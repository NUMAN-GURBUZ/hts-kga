package detector

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// stubPositions, sabit hücre konumlarıdır (ENU metre).
type stubPositions struct{ pos map[uuid.UUID]geo.Point }

func (s stubPositions) Position(id uuid.UUID) (geo.Point, bool) {
	p, ok := s.pos[id]
	return p, ok
}

// chainBuilder, konumlu bir abone dizisi kurar.
type chainBuilder struct {
	t     *testing.T
	pos   map[uuid.UUID]geo.Point
	seq   Sequence
	clock time.Duration
}

func newChain(t *testing.T) *chainBuilder {
	t.Helper()
	return &chainBuilder{t: t, pos: make(map[uuid.UUID]geo.Point), seq: Sequence{Subscriber: "abone-1"}}
}

// at, verilen ENU konumunda ve verilen süre sonra bir kayıt ekler.
func (c *chainBuilder) at(x, y float64, after time.Duration) *chainBuilder {
	c.clock += after
	cellID := uuid.New()
	c.pos[cellID] = geo.Point{X: x, Y: y}

	rec := record(c.t, "abone-1", c.clock)
	rec.CellID = cellID
	c.seq.Records = append(c.seq.Records, rec)
	return c
}

// unknownAt, envanterde olmayan bir hücreye işaret eden kayıt ekler (kural 1).
func (c *chainBuilder) unknownAt(after time.Duration) *chainBuilder {
	c.clock += after
	rec := record(c.t, "abone-1", c.clock)
	rec.CellID = uuid.New() // konum haritasına eklenmez
	c.seq.Records = append(c.seq.Records, rec)
	return c
}

func (c *chainBuilder) rule(maxKMH float64) *VelocityRule {
	c.t.Helper()
	r, err := NewVelocityRule(stubPositions{pos: c.pos}, maxKMH, DefaultMarginCap)
	if err != nil {
		c.t.Fatalf("NewVelocityRule: %v", err)
	}
	return r
}

// eventAt, dizideki n. kaydın olay kimliğidir.
func (c *chainBuilder) eventAt(n int) uuid.UUID { return c.seq.Records[n].EventID }

// ─── Kurulum ──────────────────────────────────────────────────────────────────

func TestVelocityRule_RejectsBadSetup(t *testing.T) {
	if _, err := NewVelocityRule(nil, 300, 0); err == nil {
		t.Error("konum kaynağı olmadan kurulmamalı")
	}
	if _, err := NewVelocityRule(stubPositions{}, 0, 0); err == nil {
		t.Error("hız üst sınırı 0 reddedilmeliydi")
	}
	if _, err := NewVelocityRule(stubPositions{}, -5, 0); err == nil {
		t.Error("negatif hız üst sınırı reddedilmeliydi")
	}
}

// ─── Temel davranış ───────────────────────────────────────────────────────────

// TestVelocityRule_PlausibleMovementProducesNoHits, makul hareketin bulgu
// üretmediğini sınar.
//
// Kentsel senaryoda tipik durum: 2,4 saatlik aralıkta 10 km. Ortalama 4,2 km/h —
// 300 km/h eşiğinin çok altında. ADR-29/2'nin ölçtüğü fiziksel sınır tam bu.
func TestVelocityRule_PlausibleMovementProducesNoHits(t *testing.T) {
	c := newChain(t).
		at(0, 0, 0).
		at(10_000, 0, 144*time.Minute). // 10 km / 2,4 sa = 4,2 km/h
		at(0, 0, 144*time.Minute)

	if hits := c.rule(300).Evaluate(c.seq); len(hits) != 0 {
		t.Errorf("makul hareket bulgu üretti: %+v", hits)
	}
}

// TestVelocityRule_JumpAttributedToSingleRecord, atlamanın **tek** kayda
// atfedildiğini sınar.
//
// Bu testin kapattığı hata precision'ı %36'ya indiren hatadır: ihlal çiftin
// özelliğidir ve kayıt bazlı bir kural iki ucu da işaretler.
func TestVelocityRule_JumpAttributedToSingleRecord(t *testing.T) {
	// Gerçek yörünge: origin çevresi. 2. kayıt 40 km öteye atlatılmış.
	c := newChain(t).
		at(0, 0, 0).                  // 0: gerçek
		at(100, 0, 5*time.Minute).    // 1: gerçek
		at(40_000, 0, 5*time.Minute). // 2: ATLAMA (40 km / 5 dk = 480 km/h)
		at(200, 0, 5*time.Minute).    // 3: gerçek
		at(300, 0, 5*time.Minute)     // 4: gerçek

	hits := c.rule(300).Evaluate(c.seq)
	if len(hits) != 1 {
		t.Fatalf("%d bulgu, beklenen 1 (bir geçiş → en çok bir bulgu, ADR-28/3)", len(hits))
	}

	h := hits[0]
	if h.EventID != c.eventAt(2) {
		t.Errorf("bulgu yanlış kayda atfedildi: index %d beklendi", 2)
	}
	if h.Rule != integrityrule.Velocity {
		t.Errorf("kural %s", h.Rule)
	}
	// margin = 480 / 300 = 1,6
	if h.Margin < 1.5 || h.Margin > 1.7 {
		t.Errorf("margin = %g, beklenen ~1,6", h.Margin)
	}
	if h.Evidence["attribution"] != "local_support" {
		t.Errorf("kanıtta atıf yöntemi yok: %v", h.Evidence["attribution"])
	}
	if h.Evidence["threshold_kmh"] != 300.0 {
		t.Errorf("kanıtta eşik yanlış: %v", h.Evidence["threshold_kmh"])
	}
	if h.Evidence["zero_interval"] != false {
		t.Errorf("zero_interval yanlış: %v", h.Evidence["zero_interval"])
	}
}

// TestVelocityRule_ZeroIntervalIsImpossible, aynı damgada farklı konumun
// yakalandığını sınar.
//
// Kentsel koşuda >300 km/h isabetlerin **tamamı** bu popülasyondadır (ADR-29/3).
// `margin` sonsuz olacağından kapılır; `+Inf` koruması olmadan json.Marshal hata
// verir ve kural kentselde sıfır bulgu üretir.
func TestVelocityRule_ZeroIntervalIsImpossible(t *testing.T) {
	c := newChain(t).
		at(0, 0, 0).
		at(100, 0, 5*time.Minute).
		at(10_000, 0, 0). // aynı damga, 10 km öteye
		at(200, 0, 5*time.Minute).
		at(300, 0, 5*time.Minute)

	hits := c.rule(300).Evaluate(c.seq)
	if len(hits) != 1 {
		t.Fatalf("%d bulgu, beklenen 1", len(hits))
	}

	h := hits[0]
	if h.EventID != c.eventAt(2) {
		t.Error("bulgu yanlış kayda atfedildi")
	}
	if h.Evidence["zero_interval"] != true {
		t.Errorf("zero_interval işaretlenmedi: %v", h.Evidence["zero_interval"])
	}
	if h.Evidence["capped"] != true {
		t.Errorf("kapılma işaretlenmedi: %v", h.Evidence["capped"])
	}
	if math.IsInf(h.Margin, 0) || math.IsNaN(h.Margin) {
		t.Errorf("margin sonlu olmalı: %g", h.Margin)
	}
}

// TestVelocityRule_SameTickSameCellIsNotViolation, tick-içi eşit damgaların
// aynı hücrede ihlal sayılmadığını sınar.
//
// Temiz kayıtlarda tick-içi olaylar aynı gözlemden gelir → mesafe 0. İhlal
// sayılsaydı Sprint 5 verisinde ~%1,7 olay (≈5.000 kayıt) bulgu üretirdi.
func TestVelocityRule_SameTickSameCellIsNotViolation(t *testing.T) {
	cellID := uuid.New()
	pos := map[uuid.UUID]geo.Point{cellID: {X: 0, Y: 0}}

	seq := Sequence{Subscriber: "abone-1"}
	for i := 0; i < 3; i++ {
		rec := record(t, "abone-1", time.Hour) // hepsi aynı damga
		rec.CellID = cellID
		seq.Records = append(seq.Records, rec)
	}

	rule, err := NewVelocityRule(stubPositions{pos: pos}, 300, DefaultMarginCap)
	if err != nil {
		t.Fatalf("NewVelocityRule: %v", err)
	}
	if hits := rule.Evaluate(seq); len(hits) != 0 {
		t.Errorf("aynı tick aynı hücre bulgu üretti: %+v", hits)
	}
}

// ─── Karar verilemezlik (ADR-29/6) ────────────────────────────────────────────

// TestVelocityRule_UndecidableProducesNoHit, dış komşu yoksa bulgu
// üretilmediğini sınar.
//
// Zincir yalnızca iki kayıttan oluşuyorsa hangi ucun suçlu olduğu bilinemez.
// Adli bir sistem iki tarafı birlikte suçlamaz.
func TestVelocityRule_UndecidableProducesNoHit(t *testing.T) {
	c := newChain(t).
		at(0, 0, 0).
		at(40_000, 0, 5*time.Minute) // ihlal, ama dış komşu yok

	if hits := c.rule(300).Evaluate(c.seq); len(hits) != 0 {
		t.Errorf("karar verilemez durumda bulgu üretildi: %+v", hits)
	}
}

// TestVelocityRule_SymmetricCaseProducesNoHit, mesafeler eşit olduğunda karar
// verilmediğini sınar.
//
// Karşılaştırma katı eşitsizliktir: kanıt tek bir kaydı göstermiyorsa suçlama
// yapılmaz.
func TestVelocityRule_SymmetricCaseProducesNoHit(t *testing.T) {
	// p2 = (0,0). A = (-20000, 0), B = (20000, 0): p2'ye mesafeleri eşit.
	// A→B geçişi 40 km / 5 dk = 480 km/h → ihlal.
	// n2 de simetrik konumlandırılır ki giden kenar da karar vermesin.
	c := newChain(t).
		at(0, 0, 0).                    // 0: p2
		at(-20_000, 0, 60*time.Minute). // 1: A
		at(20_000, 0, 5*time.Minute).   // 2: B — ihlal
		at(0, 0, 60*time.Minute)        // 3: n2, A ve B'ye eşit mesafede

	if hits := c.rule(300).Evaluate(c.seq); len(hits) != 0 {
		t.Errorf("simetrik durumda bulgu üretildi: %+v", hits)
	}
}

// ─── Kural 1 kayıtlarının dışlanması (ADR-29/4) ────────────────────────────────

// TestVelocityRule_UnknownCellsExcludedFromChain, envanterde olmayan hücreye
// işaret eden kayıtların zincirden çıkarıldığını sınar.
//
// Zincire NULL konumla girerlerse ardışık çiftler kırılır ve komşu geçişler
// yanlış hesaplanır. O kayıt zaten kural 1 tarafından talep edilmiştir.
func TestVelocityRule_UnknownCellsExcludedFromChain(t *testing.T) {
	c := newChain(t).
		at(0, 0, 0).
		at(100, 0, 5*time.Minute).
		unknownAt(5*time.Minute). // kural 1 kaydı — atlanmalı
		at(200, 0, 5*time.Minute).
		at(300, 0, 5*time.Minute)

	hits := c.rule(300).Evaluate(c.seq)
	if len(hits) != 0 {
		t.Errorf("kural 1 kaydı hız zincirini bozdu: %+v", hits)
	}

	// Bilinmeyen kayıt hiçbir bulguya çıpalanmamalı.
	unknownID := c.eventAt(2)
	for _, h := range hits {
		if h.EventID == unknownID {
			t.Error("bilinmeyen konumlu kayda hız bulgusu yazıldı")
		}
	}
}

// ─── Sınır davranışı ──────────────────────────────────────────────────────────

// TestVelocityRule_ThresholdBoundary, eşik sınırında davranışı sınar.
func TestVelocityRule_ThresholdBoundary(t *testing.T) {
	tests := []struct {
		name     string
		distance float64
		wantHit  bool
	}{
		// 5 dakikada: 300 km/h = 25.000 m
		{"eşiğin altında (299 km/h)", 24_917, false},
		{"tam eşikte (300 km/h)", 25_000, false}, // katı büyüktür
		{"eşiğin üstünde (301 km/h)", 25_084, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newChain(t).
				at(0, 0, 0).
				at(0, 0, 60*time.Minute).
				at(tc.distance, 0, 5*time.Minute).
				at(0, 0, 60*time.Minute).
				at(0, 0, 60*time.Minute)

			hits := c.rule(300).Evaluate(c.seq)
			if got := len(hits) > 0; got != tc.wantHit {
				t.Errorf("bulgu=%v, beklenen %v (mesafe %g m / 5 dk)", got, tc.wantHit, tc.distance)
			}
		})
	}
}

// TestVelocityRule_SingleRecordProducesNoHit, tek kayıtlı abonede geçiş
// olmadığını sınar.
func TestVelocityRule_SingleRecordProducesNoHit(t *testing.T) {
	c := newChain(t).at(0, 0, 0)
	if hits := c.rule(300).Evaluate(c.seq); len(hits) != 0 {
		t.Errorf("tek kayıtta bulgu üretildi: %+v", hits)
	}
}

// TestVelocityRule_OneFindingPerRecordEvenWithTwoViolations, atlanan kaydın
// hem gelen hem giden kenarında ihlal olsa da tek bulgu üretildiğini sınar.
//
// Tekillik kısıtı (run_id, event_id, rule_id) zaten ikinci satırı reddederdi;
// ama motorun sayaçları iki isabet görür ve kanonik/bastırılmış ayrımı bozulurdu.
func TestVelocityRule_OneFindingPerRecordEvenWithTwoViolations(t *testing.T) {
	c := newChain(t).
		at(0, 0, 0).
		at(100, 0, 5*time.Minute).
		at(40_000, 0, 5*time.Minute). // gelen VE giden kenar ihlal
		at(200, 0, 5*time.Minute).
		at(300, 0, 5*time.Minute)

	hits := c.rule(300).Evaluate(c.seq)
	if len(hits) != 1 {
		t.Fatalf("%d bulgu, beklenen 1", len(hits))
	}
	// Atfedilen hız iki ihlalin en yükseği olmalı.
	if hits[0].Margin < 1.5 {
		t.Errorf("margin = %g, en yüksek ihlal atfedilmeliydi", hits[0].Margin)
	}
}

// ─── PBT ──────────────────────────────────────────────────────────────────────

// TestPBT_VelocityRuleAtMostOneFindingPerTransition, PBT #15: hiçbir girdide
// bulgu sayısı ihlal eden geçiş sayısını aşamaz ve aynı kayda iki bulgu
// yazılamaz (ADR-28/3).
//
// Bu, precision'ı %50'nin altına indiren yapısal hatanın nüksetmesini yasaklar:
// naif kural her ihlalde **iki** bulgu üretir (çiftin iki ucu), atıflı kural en
// çok bir.
//
// # Neden "komşu iki kayıt asla birlikte işaretlenmez" demiyoruz
//
// İlk yazımda değişmez böyle kurulmuştu ve PBT onu çürüttü — haklı olarak.
// Ardışık iki geçiş (i−1, i) ve (i, i+1) ayrı ayrı ihlal edip sırasıyla i ve
// i+1'i suçlayabilir; o zaman komşu iki kayıt işaretlenir ama **her geçiş yine
// tek bulgu** üretmiştir. Gerçek değişmez geçiş başınadır, kayıt komşuluğu
// başına değil.
func TestPBT_VelocityRuleAtMostOneFindingPerTransition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 12).Draw(t, "kayıt_sayısı")

		pos := make(map[uuid.UUID]geo.Point, n)
		seq := Sequence{Subscriber: "a"}
		var clock time.Duration
		index := make(map[uuid.UUID]int, n)

		for i := 0; i < n; i++ {
			x := rapid.Float64Range(-50_000, 50_000).Draw(t, "x")
			y := rapid.Float64Range(-50_000, 50_000).Draw(t, "y")
			gap := rapid.IntRange(0, 24).Draw(t, "gap_tick")
			clock += time.Duration(gap) * 5 * time.Minute

			cellID := uuid.New()
			pos[cellID] = geo.Point{X: x, Y: y}
			rec := Record{
				RunID: uuid.New(), EventID: uuid.New(), Time: baseTime.Add(clock),
				Subscriber: "a", CellID: cellID, Scenario: "A",
			}
			index[rec.EventID] = i
			seq.Records = append(seq.Records, rec)
		}

		rule, err := NewVelocityRule(stubPositions{pos: pos}, 300, DefaultMarginCap)
		if err != nil {
			t.Fatalf("NewVelocityRule: %v", err)
		}
		hits := rule.Evaluate(seq)

		flagged := make(map[int]bool, len(hits))
		for _, h := range hits {
			i, ok := index[h.EventID]
			if !ok {
				t.Fatalf("bulgu dizide olmayan bir olaya çıpalandı: %s", h.EventID)
			}
			if flagged[i] {
				t.Fatalf("aynı kayda iki bulgu yazıldı (index %d)", i)
			}
			flagged[i] = true

			if math.IsNaN(h.Margin) || math.IsInf(h.Margin, 0) {
				t.Fatalf("margin sonlu değil: %g", h.Margin)
			}
			if h.Margin < 1.0 {
				t.Fatalf("margin %g < 1.0 — eşik aşılmadan bulgu yazılmış", h.Margin)
			}
		}

		// Bulgu sayısı ihlal eden geçiş sayısını aşamaz (ADR-28/3).
		// Naif kural bunun iki katını üretirdi.
		violations := 0
		for i := 0; i+1 < len(seq.Records); i++ {
			a, b := seq.Records[i], seq.Records[i+1]
			pa, okA := pos[a.CellID]
			pb, okB := pos[b.CellID]
			if !okA || !okB {
				continue
			}
			v, _ := impliedVelocityKMH(geo.Distance(pa, pb), b.Time.Sub(a.Time).Seconds())
			if v > 300 {
				violations++
			}
		}
		if len(hits) > violations {
			t.Fatalf("%d bulgu, yalnızca %d ihlal eden geçiş var (ADR-28/3)",
				len(hits), violations)
		}
	})
}

// TestPBT_VelocityRuleSilentBelowThreshold, eşiğin altındaki tüm geçişlerde
// bulgu üretilmediğini sınar.
func TestPBT_VelocityRuleSilentBelowThreshold(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 10).Draw(t, "kayıt_sayısı")
		const maxKMH = 300.0

		pos := make(map[uuid.UUID]geo.Point, n)
		seq := Sequence{Subscriber: "a"}
		var clock time.Duration
		x := 0.0

		for i := 0; i < n; i++ {
			// Her adım en az 1 tick (5 dk) ve en çok 20 km: 240 km/h < 300.
			gap := rapid.IntRange(1, 12).Draw(t, "gap_tick")
			step := rapid.Float64Range(0, 20_000).Draw(t, "step_m")
			clock += time.Duration(gap) * 5 * time.Minute
			x += step

			cellID := uuid.New()
			pos[cellID] = geo.Point{X: x, Y: 0}
			seq.Records = append(seq.Records, Record{
				RunID: uuid.New(), EventID: uuid.New(), Time: baseTime.Add(clock),
				Subscriber: "a", CellID: cellID, Scenario: "A",
			})
		}

		rule, err := NewVelocityRule(stubPositions{pos: pos}, maxKMH, DefaultMarginCap)
		if err != nil {
			t.Fatalf("NewVelocityRule: %v", err)
		}
		if hits := rule.Evaluate(seq); len(hits) != 0 {
			t.Fatalf("eşiğin altındaki harekette %d bulgu üretildi", len(hits))
		}
	})
}

// TestImpliedVelocityKMH, hız hesabının sınır durumlarını sınar.
func TestImpliedVelocityKMH(t *testing.T) {
	tests := []struct {
		name              string
		distanceM, deltaS float64
		wantKMH           float64
		wantZero          bool
	}{
		{"5 dakikada 25 km", 25_000, 300, 300, false},
		{"1 saatte 100 km", 100_000, 3600, 100, false},
		{"Δt=0, hareket var", 10_000, 0, math.Inf(1), true},
		{"Δt=0, hareket yok", 0, 0, 0, true},
		{"Δt=0, gürültü içinde", 0.5, 0, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kmh, zero := impliedVelocityKMH(tc.distanceM, tc.deltaS)
			if zero != tc.wantZero {
				t.Errorf("zeroDelta = %v, beklenen %v", zero, tc.wantZero)
			}
			if math.IsInf(tc.wantKMH, 1) {
				if !math.IsInf(kmh, 1) {
					t.Errorf("hız = %g, beklenen +Inf", kmh)
				}
				return
			}
			if math.Abs(kmh-tc.wantKMH) > 0.01 {
				t.Errorf("hız = %g, beklenen %g", kmh, tc.wantKMH)
			}
		})
	}
}
