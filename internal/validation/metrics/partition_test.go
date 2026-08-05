package metrics

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestPartitionKey_ZeroValueInvalid, sıfır değerinin sorguya giremediğini
// doğrular.
//
// K4'ün ilk savunması budur: `var p PartitionKey` ile kurulmuş bir anahtar
// "belirtilmemiş" demektir ve ölçüm reddedilmelidir.
func TestPartitionKey_ZeroValueInvalid(t *testing.T) {
	var zero PartitionKey

	if zero.Valid() {
		t.Error("sıfır değeri geçerli sayıldı")
	}
	if err := zero.validate(); err == nil {
		t.Error("sıfır değeri sorguya kabul edildi")
	}
}

// TestPartitionKey_OnlyTwoValues, paket dışından yalnızca iki değerin
// üretilebildiğini doğrular.
func TestPartitionKey_OnlyTwoValues(t *testing.T) {
	if Calibration.String() != "C" {
		t.Errorf("Calibration = %q, \"C\" beklenir", Calibration)
	}
	if Validation.String() != "V" {
		t.Errorf("Validation = %q, \"V\" beklenir", Validation)
	}
	if !Calibration.Valid() || !Validation.Valid() {
		t.Error("tanımlı değerlerden biri geçersiz sayıldı")
	}
	if Calibration == Validation {
		t.Error("iki bölüm anahtarı eşit")
	}
}

// TestParsePartition_RejectsMixed, karışık küme ifadelerinin reddedildiğini
// doğrular — K4'ün "karışık sorgu → hata" ölçütü.
func TestParsePartition_RejectsMixed(t *testing.T) {
	valid := map[string]PartitionKey{"C": Calibration, "V": Validation}
	for in, want := range valid {
		got, err := ParsePartition(in)
		if err != nil {
			t.Errorf("%q reddedildi: %v", in, err)
		}
		if got != want {
			t.Errorf("%q → %v, %v beklenir", in, got, want)
		}
	}

	// Karışık ya da geçersiz her ifade reddedilmeli.
	for _, in := range []string{"", "c", "v", "C,V", "CV", "both", "*", "all", "%", "C V"} {
		if _, err := ParsePartition(in); err == nil {
			t.Errorf("%q kabul edildi — karışık/geçersiz küme reddedilmeliydi", in)
		}
	}
}

// TestQueries_RejectUnsetPartition, üç metrik sorgusunun da belirtilmemiş
// bölüm anahtarını reddettiğini doğrular.
//
// Veritabanı bağlantısı gerekmez: denetim sorgudan önce çalışır. Bu, K4'ün
// altyapısız koşabilen regresyon testidir.
func TestQueries_RejectUnsetPartition(t *testing.T) {
	var zero PartitionKey
	ctx := context.Background()
	runID := uuid.New()

	if _, err := Coverage(ctx, nil, runID, zero); err == nil {
		t.Error("F.1 belirtilmemiş bölümü kabul etti")
	}
	if _, err := AreaStats(ctx, nil, runID, zero); err == nil {
		t.Error("F.2 belirtilmemiş bölümü kabul etti")
	}
	if _, err := Errors(ctx, nil, runID, zero); err == nil {
		t.Error("F.3 belirtilmemiş bölümü kabul etti")
	}
	if _, err := Compute(ctx, nil, runID, "A", zero); err == nil {
		t.Error("Compute belirtilmemiş bölümü kabul etti")
	}
}

// TestKey_Label, ölçüm etiketlerini doğrular.
func TestKey_Label(t *testing.T) {
	cases := []struct {
		key  Key
		want string
	}{
		{Key{Method: "B0", Confidence: -1}, "B0"},
		{Key{Method: "B1", Confidence: -1}, "B1"},
		{Key{Method: "M", Confidence: 0.50}, "M@50%"},
		{Key{Method: "M", Confidence: 0.90}, "M@90%"},
		{Key{Method: "M", Confidence: 0.95}, "M@95%"},
	}
	for _, tc := range cases {
		if got := tc.key.Label(); got != tc.want {
			t.Errorf("etiket %q, %q beklenir", got, tc.want)
		}
	}
}

// TestTrimMethod, CHAR(2) boşluk dolgusunun temizlendiğini doğrular.
//
// PostgreSQL 'M' değerini "M " olarak döndürür; Sprint 4'te K8 raporunda
// etiketler bu yüzden yanlış basılmıştı.
func TestTrimMethod(t *testing.T) {
	cases := map[string]string{"M ": "M", "B0": "B0", "B1": "B1", "M": "M", "  ": ""}
	for in, want := range cases {
		if got := trimMethod(in); got != want {
			t.Errorf("trimMethod(%q) = %q, %q beklenir", in, got, want)
		}
	}
}

// TestRow_Validate, şema kısıtlarının istemci tarafında yakalandığını
// doğrular.
func TestRow_Validate(t *testing.T) {
	base := Row{
		Key:          Key{Method: "M", Confidence: 0.90},
		PartitionKey: Validation,
		Scenario:     "A",
		NEvents:      100, CoverageRate: 0.9,
		MedianAreaKM2: 1, P90AreaKM2: 2,
		MedianHaversineM: 10, R50M: 10, R95M: 30,
		MedianPartCount: 1, P95PartCount: 3, RepairedRatio: 0,
	}
	if err := base.validate(); err != nil {
		t.Fatalf("geçerli satır reddedildi: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Row)
	}{
		{"olay sayısı sıfır", func(r *Row) { r.NEvents = 0 }},
		{"kapsama 1'den büyük", func(r *Row) { r.CoverageRate = 1.5 }},
		{"kapsama negatif", func(r *Row) { r.CoverageRate = -0.1 }},
		{"alan sıfır", func(r *Row) { r.MedianAreaKM2 = 0 }},
		{"hata negatif", func(r *Row) { r.R95M = -1 }},
		{"onarım oranı 1'den büyük", func(r *Row) { r.RepairedRatio = 1.2 }},
	}
	for _, tc := range cases {
		row := base
		tc.mut(&row)
		if err := row.validate(); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}

// TestRoundPartCount, parça sayısı yuvarlamasını doğrular.
//
// `percentile_cont` ara değer üretir (2,5); sütun INTEGER ve CHECK ≥ 1'dir.
func TestRoundPartCount(t *testing.T) {
	cases := map[float64]int{0: 1, 0.4: 1, 1: 1, 1.4: 1, 1.5: 2, 2.5: 3, 6.7: 7}
	for in, want := range cases {
		if got := roundPartCount(in); got != want {
			t.Errorf("roundPartCount(%g) = %d, %d beklenir", in, got, want)
		}
	}
}
