package integrityrule

import (
	"testing"

	"pgregory.net/rapid"
)

// TestIDs_MatchSchemaConstraint, beş kimliğin şema CHECK kısıtıyla (1..5)
// örtüştüğünü sınar.
//
// integrity_findings.rule_id ve ground_truth.injected_rule sütunlarının ikisi de
// CHECK (BETWEEN 1 AND 5) taşıyor. Aralık dışı bir kimlik, bulgunun
// veritabanına hiç yazılmaması demektir.
func TestIDs_MatchSchemaConstraint(t *testing.T) {
	if len(All) != 5 {
		t.Fatalf("All %d kural içeriyor, beklenen 5", len(All))
	}

	seen := make(map[ID]bool, len(All))
	for _, id := range All {
		if int(id) < 1 || int(id) > 5 {
			t.Errorf("%s: kimlik 1..5 aralığı dışında", id)
		}
		if seen[id] {
			t.Errorf("%s: kimlik iki kez listelenmiş", id)
		}
		seen[id] = true
	}

	for v := 1; v <= 5; v++ {
		if !seen[ID(v)] {
			t.Errorf("kimlik %d All listesinde yok", v)
		}
	}
}

// TestByID_IsAscendingAndComplete, enjektörün ağırlık tablosunu kurduğu sıranın
// kimlik sırası olduğunu sınar.
//
// Sıra determinizmin parçasıdır: kümülatif ağırlık tablosu bu sırada kurulur.
// Kayarsa aynı tohum farklı enjeksiyonlar üretir ve Sprint 5'in dört koşusu
// (K10) geçersiz olur.
func TestByID_IsAscendingAndComplete(t *testing.T) {
	if len(ByID) != len(All) {
		t.Fatalf("ByID %d, All %d kural içeriyor", len(ByID), len(All))
	}
	for i, id := range ByID {
		if int(id) != i+1 {
			t.Errorf("ByID[%d] = %s, beklenen kimlik %d", i, id, i+1)
		}
	}

	// İki liste aynı kümeyi kapsamalı, farklı sırada olmalı.
	same := true
	for i := range All {
		if All[i] != ByID[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("All ve ByID aynı sırada; öncelik ve kimlik sıraları ayrı olmalı (ADR-28 vs ADR-09)")
	}
}

// TestNames_FitColumnAndAreDistinct, adların rule_name VARCHAR(50) sütununa
// sığdığını ve birbirinden ayırt edilebilir olduğunu sınar.
func TestNames_FitColumnAndAreDistinct(t *testing.T) {
	const maxLen = 50 // integrity_findings.rule_name VARCHAR(50)

	names := make(map[string]ID, len(All))
	for _, id := range All {
		name := id.Name()
		if name == "" {
			t.Errorf("%d: ad boş", int(id))
		}
		if len(name) > maxLen {
			t.Errorf("%s: ad %d bayt, VARCHAR(%d) sütununa sığmaz", id, len(name), maxLen)
		}
		if other, dup := names[name]; dup {
			t.Errorf("%s ile %d aynı adı taşıyor: %q", id, int(other), name)
		}
		names[name] = id
	}
}

// TestClasses_MatchADR27, ADR-27'nin sınıf atamasını sınar.
//
// Atama ölçümden doğdu: kural 3 toplu modda tespit edilemez (precision %8),
// kural 5 akışta tespit edilemez (precision %50). Sınıfın kayması, o kuralın
// yanlış fazda koşması ve precision'ın çökmesi demektir.
func TestClasses_MatchADR27(t *testing.T) {
	want := map[ID]Class{
		Inventory:  ClassStream,
		TimeOrder:  ClassStream,
		Activity:   ClassBatch,
		Velocity:   ClassBatch,
		Trajectory: ClassAggregate,
	}

	for id, expected := range want {
		if got := id.Class(); got != expected {
			t.Errorf("%s sınıfı %q, ADR-27 %q diyor", id, got, expected)
		}
		if !id.Class().Valid() {
			t.Errorf("%s: sınıf geçersiz", id)
		}
	}
}

// TestEventAnchored_OnlyRule4Excluded, ADR-30'un kapsam kararını sınar.
func TestEventAnchored_OnlyRule4Excluded(t *testing.T) {
	for _, id := range All {
		want := id != Trajectory
		if got := id.EventAnchored(); got != want {
			t.Errorf("%s: EventAnchored() = %v, beklenen %v (ADR-30)", id, got, want)
		}
	}
}

// TestPrecedence_MatchesADR28, öncelik sırasının kanıt gücüne göre
// dondurulduğunu sınar.
//
// Sıra ölçülen precision'a göre seçilseydi aşırı uydurma olurdu; kanıt tipinden
// türetildi. Bu test o kararın kaymadığını garanti eder.
func TestPrecedence_MatchesADR28(t *testing.T) {
	wantOrder := []ID{Inventory, Activity, TimeOrder, Velocity, Trajectory}

	for i, id := range wantOrder {
		if got := id.Precedence(); got != i {
			t.Errorf("%s önceliği %d, beklenen %d", id, got, i)
		}
	}

	// Eşiği ve atıf belirsizliği olan kural (2) diğer üç olay-çıpalı kuraldan
	// zayıf olmalı — ADR-28'in tek gerçek işlevi bu.
	for _, stronger := range []ID{Inventory, Activity, TimeOrder} {
		if !stronger.StrongerThan(Velocity) {
			t.Errorf("%s, %s'ten güçlü olmalı (ADR-28)", stronger, Velocity)
		}
		if Velocity.StrongerThan(stronger) {
			t.Errorf("%s, %s'ten güçlü olmamalı", Velocity, stronger)
		}
	}
}

// TestStrongerThan_UndefinedIsWeakest, tanımsız kimliğin hiçbir tanımlı kuralı
// bastıramadığını sınar.
func TestStrongerThan_UndefinedIsWeakest(t *testing.T) {
	unknown := ID(99)

	if unknown.StrongerThan(Velocity) {
		t.Error("tanımsız kimlik tanımlı kuralı bastırmamalı")
	}
	if !Velocity.StrongerThan(unknown) {
		t.Error("tanımlı kural tanımsız kimlikten güçlü olmalı")
	}
	if unknown.StrongerThan(ID(98)) {
		t.Error("iki tanımsız kimlik arasında güç ilişkisi olmamalı")
	}
	if got := unknown.Precedence(); got != len(All) {
		t.Errorf("tanımsız önceliği %d, beklenen %d", got, len(All))
	}
}

// TestInClass_PartitionsAllRules, sınıfların beş kuralı örtüşmeden bölümlediğini
// sınar.
func TestInClass_PartitionsAllRules(t *testing.T) {
	total := 0
	for _, c := range []Class{ClassStream, ClassBatch, ClassAggregate} {
		ids := InClass(c)
		total += len(ids)
		for _, id := range ids {
			if id.Class() != c {
				t.Errorf("InClass(%q) %s döndürdü", c, id)
			}
		}
	}
	if total != len(All) {
		t.Errorf("sınıflar %d kural kapsıyor, beklenen %d", total, len(All))
	}

	// Sıra korunmalı: motor kuralları bu sırada koşturuyor (ADR-28).
	stream := InClass(ClassStream)
	if len(stream) != 2 || stream[0] != Inventory || stream[1] != TimeOrder {
		t.Errorf("akış kuralları öncelik sırasında değil: %v", stream)
	}
	batch := InClass(ClassBatch)
	if len(batch) != 2 || batch[0] != Activity || batch[1] != Velocity {
		t.Errorf("toplu kurallar öncelik sırasında değil: %v", batch)
	}
}

// TestParse_RejectsOutOfRange, veritabanından okunan kimliğin doğrulandığını
// sınar.
func TestParse_RejectsOutOfRange(t *testing.T) {
	for v := 1; v <= 5; v++ {
		id, err := Parse(v)
		if err != nil {
			t.Errorf("Parse(%d): beklenmeyen hata: %v", v, err)
		}
		if int(id) != v {
			t.Errorf("Parse(%d) = %d", v, int(id))
		}
	}

	for _, v := range []int{-1, 0, 6, 99} {
		if _, err := Parse(v); err == nil {
			t.Errorf("Parse(%d): hata bekleniyordu", v)
		}
	}
}

// TestString_UndefinedIsExplicit, tanımsız kimliğin gizlenmediğini sınar.
func TestString_UndefinedIsExplicit(t *testing.T) {
	if got := ID(7).String(); got != "ID(7)" {
		t.Errorf("ID(7).String() = %q", got)
	}
	if got := Velocity.String(); got != "2:kinematik tutarsızlık" {
		t.Errorf("Velocity.String() = %q", got)
	}
}

// TestPBT_PrecedenceIsStrictTotalOrder, önceliğin katı tam sıralama olduğunu
// sınar.
//
// Motor öncelik yarışını kazananı seçerken bu özelliğe güveniyor: iki kural
// birbirinden aynı anda güçlü olamaz, ve kendisinden güçlü olamaz. Aksi hâlde
// "kanonik bulgu" seçimi girdi sırasına bağlı hale gelirdi ve determinizm
// (K10) bozulurdu.
func TestPBT_PrecedenceIsStrictTotalOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := All[rapid.IntRange(0, len(All)-1).Draw(t, "a")]
		b := All[rapid.IntRange(0, len(All)-1).Draw(t, "b")]
		c := All[rapid.IntRange(0, len(All)-1).Draw(t, "c")]

		// Yansımasız: hiçbir kural kendisinden güçlü değil.
		if a.StrongerThan(a) {
			t.Fatalf("%s kendisinden güçlü", a)
		}

		// Antisimetrik: ikisi birbirinden aynı anda güçlü olamaz.
		if a.StrongerThan(b) && b.StrongerThan(a) {
			t.Fatalf("%s ve %s birbirinden güçlü", a, b)
		}

		// Tam: farklı iki kural arasında daima bir yön var.
		if a != b && !a.StrongerThan(b) && !b.StrongerThan(a) {
			t.Fatalf("%s ve %s karşılaştırılamıyor", a, b)
		}

		// Geçişli.
		if a.StrongerThan(b) && b.StrongerThan(c) && !a.StrongerThan(c) {
			t.Fatalf("geçişlilik bozuldu: %s > %s > %s", a, b, c)
		}
	})
}

// TestPBT_ValidIDsAreSelfConsistent, tanımlı her kimliğin tutarlı bir ad, sınıf
// ve öncelik taşıdığını sınar.
func TestPBT_ValidIDsAreSelfConsistent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := rapid.IntRange(-10, 20).Draw(t, "v")
		id := ID(v)

		if !id.Valid() {
			// Tanımsız kimlik: ad açıklayıcı olmalı, sınıf güvenli tarafa
			// düşmeli (aggregate = bulgu yazılmaz).
			if id.Class() != ClassAggregate {
				t.Fatalf("tanımsız %d sınıfı %q, beklenen %q", v, id.Class(), ClassAggregate)
			}
			if id.EventAnchored() {
				t.Fatalf("tanımsız %d olay-çıpalı sayılıyor", v)
			}
			return
		}

		if id.Name() == "" {
			t.Fatalf("%s: ad boş", id)
		}
		if !id.Class().Valid() {
			t.Fatalf("%s: sınıf geçersiz", id)
		}
		if p := id.Precedence(); p < 0 || p >= len(All) {
			t.Fatalf("%s: öncelik aralık dışı (%d)", id, p)
		}
		if parsed, err := Parse(v); err != nil || parsed != id {
			t.Fatalf("%s: Parse gidiş-dönüş bozuk (%v, %v)", id, parsed, err)
		}
	})
}
