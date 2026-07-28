package split

import (
	"fmt"
	"math"
	"testing"

	"github.com/google/uuid"
)

const eps = 1e-12

// mustSplitter, geçerli bir bölümleyici kurar.
func mustSplitter(t *testing.T, seed int64, ratio float64) Splitter {
	t.Helper()
	s, err := New(seed, ratio)
	if err != nil {
		t.Fatalf("New(%d, %g): %v", seed, ratio, err)
	}
	return s
}

// eventIDs, deterministik test olay kimlikleri üretir.
//
// Gerçek event_id'ler de UUIDv5 olduğundan aynı biçim kullanılır; paket
// internal/simulator/event'i tanımaz, kimlikler burada bağımsız üretilir.
func eventIDs(n int) []uuid.UUID {
	ns := uuid.MustParse("29b15779-62b7-4b8d-889e-417227237e61")
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.NewSHA1(ns, []byte(fmt.Sprintf("event-%d", i)))
	}
	return ids
}

// TestGoldenVector, bölümleme sözleşmesini dondurur.
//
// Değerler uygulamadan bağımsız hesaplanmıştır (SHA-256 doğrudan belgelenen
// bayt düzenine uygulanarak). Test düşerse ayrımın tanımı değişmiş demektir:
// daha önce kalibrasyona düşmüş olaylar doğrulamaya kayar ve K1/K3 ölçümleri
// önceki koşularla karşılaştırılamaz hâle gelir.
func TestGoldenVector(t *testing.T) {
	s := mustSplitter(t, 42, DefaultCalibrationRatio)

	cases := []struct {
		id      string
		wantU   float64
		wantSet Partition
	}{
		{"6e89a177-fb74-5d45-aa75-cd6d47b2a41f", 0.98626186358900503, Validation},
		{"3afa79dc-78e3-51c6-a136-7fde9d5c7220", 0.00040636319810083, Calibration},
		{"f97a725f-773d-522a-a4f3-19799e145606", 0.90415044947628886, Validation},
		{"e2445eb0-ef03-5bae-8784-d8c6a6320579", 0.69421431252939469, Calibration},
		{"00000000-0000-0000-0000-000000000000", 0.01794211329003259, Calibration},
	}

	for _, tc := range cases {
		id := uuid.MustParse(tc.id)
		if got := s.Uniform(id); math.Abs(got-tc.wantU) > eps {
			t.Errorf("%s: u = %.17f, beklenen %.17f", tc.id, got, tc.wantU)
		}
		if got := s.Of(id); got != tc.wantSet {
			t.Errorf("%s: bölüm = %s, beklenen %s", tc.id, got, tc.wantSet)
		}
	}
}

// TestDeterministic, aynı olayın her zaman aynı kümeye düştüğünü denetler.
func TestDeterministic(t *testing.T) {
	s := mustSplitter(t, 42, DefaultCalibrationRatio)
	ids := eventIDs(500)

	first := make([]Partition, len(ids))
	for i, id := range ids {
		first[i] = s.Of(id)
	}
	for round := 0; round < 10; round++ {
		s2 := mustSplitter(t, 42, DefaultCalibrationRatio)
		for i, id := range ids {
			if got := s2.Of(id); got != first[i] {
				t.Fatalf("%s: %d. turda %s, ilk turda %s", id, round, got, first[i])
			}
		}
	}
}

// TestRatioIsHonoured, gerçekleşen oranın hedefe oturduğunu denetler.
func TestRatioIsHonoured(t *testing.T) {
	const n = 200000
	ids := eventIDs(n)

	for _, ratio := range []float64{0.50, 0.80, 0.95} {
		s := mustSplitter(t, 42, ratio)
		calib := 0
		for _, id := range ids {
			if s.Of(id) == Calibration {
				calib++
			}
		}
		got := float64(calib) / float64(n)
		// σ = √(p(1−p)/n) ≈ 0,0009 (p = 0,8). %1 mutlak sınır ~11σ'dır ve
		// sabit kimlik kümesiyle deterministiktir.
		if math.Abs(got-ratio) > 0.01 {
			t.Errorf("oran %g için gerçekleşen %.4f (n = %d)", ratio, got, n)
		}
		t.Logf("hedef %.2f → gerçekleşen %.4f (n = %d)", ratio, got, n)
	}
}

// TestRatioMonotonicity, oranın büyümesinin olayları yalnızca doğrulamadan
// kalibrasyona taşıdığını denetler; ters yönde kayma olmamalıdır.
//
// Bu, oranın ileride değiştirilmesi hâlinde kalibrasyon kümesinin
// öngörülebilir biçimde büyümesini garanti eder.
func TestRatioMonotonicity(t *testing.T) {
	ids := eventIDs(20000)
	low := mustSplitter(t, 42, 0.60)
	high := mustSplitter(t, 42, 0.85)

	for _, id := range ids {
		if low.Of(id) == Calibration && high.Of(id) != Calibration {
			t.Fatalf("%s: oran büyüdüğünde kalibrasyondan çıktı", id)
		}
	}
}

// TestSeedSensitivity, farklı tohumların farklı ayrım verdiğini denetler.
func TestSeedSensitivity(t *testing.T) {
	ids := eventIDs(20000)
	a := mustSplitter(t, 42, DefaultCalibrationRatio)
	b := mustSplitter(t, 43, DefaultCalibrationRatio)

	diff := 0
	for _, id := range ids {
		if a.Of(id) != b.Of(id) {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("iki tohum aynı ayrımı üretti")
	}
	// Bağımsız iki 80/20 ayrımında beklenen fark 2·0,8·0,2 = %32.
	if rate := float64(diff) / float64(len(ids)); math.Abs(rate-0.32) > 0.02 {
		t.Errorf("tohumlar arası fark oranı %.4f, ~0.32 beklenir", rate)
	}
}

// TestExtremeRatios, sınır oranlarını kapsar.
func TestExtremeRatios(t *testing.T) {
	ids := eventIDs(1000)

	all := mustSplitter(t, 42, 1.0)
	none := mustSplitter(t, 42, 0.0)
	for _, id := range ids {
		if got := all.Of(id); got != Calibration {
			t.Fatalf("oran 1.0'da %s bölümü %s", id, got)
		}
		if got := none.Of(id); got != Validation {
			t.Fatalf("oran 0.0'da %s bölümü %s", id, got)
		}
	}
}

// TestUniformRange, bölümleme koordinatının [0,1) aralığında kaldığını
// denetler.
func TestUniformRange(t *testing.T) {
	s := mustSplitter(t, 42, DefaultCalibrationRatio)
	for _, id := range eventIDs(50000) {
		u := s.Uniform(id)
		if !(u >= 0 && u < 1) {
			t.Fatalf("%s: u = %g, [0,1) dışında", id, u)
		}
	}
}

// TestNewRejectsInvalidRatio, kurulum denetimlerini kapsar.
func TestNewRejectsInvalidRatio(t *testing.T) {
	for _, r := range []float64{-0.01, 1.01, math.NaN()} {
		if _, err := New(42, r); err == nil {
			t.Errorf("geçersiz oran kabul edildi: %g", r)
		}
	}
}

// TestPartitionValues, bölüm değerlerinin şema ile uyumunu denetler.
func TestPartitionValues(t *testing.T) {
	if byte(Calibration) != 'C' || byte(Validation) != 'V' {
		t.Fatal("bölüm harfleri ground_truth.partition_key ile uyuşmuyor")
	}
	if Calibration.String() != "C" || Validation.String() != "V" {
		t.Error("String() tek harfli gösterim vermiyor")
	}
	if Partition('X').Valid() {
		t.Error("tanımsız bölüm geçerli sayıldı")
	}
}

// BenchmarkOf, olay başına maliyeti ölçer.
func BenchmarkOf(b *testing.B) {
	s, err := New(42, DefaultCalibrationRatio)
	if err != nil {
		b.Fatal(err)
	}
	id := uuid.MustParse("6e89a177-fb74-5d45-aa75-cd6d47b2a41f")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = s.Of(id)
	}
}
