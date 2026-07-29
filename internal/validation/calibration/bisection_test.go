package calibration

import (
	"context"
	"fmt"
	"math"
	"testing"

	"pgregory.net/rapid"
)

// planOptions, senaryo config'indeki kalibrasyon ayarlarıdır (ADR-02).
func planOptions() bisectOptions {
	return bisectOptions{
		Lo: 0.5, Hi: 3.0,
		Target: 0.90, Tolerance: 0.005, MaxIterations: 12,
	}
}

// logistic, monoton artan sentetik bir kapsama eğrisidir.
//
// Gerçek `coverage(λ)`'nın şekli bilinmez; bilinen tek şey ADR-02'nin
// monotonluk varsayımıdır. Test bu varsayımın **altında** bisection'ın doğru
// çalıştığını gösterir; varsayımın kendisi gerçek veriyle ölçülür (Sweep).
func logistic(mid, steepness float64) func(float64) (float64, error) {
	return func(lambda float64) (float64, error) {
		return 1 / (1 + math.Exp(-steepness*(lambda-mid))), nil
	}
}

// TestBisect_FindsKnownRoot, bilinen kökü toleransta bulduğunu doğrular.
func TestBisect_FindsKnownRoot(t *testing.T) {
	// Kök: 1/(1+e^(-2(λ−1.5))) = 0.90  →  λ = 1.5 + ln(9)/2 ≈ 2.5986
	eval := logistic(1.5, 2.0)
	wantRoot := 1.5 + math.Log(9)/2

	result, err := bisect(context.Background(), eval, planOptions())
	if err != nil {
		t.Fatalf("bisect: %v", err)
	}

	if !result.Converged {
		t.Fatalf("yakınsamadı: λ=%.4f kapsama=%.4f", result.Lambda, result.Coverage)
	}
	if math.Abs(result.Coverage-0.90) >= 0.005 {
		t.Errorf("kapsama %.5f, 0,90 ± 0,005 beklenir", result.Coverage)
	}
	if math.Abs(result.Lambda-wantRoot) > 0.05 {
		t.Errorf("λ* %.4f, %.4f beklenir", result.Lambda, wantRoot)
	}
	t.Logf("λ* = %.4f (analitik %.4f), %d iterasyon", result.Lambda, wantRoot, result.Iterations)
}

// TestBisect_FlatCurveDoesNotConverge, düz eğride yakınsamama davranışını
// doğrular — TA'lı senaryolarda beklenen durum (ADR-25).
//
// Önemli olan hata dönmemesi: λ* yine yazılır, `Converged=false` işaretlenir
// ve izleme eğrinin düz olduğunu gösterir. Sessizce "kalibre edildi"
// denmemelidir.
func TestBisect_FlatCurveDoesNotConverge(t *testing.T) {
	// λ ne olursa olsun kapsama 0,99: hedefe (0,90) hiç inmiyor.
	eval := func(float64) (float64, error) { return 0.99, nil }

	result, err := bisect(context.Background(), eval, planOptions())
	if err != nil {
		t.Fatalf("düz eğri hata döndürdü: %v", err)
	}

	if result.Converged {
		t.Error("düz eğride yakınsadı işaretlendi")
	}
	if result.Iterations != 12 {
		t.Errorf("%d iterasyon, 12 (üst sınır) beklenir", result.Iterations)
	}
	if result.Lambda < 0.5 || result.Lambda > 3.0 {
		t.Errorf("λ* %.4f aralık dışında", result.Lambda)
	}
	if len(result.Trace) != 13 {
		t.Errorf("izleme %d nokta, 13 beklenir (12 iterasyon + son ölçüm)", len(result.Trace))
	}

	// İzleme düzlüğü göstermeli: tüm kapsama değerleri aynı.
	for _, p := range result.Trace {
		if p.Coverage != 0.99 {
			t.Errorf("izlemede beklenmeyen kapsama %.4f", p.Coverage)
		}
	}
}

// TestBisect_UnreachableRootBelowRange, kök aralığın altındayken davranışı
// doğrular: λ alt sınıra yaklaşır ama yakınsama iddia edilmez.
func TestBisect_UnreachableRootBelowRange(t *testing.T) {
	// Kapsama daima hedefin üstünde → hi hep düşer → λ* alt sınıra yaklaşır.
	eval := func(lambda float64) (float64, error) { return 0.95 + 0.01*lambda, nil }

	result, err := bisect(context.Background(), eval, planOptions())
	if err != nil {
		t.Fatalf("bisect: %v", err)
	}
	if result.Converged {
		t.Error("erişilemeyen kökte yakınsadı işaretlendi")
	}
	if result.Lambda > 0.6 {
		t.Errorf("λ* %.4f, alt sınıra (0,5) yaklaşması beklenir", result.Lambda)
	}
}

// TestBisect_PropagatesEvalError, değerlendirici hatasının yutulmadığını
// doğrular.
func TestBisect_PropagatesEvalError(t *testing.T) {
	eval := func(float64) (float64, error) { return 0, fmt.Errorf("envanter okunamadı") }

	if _, err := bisect(context.Background(), eval, planOptions()); err == nil {
		t.Fatal("değerlendirici hatası yutuldu")
	}
}

// TestBisect_RespectsCancellation, iptal edilen bağlamda durduğunu doğrular.
func TestBisect_RespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := bisect(ctx, logistic(1.5, 2), planOptions()); err == nil {
		t.Fatal("iptal edilen bağlamda çalışmaya devam etti")
	}
}

// TestBisect_Rejects, geçersiz parametreleri reddeder.
func TestBisect_Rejects(t *testing.T) {
	eval := logistic(1.5, 2)

	cases := []struct {
		name string
		mut  func(*bisectOptions)
	}{
		{"aralık ters", func(o *bisectOptions) { o.Lo, o.Hi = 3.0, 0.5 }},
		{"aralık dejenere", func(o *bisectOptions) { o.Lo, o.Hi = 1.0, 1.0 }},
		{"hedef 1", func(o *bisectOptions) { o.Target = 1 }},
		{"hedef 0", func(o *bisectOptions) { o.Target = 0 }},
		{"tolerans sıfır", func(o *bisectOptions) { o.Tolerance = 0 }},
		{"iterasyon sıfır", func(o *bisectOptions) { o.MaxIterations = 0 }},
	}
	for _, tc := range cases {
		opts := planOptions()
		tc.mut(&opts)
		if _, err := bisect(context.Background(), eval, opts); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}

// TestBisect_Deterministic, aynı eğride aynı λ*'ı verdiğini doğrular (K10).
func TestBisect_Deterministic(t *testing.T) {
	eval := logistic(1.5, 2)

	first, err := bisect(context.Background(), eval, planOptions())
	if err != nil {
		t.Fatalf("bisect: %v", err)
	}
	for i := 0; i < 5; i++ {
		got, err := bisect(context.Background(), eval, planOptions())
		if err != nil {
			t.Fatalf("bisect: %v", err)
		}
		if got.Lambda != first.Lambda {
			t.Fatalf("iter %d: λ* ayrıştı %.17g ≠ %.17g", i, got.Lambda, first.Lambda)
		}
	}
}

// TestPBT_BisectionOnMonotoneCurves, monoton her eğride ikili aramanın ya
// yakınsadığını ya da kökün aralık dışında olduğunu doğrular (ADR-11).
//
// Değişmez: yakınsadıysa |kapsama − hedef| < tolerans; yakınsamadıysa kök
// gerçekten [lo, hi] aralığında değildir.
func TestPBT_BisectionOnMonotoneCurves(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mid := rapid.Float64Range(-2, 6).Draw(t, "orta")
		steep := rapid.Float64Range(0.5, 8).Draw(t, "diklik")
		eval := logistic(mid, steep)

		opts := planOptions()
		result, err := bisect(context.Background(), eval, opts)
		if err != nil {
			t.Fatalf("bisect: %v", err)
		}

		if result.Converged {
			if math.Abs(result.Coverage-opts.Target) >= opts.Tolerance {
				t.Fatalf("yakınsadı denildi ama |%.6f − %.2f| ≥ %.3f",
					result.Coverage, opts.Target, opts.Tolerance)
			}
			return
		}

		// Yakınsamadıysa: kök aralığın dışında olmalı. Uçlardaki kapsama
		// değerleri hedefin aynı tarafındaysa bu doğrulanmış olur.
		loCov, err := eval(opts.Lo)
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		hiCov, err := eval(opts.Hi)
		if err != nil {
			t.Fatalf("eval: %v", err)
		}

		belowBoth := loCov > opts.Target && hiCov > opts.Target
		aboveBoth := loCov < opts.Target && hiCov < opts.Target
		if !belowBoth && !aboveBoth {
			t.Fatalf("kök aralıkta (lo=%.4f, hi=%.4f, hedef=%.2f) ama yakınsamadı "+
				"(λ*=%.4f, kapsama=%.4f, %d iterasyon)",
				loCov, hiCov, opts.Target, result.Lambda, result.Coverage, result.Iterations)
		}
	})
}

// TestPBT_BisectionNarrowsInterval, her iterasyonun aralığı yarıladığını ve
// izlemenin tutarlı olduğunu doğrular.
func TestPBT_BisectionNarrowsInterval(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mid := rapid.Float64Range(0.5, 3).Draw(t, "orta")
		steep := rapid.Float64Range(1, 5).Draw(t, "diklik")

		opts := planOptions()
		result, err := bisect(context.Background(), logistic(mid, steep), opts)
		if err != nil {
			t.Fatalf("bisect: %v", err)
		}

		if len(result.Trace) == 0 {
			t.Fatal("izleme boş")
		}
		for _, p := range result.Trace {
			if p.Lambda < opts.Lo || p.Lambda > opts.Hi {
				t.Fatalf("λ=%.4f aralık [%.2f, %.2f] dışında", p.Lambda, opts.Lo, opts.Hi)
			}
			if p.Coverage < 0 || p.Coverage > 1 {
				t.Fatalf("kapsama %.4f [0,1] dışında", p.Coverage)
			}
		}
		if result.Iterations > opts.MaxIterations {
			t.Fatalf("%d iterasyon, üst sınır %d", result.Iterations, opts.MaxIterations)
		}
	})
}
