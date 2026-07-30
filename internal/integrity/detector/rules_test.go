package detector

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// ─── Kural 1: envanter ────────────────────────────────────────────────────────

// stubCellSet, sabit bir kimlik kümesidir.
type stubCellSet struct{ known map[uuid.UUID]bool }

func (s stubCellSet) Has(id uuid.UUID) bool { return s.known[id] }
func (s stubCellSet) Len() int              { return len(s.known) }

func TestInventoryRule_RejectsEmptyInventory(t *testing.T) {
	if _, err := NewInventoryRule(stubCellSet{known: map[uuid.UUID]bool{}}, 0); err == nil {
		t.Error("boş envanterle kural kurulmamalı — her kayıt bulgu sayılırdı")
	}
	if _, err := NewInventoryRule(nil, 0); err == nil {
		t.Error("envantersiz kural kurulmamalı")
	}
}

// TestInventoryRule_HitOnlyForUnknownCell, kuralın tam karakterizasyonunu sınar.
func TestInventoryRule_HitOnlyForUnknownCell(t *testing.T) {
	known := uuid.New()
	rule, err := NewInventoryRule(stubCellSet{known: map[uuid.UUID]bool{known: true}}, DefaultMarginCap)
	if err != nil {
		t.Fatalf("NewInventoryRule: %v", err)
	}

	rec := record(t, "abone-1", 0)
	rec.CellID = known
	if hits := rule.Observe(rec); len(hits) != 0 {
		t.Errorf("bilinen hücre bulgu üretti: %+v", hits)
	}

	rec.CellID = uuid.New() // envanterde yok
	hits := rule.Observe(rec)
	if len(hits) != 1 {
		t.Fatalf("%d bulgu, beklenen 1", len(hits))
	}

	h := hits[0]
	if h.Rule != integrityrule.Inventory {
		t.Errorf("kural %s", h.Rule)
	}
	if h.EventID != rec.EventID || !h.Time.Equal(rec.Time) || h.Scenario != rec.Scenario {
		t.Error("isabet kaydın alanlarını taşımıyor")
	}
	if h.Margin != 1.0 {
		t.Errorf("margin = %g, beklenen 1.0 (ADR-31/4: eşik yok, ikili karar)", h.Margin)
	}
	if h.Evidence["cell_id"] != rec.CellID.String() {
		t.Errorf("kanıtta cell_id yok veya yanlış: %v", h.Evidence["cell_id"])
	}
	if h.Evidence["inventory_size"] != 1 {
		t.Errorf("kanıtta inventory_size = %v, beklenen 1", h.Evidence["inventory_size"])
	}
}

// TestPBT_InventoryRuleIsExactCharacterisation, PBT #13:
// `cell_id ∈ envanter ⇔ bulgu yok`. Hem yanlış pozitifi hem yanlış negatifi
// aynı anda yasaklar.
func TestPBT_InventoryRuleIsExactCharacterisation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "envanter_boyutu")
		known := make(map[uuid.UUID]bool, n)
		ids := make([]uuid.UUID, 0, n)
		for i := 0; i < n; i++ {
			id := uuid.New()
			known[id] = true
			ids = append(ids, id)
		}

		rule, err := NewInventoryRule(stubCellSet{known: known}, DefaultMarginCap)
		if err != nil {
			t.Fatalf("NewInventoryRule: %v", err)
		}

		var cellID uuid.UUID
		inInventory := rapid.Bool().Draw(t, "envanterde")
		if inInventory {
			cellID = ids[rapid.IntRange(0, n-1).Draw(t, "hangi")]
		} else {
			cellID = uuid.New()
		}

		rec := Record{
			RunID: uuid.New(), EventID: uuid.New(), Time: baseTime,
			Subscriber: "a", CellID: cellID, Scenario: "A",
		}
		hits := rule.Observe(rec)

		if inInventory && len(hits) != 0 {
			t.Fatalf("envanterdeki hücre bulgu üretti")
		}
		if !inInventory && len(hits) != 1 {
			t.Fatalf("envanterde olmayan hücre %d bulgu üretti, beklenen 1", len(hits))
		}
	})
}

// ─── Kural 3: zaman ───────────────────────────────────────────────────────────

const testTick = 5 * time.Minute

func newTimeRule(t *testing.T, tolerance time.Duration) *TimeOrderRule {
	t.Helper()
	r, err := NewTimeOrderRule(tolerance, testTick, DefaultMarginCap)
	if err != nil {
		t.Fatalf("NewTimeOrderRule: %v", err)
	}
	return r
}

func TestTimeOrderRule_RejectsBadSetup(t *testing.T) {
	if _, err := NewTimeOrderRule(-time.Second, testTick, 0); err == nil {
		t.Error("negatif tolerans reddedilmeliydi")
	}
	if _, err := NewTimeOrderRule(0, 0, 0); err == nil {
		t.Error("sıfır tick uzunluğu reddedilmeliydi")
	}
}

// TestTimeOrderRule_MonotoneStreamProducesNoHits, artan akışta bulgu
// üretilmediğini sınar.
func TestTimeOrderRule_MonotoneStreamProducesNoHits(t *testing.T) {
	rule := newTimeRule(t, 0)

	for i := 0; i < 10; i++ {
		rec := record(t, "abone-1", time.Duration(i)*time.Hour)
		if hits := rule.Observe(rec); len(hits) != 0 {
			t.Fatalf("kayıt %d bulgu üretti: %+v", i, hits)
		}
	}
}

// TestTimeOrderRule_EqualTimestampsAreNotViolations, tick-içi eşit damgaların
// ihlal sayılmadığını sınar.
//
// Eşitlik ihlal sayılsaydı her tick-içi ikinci olay bulgu üretirdi: Sprint 5
// verisinde ~%1,7 olay bir başka olayla aynı tick'i paylaşıyor, yani ~5.000
// yanlış pozitif.
func TestTimeOrderRule_EqualTimestampsAreNotViolations(t *testing.T) {
	rule := newTimeRule(t, 0)

	first := record(t, "abone-1", time.Hour)
	second := record(t, "abone-1", time.Hour) // aynı tick, aynı damga

	if hits := rule.Observe(first); len(hits) != 0 {
		t.Fatalf("ilk kayıt bulgu üretti")
	}
	if hits := rule.Observe(second); len(hits) != 0 {
		t.Errorf("eşit damga ihlal sayıldı: %+v", hits)
	}
}

// TestTimeOrderRule_BackstepDetected, geriye kaydırılmış damganın yakalandığını
// ve kanıt/margin değerlerinin doğru olduğunu sınar.
func TestTimeOrderRule_BackstepDetected(t *testing.T) {
	rule := newTimeRule(t, 0)

	earlier := record(t, "abone-1", 3*time.Hour)
	if hits := rule.Observe(earlier); len(hits) != 0 {
		t.Fatalf("ilk kayıt bulgu üretti")
	}

	// Enjektörün kaydırması: −2 saat.
	shifted := record(t, "abone-1", time.Hour)
	hits := rule.Observe(shifted)
	if len(hits) != 1 {
		t.Fatalf("%d bulgu, beklenen 1", len(hits))
	}

	h := hits[0]
	if h.Rule != integrityrule.TimeOrder {
		t.Errorf("kural %s", h.Rule)
	}
	if h.EventID != shifted.EventID {
		t.Error("bulgu kaydırılmış kayda çıpalanmalı")
	}
	// margin = geri_gidiş / tick = 7200 / 300 = 24
	if h.Margin != 24.0 {
		t.Errorf("margin = %g, beklenen 24.0 (7200s / 300s)", h.Margin)
	}
	if got := h.Evidence["backstep_s"]; got != 7200.0 {
		t.Errorf("backstep_s = %v, beklenen 7200", got)
	}
	if got := h.Evidence["prev_event_id"]; got != earlier.EventID.String() {
		t.Errorf("prev_event_id = %v", got)
	}
}

// TestTimeOrderRule_WatermarkNotLoweredByViolation, ihlalin su işaretini geriye
// çekmediğini sınar.
//
// Çekseydi kaydırılmış kaydın ardından gelen temiz kayıtlar da ihlal görünmez
// hâle gelir ve bir sonraki kaydırma tespit edilemezdi.
func TestTimeOrderRule_WatermarkNotLoweredByViolation(t *testing.T) {
	rule := newTimeRule(t, 0)

	rule.Observe(record(t, "abone-1", 5*time.Hour))           // su işareti = 5sa
	rule.Observe(record(t, "abone-1", 1*time.Hour))           // ihlal
	second := rule.Observe(record(t, "abone-1", 2*time.Hour)) // hâlâ ihlal (5sa'ya göre)

	if len(second) != 1 {
		t.Errorf("ikinci kaydırma da ihlal olmalı: %d bulgu", len(second))
	}
}

// TestTimeOrderRule_SubscribersAreIndependent, abonelerin su işaretlerinin
// karışmadığını sınar.
func TestTimeOrderRule_SubscribersAreIndependent(t *testing.T) {
	rule := newTimeRule(t, 0)

	rule.Observe(record(t, "abone-1", 5*time.Hour))
	if hits := rule.Observe(record(t, "abone-2", time.Hour)); len(hits) != 0 {
		t.Errorf("başka abonenin su işareti kullanıldı: %+v", hits)
	}
	if rule.Subscribers() != 2 {
		t.Errorf("izlenen abone %d, beklenen 2", rule.Subscribers())
	}
}

// TestTimeOrderRule_ToleranceHonoured, toleransın sınır davranışını sınar.
func TestTimeOrderRule_ToleranceHonoured(t *testing.T) {
	rule := newTimeRule(t, 10*time.Second)

	rule.Observe(record(t, "abone-1", time.Hour))

	// 10 saniye geri: tolerans dâhilinde (<=).
	if hits := rule.Observe(record(t, "abone-1", time.Hour-10*time.Second)); len(hits) != 0 {
		t.Errorf("tolerans içindeki geri gidiş ihlal sayıldı: %+v", hits)
	}
	// 11 saniye geri: ihlal.
	if hits := rule.Observe(record(t, "abone-1", time.Hour-11*time.Second)); len(hits) != 1 {
		t.Errorf("tolerans dışı geri gidiş yakalanmadı: %d bulgu", len(hits))
	}
}

// TestTimeOrderRule_EmptySubscriberIgnored, takma adı olmayan kaydın bulgu
// üretmediğini sınar.
func TestTimeOrderRule_EmptySubscriberIgnored(t *testing.T) {
	rule := newTimeRule(t, 0)
	rec := record(t, "", 0)
	if hits := rule.Observe(rec); len(hits) != 0 {
		t.Errorf("takma adsız kayıt bulgu üretti: %+v", hits)
	}
}

// TestPBT_TimeOrderRuleSilentOnMonotoneStream, PBT #14: zamanda monoton artan
// akışta kural 3 hiç bulgu üretmez.
func TestPBT_TimeOrderRuleSilentOnMonotoneStream(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rule, err := NewTimeOrderRule(0, testTick, DefaultMarginCap)
		if err != nil {
			t.Fatalf("NewTimeOrderRule: %v", err)
		}

		subscribers := rapid.IntRange(1, 4).Draw(t, "abone_sayısı")
		steps := rapid.IntRange(1, 30).Draw(t, "kayıt_sayısı")

		// Abone başına monoton artan (eşitliğe izin veren) damgalar.
		offsets := make([]time.Duration, subscribers)
		for i := 0; i < steps; i++ {
			s := rapid.IntRange(0, subscribers-1).Draw(t, "abone")
			// 0 dâhil: tick-içi eşit damga durumu da örneklenir.
			delta := rapid.IntRange(0, 120).Draw(t, "delta_dk")
			offsets[s] += time.Duration(delta) * time.Minute

			rec := Record{
				RunID: uuid.New(), EventID: uuid.New(),
				Time:       baseTime.Add(offsets[s]),
				Subscriber: string(rune('a' + s)),
				Scenario:   "A",
			}
			if hits := rule.Observe(rec); len(hits) != 0 {
				t.Fatalf("monoton akışta bulgu üretildi (abone %d, adım %d): %+v", s, i, hits)
			}
		}
	})
}

// ─── Kural 5: aktivite ────────────────────────────────────────────────────────

func newActivityRule(t *testing.T, minSupport int) *ActivityRule {
	t.Helper()
	r, err := NewActivityRule(minSupport, DefaultMarginCap)
	if err != nil {
		t.Fatalf("NewActivityRule: %v", err)
	}
	return r
}

func TestActivityRule_RejectsBadSetup(t *testing.T) {
	if _, err := NewActivityRule(0, 0); err == nil {
		t.Error("asgari destek 0 reddedilmeliydi")
	}
}

// sequence, verilen cihaz kimlikleriyle bir abone dizisi kurar.
func sequence(t *testing.T, subscriber string, devices ...string) Sequence {
	t.Helper()
	seq := Sequence{Subscriber: subscriber}
	for i, d := range devices {
		rec := record(t, subscriber, time.Duration(i)*time.Hour)
		rec.Device = d
		seq.Records = append(seq.Records, rec)
	}
	return seq
}

// TestActivityRule_SingleDeviceProducesNoHits, tek cihazlı abonede bulgu
// üretilmediğini sınar.
func TestActivityRule_SingleDeviceProducesNoHits(t *testing.T) {
	rule := newActivityRule(t, 2)
	seq := sequence(t, "abone-1", "imei-A", "imei-A", "imei-A", "imei-A")

	if hits := rule.Evaluate(seq); len(hits) != 0 {
		t.Errorf("tek cihazlı abone bulgu üretti: %+v", hits)
	}
}

// TestActivityRule_MinorityDeviceFlagged, azınlık cihazın işaretlendiğini ve
// modal cihazın işaretlenmediğini sınar.
func TestActivityRule_MinorityDeviceFlagged(t *testing.T) {
	rule := newActivityRule(t, 2)
	seq := sequence(t, "abone-1", "imei-A", "imei-A", "imei-B", "imei-A", "imei-A")

	hits := rule.Evaluate(seq)
	if len(hits) != 1 {
		t.Fatalf("%d bulgu, beklenen 1", len(hits))
	}

	h := hits[0]
	if h.EventID != seq.Records[2].EventID {
		t.Error("bulgu azınlık kayda çıpalanmalı")
	}
	if h.Rule != integrityrule.Activity {
		t.Errorf("kural %s", h.Rule)
	}
	if h.Margin != 2.0 {
		t.Errorf("margin = %g, beklenen 2.0 (farklı IMEI sayısı)", h.Margin)
	}
	if h.Evidence["imei"] != "imei-B" || h.Evidence["modal_imei"] != "imei-A" {
		t.Errorf("kanıt yanlış: %v", h.Evidence)
	}
	if h.Evidence["support"] != 4 {
		t.Errorf("support = %v, beklenen 4", h.Evidence["support"])
	}
}

// TestActivityRule_InsufficientSupportProducesNoHits, referansın yeterince
// desteklenmediği durumda karar verilmediğini sınar.
//
// Tek kayıtlı bir abonede "modal" cihaz kavramı boştur; hangi kaydın sapma
// olduğu söylenemez. Karar verilemezlik → bulgu yok.
func TestActivityRule_InsufficientSupportProducesNoHits(t *testing.T) {
	rule := newActivityRule(t, 3)
	seq := sequence(t, "abone-1", "imei-A", "imei-B") // her ikisi de 1 destek

	if hits := rule.Evaluate(seq); len(hits) != 0 {
		t.Errorf("desteksiz referansla bulgu üretildi: %+v", hits)
	}
}

// TestActivityRule_EmptyDeviceIgnored, boş IMEI'nin modal hesabına girmediğini
// sınar.
//
// `pseudo_imei` şemada NULL'a izin veriyor. "Cihaz bilinmiyor" ile "cihaz
// değişti" farklı şeylerdir ve ikincisi iddia edilemez.
func TestActivityRule_EmptyDeviceIgnored(t *testing.T) {
	rule := newActivityRule(t, 2)
	seq := sequence(t, "abone-1", "imei-A", "", "imei-A", "")

	if hits := rule.Evaluate(seq); len(hits) != 0 {
		t.Errorf("boş IMEI bulgu üretti: %+v", hits)
	}
}

// TestActivityRule_ModalChoiceIsDeterministic, eşitlik durumunda seçimin
// koşudan koşuya aynı kaldığını sınar (K10).
func TestActivityRule_ModalChoiceIsDeterministic(t *testing.T) {
	rule := newActivityRule(t, 2)
	seq := sequence(t, "abone-1", "imei-B", "imei-B", "imei-A", "imei-A")

	first := rule.Evaluate(seq)
	for i := 0; i < 20; i++ {
		again := rule.Evaluate(seq)
		if len(again) != len(first) {
			t.Fatalf("bulgu sayısı değişti: %d → %d", len(first), len(again))
		}
		for j := range first {
			if again[j].EventID != first[j].EventID {
				t.Fatalf("bulgu %d başka olaya çıpalandı", j)
			}
		}
	}
	// Eşitlikte kimliğe göre küçük olan (imei-A) modal seçilir.
	if len(first) != 2 {
		t.Fatalf("%d bulgu, beklenen 2", len(first))
	}
	for _, h := range first {
		if h.Evidence["modal_imei"] != "imei-A" {
			t.Errorf("eşitlikte modal %v, beklenen imei-A", h.Evidence["modal_imei"])
		}
	}
}

// TestPBT_ActivityRuleNeverFlagsModalDevice, modal cihazın hiçbir girdide
// işaretlenmediğini sınar.
func TestPBT_ActivityRuleNeverFlagsModalDevice(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rule, err := NewActivityRule(1, DefaultMarginCap)
		if err != nil {
			t.Fatalf("NewActivityRule: %v", err)
		}

		n := rapid.IntRange(1, 40).Draw(t, "kayıt_sayısı")
		devices := []string{"imei-A", "imei-B", "imei-C"}

		seq := Sequence{Subscriber: "a"}
		byEvent := make(map[uuid.UUID]string, n)
		for i := 0; i < n; i++ {
			d := devices[rapid.IntRange(0, len(devices)-1).Draw(t, "cihaz")]
			rec := Record{
				RunID: uuid.New(), EventID: uuid.New(),
				Time:       baseTime.Add(time.Duration(i) * time.Minute),
				Subscriber: "a", Device: d, Scenario: "A",
			}
			byEvent[rec.EventID] = d
			seq.Records = append(seq.Records, rec)
		}

		hits := rule.Evaluate(seq)
		if len(hits) == 0 {
			return
		}

		modal, _ := hits[0].Evidence["modal_imei"].(string)
		for _, h := range hits {
			if byEvent[h.EventID] == modal {
				t.Fatalf("modal cihaz (%q) işaretlendi", modal)
			}
			if got, _ := h.Evidence["modal_imei"].(string); got != modal {
				t.Fatalf("modal cihaz bulgular arasında tutarsız: %q vs %q", got, modal)
			}
			if h.Margin < 2.0 {
				t.Fatalf("margin %g < 2.0 (en az iki cihaz olmalı)", h.Margin)
			}
		}
	})
}
