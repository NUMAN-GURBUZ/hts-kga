// T-E04-06 — Bisection kalibrasyon döngüsü (ADR-02, ADR-25).
//
//	coverage@90%(λ) = 0.90 denklemini λ ∈ [0.5, 3.0] aralığında çöz.
//
// # Neden kök bulma
//
// ADR-02: `coverage(λ)` λ'da monoton artandır (daha büyük belirsizlik → daha
// geniş bölge → daha yüksek kapsama). Monoton tek değişkenli fonksiyonda
// ikili arama garantili yakınsar ve maliyeti önceden bellidir: 12 iterasyon ×
// 5.000 olay.
//
// # λ'nın kaldıraç sınırı (ADR-25)
//
// λ yalnızca σ_eff'i ölçekler; TA penceresi (`w_TA`, ADR-18) geometrik
// örtüşme oranıdır ve λ'yı **görmez**. TA'lı senaryolarda kütlenin ezici
// kısmı halkanın içinde kaldığı için `coverage(λ)` düz seyredebilir ve
// bisection hedefi tutturamayabilir.
//
// Bu bir hata değil, modelin sınırıdır. Döngü yakınsamazsa:
//
//   - λ* yine yazılır (aralığın son ortası),
//   - `Converged=false` işaretlenir,
//   - izleme (trace) raporda kalır ve eğrinin düz olduğu görülür.
//
// Sessizce "kalibre edildi" denmez.

package calibration

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/sampling"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// Point, tek bir λ denemesinin sonucudur.
type Point struct {
	Lambda   float64
	Coverage float64
}

// Result, kalibrasyon koşusunun çıktısıdır.
type Result struct {
	// Lambda, bulunan λ*'dır — run_config.lambda'ya yazılır.
	Lambda float64
	// Coverage, λ* ile ölçülen kapsama oranıdır.
	Coverage float64
	// Target, hedeflenen kapsama (calibration.target_coverage).
	Target float64
	// Converged, |kapsama − hedef| < tolerans sağlandı mı.
	Converged bool
	// Iterations, yürütülen iterasyon sayısıdır.
	Iterations int
	// Trace, denenen tüm (λ, kapsama) çiftleridir — eğrinin şekli buradan
	// okunur; düz bir eğri yakınsamamanın nedenini gösterir (ADR-25).
	Trace []Point
	// SampleSize, kalibrasyon örnekleminin büyüklüğüdür.
	SampleSize int
	// Duration, koşu süresidir.
	Duration time.Duration
}

// Summary, sonucun okunabilir özetidir.
func (r Result) Summary() string {
	status := "yakınsadı"
	if !r.Converged {
		status = "YAKINSAMADI — λ'nın kaldıracı yetersiz (ADR-25)"
	}
	out := fmt.Sprintf("kalibrasyon: λ* = %.4f · kapsama %.4f (hedef %.2f) · %s · "+
		"%d iterasyon · %d olay · %s\n",
		r.Lambda, r.Coverage, r.Target, status, r.Iterations, r.SampleSize,
		r.Duration.Round(time.Second))
	for _, p := range r.Trace {
		out += fmt.Sprintf("    λ=%.4f → kapsama %.4f\n", p.Lambda, p.Coverage)
	}
	return out
}

// Config, kalibrasyon koşusunun parametreleridir.
type Config struct {
	// Pool, veritabanı bağlantısıdır.
	Pool *postgres.Pool
	// Inventory, envanter kaynağıdır (Redis).
	Inventory params.CellSource
	// Scenario, senaryo yapılandırmasıdır.
	Scenario *config.Scenario
	// RunID, kalibre edilecek koşudur.
	RunID uuid.UUID
	// Logger, isteğe bağlıdır.
	Logger *slog.Logger
}

// Runner, kalibrasyon döngüsünü yürütür.
type Runner struct {
	pool             *postgres.Pool
	scenario         *config.Scenario
	runID            uuid.UUID
	log              *slog.Logger
	inventory        *params.Inventory
	grid             *density.Grid
	projector        *geo.Projector
	samples          []sample
	baseConfig       density.Config
	technology       ta.Technology
	taEnabled        bool
	targetConfidence float64
}

// New, kalibrasyon koşusunu hazırlar: envanteri yükler, kalibrasyon kümesini
// okur ve örneklemi seçer.
func New(cfg Config) (*Runner, error) {
	switch {
	case cfg.Pool == nil:
		return nil, fmt.Errorf("kalibrasyon: veritabanı bağlantısı zorunlu")
	case cfg.Inventory == nil:
		return nil, fmt.Errorf("kalibrasyon: envanter kaynağı zorunlu")
	case cfg.Scenario == nil:
		return nil, fmt.Errorf("kalibrasyon: senaryo zorunlu")
	case cfg.RunID == uuid.Nil:
		return nil, fmt.Errorf("kalibrasyon: run_id zorunlu (ADR-05)")
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	scn := cfg.Scenario
	ctx := context.Background()

	inventory, err := params.Load(ctx, cfg.Inventory, cfg.RunID,
		scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		return nil, fmt.Errorf("kalibrasyon: envanter yüklenemedi: %w", err)
	}

	grid, err := density.NewGrid(scn.Analysis.GridResolutionM)
	if err != nil {
		return nil, fmt.Errorf("kalibrasyon: ızgara: %w", err)
	}

	tech, err := technologyOf(scn.TimingAdvance.Enabled, scn.TimingAdvance.Technology)
	if err != nil {
		return nil, err
	}

	all, err := loadCalibrationSet(ctx, cfg.Pool.Querier(), cfg.RunID)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("kalibrasyon: 'C' kümesinde kapsanan olay yok (%s) — "+
			"koşu tamamlandı mı?", cfg.RunID)
	}

	samples := selectSample(all, scn.Analysis.Sample.CalibrationEvents, scn.Run.Seed)

	return &Runner{
		pool:      cfg.Pool,
		scenario:  scn,
		runID:     cfg.RunID,
		log:       log,
		inventory: inventory,
		grid:      grid,
		projector: inventory.Projector(),
		samples:   samples,
		baseConfig: density.Config{
			RxSensitivityDBm: scn.Network.RxSensitivityDBm,
			SigmaNominalDB:   scn.Radio.ShadowingSigmaDB,
			Lambda:           1,
			UTHeightM:        rf.UTHeightM,
		},
		technology:       tech,
		taEnabled:        scn.TimingAdvance.Enabled,
		targetConfidence: modelConfidence,
	}, nil
}

// modelConfidence, kalibrasyonun hedeflediği güven seviyesidir (ADR-02).
//
// Kapsama @90% üzerinden ölçülür; K1 de bu seviyeyi denetler.
const modelConfidence = 0.90

// selectSample, tam N olay seçer (ADR-14, T-E04-07).
//
// Seçim `sampling.ExactSample` ile karma sırasına göre yapılır: girdi
// sırasından bağımsız, tohuma bağlı ve tekrarlanabilir. Kümenin iterasyonlar
// arasında sabit kalması, λ*'ın örneklem gürültüsüne değil λ'ya yakınsaması
// için zorunludur.
func selectSample(all []sample, target int, seed int64) []sample {
	if target <= 0 || target >= len(all) {
		return all
	}

	ids := make([]uuid.UUID, len(all))
	for i, s := range all {
		ids[i] = s.eventID
	}

	chosen := sampling.ExactSample(ids, target, seed)
	keep := make(map[uuid.UUID]struct{}, len(chosen))
	for _, id := range chosen {
		keep[id] = struct{}{}
	}

	// Özgün sıra korunur: kayan nokta toplamları sıraya duyarlıdır (K10).
	out := make([]sample, 0, len(chosen))
	for _, s := range all {
		if _, ok := keep[s.eventID]; ok {
			out = append(out, s)
		}
	}
	return out
}

// SampleSize, kalibrasyon örnekleminin büyüklüğünü döndürür.
func (r *Runner) SampleSize() int { return len(r.samples) }

// Sweep, verilen λ değerlerinde kapsamayı ölçer (pilot ölçüm — G0).
//
// Bisection'dan önce eğrinin şeklini görmek içindir: monotonluk ADR-02'de
// **varsayılmıştır**, ölçülmemiştir. Düz ya da monoton olmayan bir eğri,
// ikili aramanın sessizce yanlış kök vermesi demektir.
func (r *Runner) Sweep(ctx context.Context, lambdas []float64) ([]Point, error) {
	out := make([]Point, 0, len(lambdas))
	for _, lambda := range lambdas {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		c, err := r.coverageAt(lambda)
		if err != nil {
			return out, err
		}
		r.log.Info("λ taraması", "lambda", lambda, "kapsama", c)
		out = append(out, Point{Lambda: lambda, Coverage: c})
	}
	return out, nil
}

// Run, bisection döngüsünü yürütür ve λ*'ı `run_config`'e yazar.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	started := time.Now()
	cal := r.scenario.Calibration

	r.log.Info("kalibrasyon başlıyor",
		"run_id", r.runID, "örneklem", len(r.samples),
		"λ_aralığı", fmt.Sprintf("[%.2f, %.2f]", cal.LambdaMin, cal.LambdaMax),
		"hedef", cal.TargetCoverage, "tolerans", cal.Tolerance,
		"maks_iterasyon", cal.MaxIterations)

	result, err := bisect(ctx, r.coverageAt, bisectOptions{
		Lo:            cal.LambdaMin,
		Hi:            cal.LambdaMax,
		Target:        cal.TargetCoverage,
		Tolerance:     cal.Tolerance,
		MaxIterations: cal.MaxIterations,
		OnIteration: func(i int, lambda, coverage float64) {
			r.log.Info("kalibrasyon iterasyonu",
				"iterasyon", i, "lambda", lambda, "kapsama", coverage,
				"fark", coverage-cal.TargetCoverage)
		},
	})
	result.SampleSize = len(r.samples)
	if err != nil {
		return result, err
	}

	if !result.Converged {
		r.log.Warn("kalibrasyon yakınsamadı — λ* aralık ortası olarak yazılıyor",
			"lambda", result.Lambda, "kapsama", result.Coverage, "hedef", result.Target,
			"not", "λ yalnızca σ_eff'i ölçekler; TA penceresi λ'dan bağımsızdır (ADR-25)")
	}

	if err := r.pool.SetLambda(ctx, r.runID, result.Lambda); err != nil {
		return result, err
	}

	result.Duration = time.Since(started)
	return result, nil
}

// bisectOptions, ikili aramanın parametreleridir.
type bisectOptions struct {
	Lo, Hi        float64
	Target        float64
	Tolerance     float64
	MaxIterations int
	// OnIteration, her denemeden sonra çağrılır (günlük için); nil olabilir.
	OnIteration func(iteration int, lambda, coverage float64)
}

// bisect, monoton artan bir fonksiyonun hedefi verdiği noktayı arar.
//
// Değerlendirici (eval) ayrık tutulur: döngünün kendisi veritabanı, envanter
// ya da kütle modeli tanımaz ve sentetik bir fonksiyonla test edilebilir.
//
// Yakınsamazsa hata **dönmez**: son aralık ortası λ* olarak alınır,
// `Converged=false` işaretlenir ve izleme raporda kalır. Bu bilinçlidir —
// yakınsamama, modelin λ'ya duyarsız olduğu anlamına gelir ve bu raporlanması
// gereken bir bulgudur (ADR-25), gizlenecek bir hata değil.
func bisect(ctx context.Context, eval func(float64) (float64, error),
	opts bisectOptions) (Result, error) {

	result := Result{Target: opts.Target}

	switch {
	case !(opts.Lo < opts.Hi):
		return result, fmt.Errorf("kalibrasyon: λ aralığı geçersiz [%g, %g]", opts.Lo, opts.Hi)
	case !(opts.Target > 0 && opts.Target < 1):
		return result, fmt.Errorf("kalibrasyon: hedef kapsama (0,1) olmalı (%g)", opts.Target)
	case !(opts.Tolerance > 0):
		return result, fmt.Errorf("kalibrasyon: tolerans pozitif olmalı (%g)", opts.Tolerance)
	case opts.MaxIterations <= 0:
		return result, fmt.Errorf("kalibrasyon: iterasyon sınırı pozitif olmalı (%d)",
			opts.MaxIterations)
	}

	lo, hi := opts.Lo, opts.Hi
	for i := 0; i < opts.MaxIterations; i++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		lambda := (lo + hi) / 2
		coverage, err := eval(lambda)
		if err != nil {
			return result, err
		}

		result.Iterations = i + 1
		result.Trace = append(result.Trace, Point{Lambda: lambda, Coverage: coverage})
		if opts.OnIteration != nil {
			opts.OnIteration(i+1, lambda, coverage)
		}

		if math.Abs(coverage-opts.Target) < opts.Tolerance {
			result.Lambda, result.Coverage, result.Converged = lambda, coverage, true
			return result, nil
		}
		if coverage < opts.Target {
			lo = lambda // daha geniş belirsizlik gerekiyor
		} else {
			hi = lambda
		}
	}

	result.Lambda = (lo + hi) / 2
	coverage, err := eval(result.Lambda)
	if err != nil {
		return result, err
	}
	result.Coverage = coverage
	result.Trace = append(result.Trace, Point{Lambda: result.Lambda, Coverage: coverage})
	return result, nil
}

// PartitionOf, bir olayın bölümünü döndürür (tanı amaçlı).
//
// Kalibrasyon kümesinin gerçekten 'C' olduğunu doğrulamak için testlerde
// kullanılır; üretimde sorgu zaten `partition_key='C'` süzgeciyle gelir.
func (r *Runner) PartitionOf(eventID uuid.UUID) (split.Partition, error) {
	splitter, err := split.New(r.scenario.Run.Seed, r.scenario.Calibration.SplitRatio)
	if err != nil {
		return 0, err
	}
	return splitter.Of(eventID), nil
}
