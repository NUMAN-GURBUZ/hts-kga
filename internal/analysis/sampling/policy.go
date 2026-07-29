// Package sampling, analiz motorunun hangi olayları işleyeceğini belirler
// (ADR-14, ADR-24).
//
// # Neden filtre kütle hesabından ÖNCE
//
// Sprint 4'ün analiz motoru her kaydı işliyordu. 300.000 olaylık bir koşuda bu,
// 1,5 milyon `estimates` satırı ve D senaryosunda ~2,4 saat demek — ADR-14
// bunu açıkça reddediyor. Filtre yazma anında uygulansaydı yalnızca disk
// kazanılırdı; CPU'nun tamamı yine harcanırdı. Bu yüzden filtre, kaydı
// motora vermeden önce çalışır.
//
// # Kör test bozulmadan C/V ayrımı
//
// Örnekleme `partition_key` üzerinden tanımlıdır ama o sütun `ground_truth`
// tablosundadır ve analiz servisi o tabloyu **göremez** (K6). Çelişki
// `pkg/split` ile çözülür: bölüm yalnızca `event_id` ve koşu tohumundan
// hesaplanır, veritabanına hiç gidilmez.
//
// Bu bir sızıntı değildir. Bölüm anahtarı olayın **konumu** hakkında hiçbir
// şey taşımaz; hangi kümeye düştüğünü söyler, nerede olduğunu değil.
//
// # Üç mod (ADR-14)
//
//	validation   'V' kümesinin tamamı        — final ölçüm (K1–K3)
//	calibration  'C' içinden ~N olay          — bisection döngüsü (ADR-02)
//	full         her olay                     — K9 ölçekleme ölçümü (S8)
package sampling

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
)

// Mode, örnekleme modudur (senaryo config'i / ortam değişkeni).
type Mode string

const (
	// ModeValidation, 'V' kümesinin tamamını seçer.
	ModeValidation Mode = "validation"
	// ModeCalibration, 'C' içinden seyreltilmiş örneği seçer.
	ModeCalibration Mode = "calibration"
	// ModeFull, hiçbir olayı elemez.
	ModeFull Mode = "full"
)

// Valid, modun tanımlı olup olmadığını bildirir.
func (m Mode) Valid() bool {
	return m == ModeValidation || m == ModeCalibration || m == ModeFull
}

// ParseMode, metinden modu çözer.
func ParseMode(s string) (Mode, error) {
	m := Mode(s)
	if !m.Valid() {
		return "", fmt.Errorf("örnekleme modu 'validation', 'calibration' veya 'full' olmalı (%q)", s)
	}
	return m, nil
}

// Policy, bir olayın analiz edilip edilmeyeceğine karar verir.
//
// Değişmezdir ve saf bir fonksiyondur: aynı olay kimliği daima aynı cevabı
// alır, çağrı sırasından ve süreçten bağımsız olarak (K10).
type Policy struct {
	mode     Mode
	splitter split.Splitter
	// keepRatio, kalibrasyon modunda 'C' kümesinden tutulacak orandır (0,1].
	keepRatio float64
	seed      int64
}

// Config, politika kurulum parametreleridir.
type Config struct {
	// Mode, örnekleme modudur.
	Mode Mode
	// Seed, koşu tohumudur (run_config.seed) — bölümleme ve seyreltme aynı
	// tohumdan türetilir, böylece koşu tekrarlanabilir (K10).
	Seed int64
	// SplitRatio, kalibrasyon/doğrulama oranıdır (calibration.split_ratio).
	SplitRatio float64
	// TargetEvents, kalibrasyon modunda hedeflenen olay sayısıdır
	// (analysis.sample.calibration_events, varsayılan 5.000).
	TargetEvents int
	// ExpectedTotalEvents, koşunun beklenen toplam olay sayısıdır.
	//
	// Akışta gerçek sayı önceden bilinemez; seyreltme oranı bu tahminden
	// hesaplanır. Tahmin ajan × gün × günlük olay hedefinden gelir ve
	// Poisson dalgalanması ±%5 mertebesindedir — 5.000 hedefinde bu, 250
	// olaylık bir sapma demektir ve kalibrasyonu etkilemez.
	ExpectedTotalEvents int
}

// New, örnekleme politikasını kurar.
func New(cfg Config) (Policy, error) {
	if !cfg.Mode.Valid() {
		return Policy{}, fmt.Errorf("örnekleme: mod geçersiz (%q)", cfg.Mode)
	}

	splitter, err := split.New(cfg.Seed, cfg.SplitRatio)
	if err != nil {
		return Policy{}, fmt.Errorf("örnekleme: %w", err)
	}

	p := Policy{mode: cfg.Mode, splitter: splitter, seed: cfg.Seed, keepRatio: 1}

	if cfg.Mode == ModeCalibration {
		if cfg.TargetEvents <= 0 {
			return Policy{}, fmt.Errorf("örnekleme: kalibrasyon hedefi pozitif olmalı (%d)",
				cfg.TargetEvents)
		}
		if cfg.ExpectedTotalEvents <= 0 {
			return Policy{}, fmt.Errorf("örnekleme: beklenen olay sayısı pozitif olmalı (%d)",
				cfg.ExpectedTotalEvents)
		}

		expectedC := float64(cfg.ExpectedTotalEvents) * cfg.SplitRatio
		p.keepRatio = math.Min(1, float64(cfg.TargetEvents)/expectedC)
	}
	return p, nil
}

// Mode, politikanın modunu döndürür.
func (p Policy) Mode() Mode { return p.mode }

// KeepRatio, kalibrasyon seyreltme oranını döndürür (diğer modlarda 1).
func (p Policy) KeepRatio() float64 { return p.keepRatio }

// Includes, olayın analiz edilip edilmeyeceğini bildirir.
func (p Policy) Includes(eventID uuid.UUID) bool {
	switch p.mode {
	case ModeFull:
		return true

	case ModeValidation:
		return p.splitter.Of(eventID) == split.Validation

	case ModeCalibration:
		if p.splitter.Of(eventID) != split.Calibration {
			return false
		}
		return p.thin(eventID) < p.keepRatio

	default:
		return false
	}
}

// thin, seyreltme için [0,1) aralığında deterministik bir değer üretir.
//
// `split.Uniform`'dan **ayrı** bir karma kullanılır: aynı değer hem bölüm
// hem seyreltme için kullanılsaydı, seyreltme 'C' kümesinin yalnızca
// başlangıcından (uniform değeri küçük olanlardan) seçerdi ve örneklem
// bölümün rastgele bir alt kümesi olmazdı.
// Tohum **başa** yazılır ve sonuç ayrıca karıştırılır (splitmix64
// sonlandırıcısı). FNV-1a'da çarpma bitleri düşükten yükseğe taşır: tohum
// sona eklenseydi yalnızca alt bitleri etkilerdi ve sıralama üst 53 bitten
// türetildiği için **tohum örneklemi hiç değiştirmezdi**. Bu, testle
// yakalanan gerçek bir hataydı.
func (p Policy) thin(eventID uuid.UUID) float64 {
	var buf [24]byte
	binary.BigEndian.PutUint64(buf[:8], uint64(p.seed))
	copy(buf[8:], eventID[:])

	h := mix64(fnv1a(buf[:]))
	return float64(h>>11) / float64(uint64(1)<<53)
}

// fnv1a, 64 bitlik FNV-1a karmasıdır.
//
// Kriptografik güç gerekmez; gereken şey taşınabilir ve deterministik
// olmasıdır. `pkg/split` SHA-256 kullanır çünkü orada ayrım bir **sözleşmedir**
// (SQL'de de yeniden üretilebilmelidir); seyreltme ise yalnızca analiz
// motorunun iç kararıdır.
func fnv1a(data []byte) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

// mix64, splitmix64 sonlandırıcısıdır: girdideki her bit farkını tüm
// bitlere yayar (avalanche).
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
