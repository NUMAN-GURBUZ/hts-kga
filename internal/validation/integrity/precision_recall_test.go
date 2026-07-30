package integrity

import (
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

func f(v float64) *float64 { return &v }

// TestWilsonInterval_KnownValues, aralığın bilinen noktalarda doğru olduğunu
// sınar.
func TestWilsonInterval_KnownValues(t *testing.T) {
	tests := []struct {
		name               string
		successes, trials  int64
		wantLoLt, wantHiGt float64
	}{
		// 3/3 → normal yaklaşım [1,1] derdi; Wilson alt sınırı belirgin
		// biçimde 1'in altındadır. K7'nin en yakıcı sorusu buydu:
		// "3 bulguyla %100 precision ne kadar bilgi?"
		{"3/3 küçük örneklem", 3, 3, 0.5, 0.99},
		{"100/100 büyük örneklem", 100, 100, 0.97, 0.99},
		{"45/50", 45, 50, 0.80, 0.94},
		{"0/10", 0, 10, 0.001, 0.25},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lo, hi := WilsonInterval(tc.successes, tc.trials)
			if lo > tc.wantLoLt {
				t.Errorf("alt sınır %.4f, %.4f'ten küçük olmalı", lo, tc.wantLoLt)
			}
			if hi < tc.wantHiGt {
				t.Errorf("üst sınır %.4f, %.4f'ten büyük olmalı", hi, tc.wantHiGt)
			}
			p := float64(tc.successes) / float64(tc.trials)
			if p < lo || p > hi {
				t.Errorf("nokta kestirimi %.4f aralığın dışında [%.4f, %.4f]", p, lo, hi)
			}
		})
	}

	if lo, hi := WilsonInterval(0, 0); lo != 0 || hi != 0 {
		t.Errorf("sıfır denemede aralık (%g, %g), beklenen (0, 0)", lo, hi)
	}
}

// TestWilsonInterval_NarrowsWithSampleSize, aralığın örneklem büyüdükçe
// daraldığını sınar — ADR-31/9'un gerekçesi bu.
func TestWilsonInterval_NarrowsWithSampleSize(t *testing.T) {
	var prevWidth float64 = 2
	for _, n := range []int64{3, 10, 30, 100, 1000} {
		lo, hi := WilsonInterval(n, n) // hep %100 precision
		width := hi - lo
		if width >= prevWidth {
			t.Errorf("n=%d aralık genişliği %.4f, öncekinden (%.4f) daralmadı", n, width, prevWidth)
		}
		prevWidth = width
	}
}

// TestPBT_WilsonIntervalContainsEstimate, aralığın her girdide nokta kestirimini
// içerdiğini ve [0,1] sınırlarında kaldığını sınar.
func TestPBT_WilsonIntervalContainsEstimate(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		trials := rapid.Int64Range(1, 1_000_000).Draw(t, "denemeler")
		successes := rapid.Int64Range(0, trials).Draw(t, "başarılar")

		lo, hi := WilsonInterval(successes, trials)
		p := float64(successes) / float64(trials)

		switch {
		case math.IsNaN(lo) || math.IsNaN(hi):
			t.Fatalf("aralık NaN (%d/%d)", successes, trials)
		case lo < 0 || hi > 1:
			t.Fatalf("aralık [0,1] dışında: [%.6f, %.6f]", lo, hi)
		case lo > hi:
			t.Fatalf("alt sınır üst sınırdan büyük: [%.6f, %.6f]", lo, hi)
		case p < lo-1e-9 || p > hi+1e-9:
			t.Fatalf("nokta kestirimi %.6f aralığın dışında [%.6f, %.6f]", p, lo, hi)
		}
	})
}

// TestRow_MeetsK7, üç durumun ayrımını sınar (ADR-31/9).
//
// "Ölçülemedi" ile "tutmadı" farklı sonuçlardır: birincisi ne geçti ne kaldı,
// ikincisi ölçüldü ve eşiğin altında. Karıştırılması K7 raporunu yanlış yapar.
func TestRow_MeetsK7(t *testing.T) {
	tests := []struct {
		name        string
		row         Row
		wantMeets   bool
		wantVerdict string
	}{
		{
			name:        "yüksek precision, yeterli bulgu",
			row:         Row{RuleID: integrityrule.Inventory, Findings: 1172, Precision: f(1.0), Sufficient: true},
			wantMeets:   true,
			wantVerdict: "geçti",
		},
		{
			name:        "yüksek precision, yetersiz bulgu",
			row:         Row{RuleID: integrityrule.Velocity, Findings: 3, Precision: f(1.0), Sufficient: false},
			wantMeets:   false,
			wantVerdict: "yetersiz (3 bulgu)",
		},
		{
			name:        "eşiğin altında",
			row:         Row{RuleID: integrityrule.Velocity, Findings: 158, Precision: f(0.367), Sufficient: true},
			wantMeets:   false,
			wantVerdict: "tutmadı",
		},
		{
			name:        "bulgu yok",
			row:         Row{RuleID: integrityrule.Velocity, Findings: 0},
			wantMeets:   false,
			wantVerdict: "ölçülemedi (kanonik bulgu yok)",
		},
		{
			name:        "kural 4 kapsam dışı",
			row:         Row{RuleID: integrityrule.Trajectory, Injected: 1171},
			wantMeets:   false,
			wantVerdict: "kapsam dışı (ADR-30)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.row.MeetsK7(K7Threshold); got != tc.wantMeets {
				t.Errorf("MeetsK7 = %v, beklenen %v", got, tc.wantMeets)
			}
			if got := tc.row.Verdict(K7Threshold); got != tc.wantVerdict {
				t.Errorf("Verdict = %q, beklenen %q", got, tc.wantVerdict)
			}
		})
	}
}

// TestReport_K7Verdict, kural bazlı özetin dört kategoriyi ayırdığını sınar.
func TestReport_K7Verdict(t *testing.T) {
	r := Report{
		RunID:       uuid.New(),
		Scenario:    "A",
		Threshold:   K7Threshold,
		MinFindings: 30,
		Rows: []Row{
			{RuleID: integrityrule.Inventory, Findings: 1172, Precision: f(1.0), Sufficient: true},
			{RuleID: integrityrule.Velocity, Findings: 158, Precision: f(0.367), Sufficient: true},
			{RuleID: integrityrule.TimeOrder, Findings: 739, Precision: f(1.0), Sufficient: true},
			{RuleID: integrityrule.Trajectory},
			{RuleID: integrityrule.Activity, Findings: 1171, Precision: f(1.0), Sufficient: true},
		},
	}

	verdict := r.K7Verdict()
	for _, want := range []string{"geçti: 1, 3, 5", "tutmadı: 2", "kapsam dışı (ADR-30): 4"} {
		if !strings.Contains(verdict, want) {
			t.Errorf("özet %q içermeli:\n%s", want, verdict)
		}
	}
}

// TestReport_SummaryIsComplete, özet tablosunun beş kuralı da listelediğini
// sınar.
//
// Bulgu üretmeyen kuralın satırı düşerse "ölçülmedi" ile "sıfır bulgu" ayrımı
// kaybolur.
func TestReport_SummaryIsComplete(t *testing.T) {
	r := Report{
		RunID: uuid.New(), Scenario: "A", Threshold: K7Threshold, MinFindings: 30,
	}
	for _, id := range integrityrule.ByID {
		r.Rows = append(r.Rows, Row{RuleID: id, RuleName: id.Name()})
	}

	out := r.Summary()
	for _, id := range integrityrule.ByID {
		if !strings.Contains(out, id.Name()) {
			t.Errorf("özet %q kuralını içermiyor", id.Name())
		}
	}
	if !strings.Contains(out, "ADR-31/9") {
		t.Error("özet ölçülebilirlik kuralını belirtmeli")
	}
}
