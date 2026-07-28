// Package split, olayları kalibrasyon ve doğrulama kümelerine ayırır.
//
// # Bu paketin var oluş nedeni
//
// Ayrım iki kez, iki ayrı serviste hesaplanır:
//
//	simülatör (T-E02-16) : ground_truth.partition_key sütununu yazar
//	doğrulama (S5)       : replay sürücüsü kalibrasyon örneklemini seçer
//
// İkisi ayrı yazılsaydı — farklı hash, farklı eşik yönü, farklı tohum
// karıştırma — kalibrasyon kümesiyle doğrulama kümesi kısmen örtüşürdü.
// Bu, K4'ün ("karışık sorgu → hata") kod düzeyinde engellemeye çalıştığı
// tam olarak o şeydir: modelin kendi kalibrasyon verisinde ölçülmesi.
// Ayrım bu yüzden tek bir yerde tanımlıdır.
//
// # Kör testle ilişkisi (ADR-14 ↔ ADR-04)
//
// ADR-14 örneklemeyi partition_key üzerinden tanımlar, ama o sütun
// ground_truth tablosundadır ve analiz servisi o tabloyu **göremez** (K6 kör
// testi). Çelişki, ayrımı yeniden türetilebilir yaparak çözülür: bölüm
// yalnızca event_id ve koşu tohumundan hesaplanır.
//
// Bu bir sızıntı değildir. Bölüm anahtarı olayın **konumu** hakkında hiçbir
// şey taşımaz; hangi kümeye düştüğünü söyler, nerede olduğunu değil. Kör test
// gerçek konumu gizler, olay kimliğini değil — olay kimliği zaten
// hts_records'ta analizin önündedir.
//
// # Dondurulmuş tanım
//
//	u        = SHA-256( event_id[16] ‖ seed_big_endian[8] )[0:8] → [0,1)
//	bölüm    = u < calibration_ratio ? 'C' : 'V'
//
// SHA-256 seçimi bilinçlidir: bu bir güvenlik gereksinimi değil, **taşınabilir
// bir sözleşme** gereksinimidir. Ayrımın SQL'de, Python'da veya bir denetçinin
// elinde yeniden hesaplanabilmesi gerekir; standart bir özet bunu mümkün
// kılar, projeye özgü bir karıştırıcı kılmaz. Maliyet önemsizdir: olay başına
// bir özet, 300.000 olayda toplam ~30 ms.
package split

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// Partition, bir olayın düştüğü kümedir.
//
// Değerler ground_truth.partition_key CHAR(1) sütunuyla birebir aynıdır.
type Partition byte

const (
	// Calibration, λ'nın aranacağı kümedir (ADR-02).
	Calibration Partition = 'C'
	// Validation, nihai ölçümün yapılacağı kümedir (K1–K3).
	Validation Partition = 'V'
)

// Valid, bölümün tanımlı olup olmadığını bildirir.
func (p Partition) Valid() bool { return p == Calibration || p == Validation }

// String, bölümün tek harfli gösterimini döndürür.
func (p Partition) String() string {
	if !p.Valid() {
		return fmt.Sprintf("Partition(%d)", byte(p))
	}
	return string(byte(p))
}

// DefaultCalibrationRatio, plan BÖLÜM L'deki calibration.split_ratio
// varsayılanıdır (80/20).
const DefaultCalibrationRatio = 0.80

// uniformDenominator, 52 bitlik mantisi (0,1) aralığına ölçekler.
const (
	mantissaShift = 12
	mantissaScale = 1.0 / (1 << 52)
)

// Splitter, sabit tohum ve orana bağlı bir bölümleyicidir.
//
// Değişmezdir; eşzamanlı kullanımda güvenlidir.
type Splitter struct {
	seedBytes [8]byte
	ratio     float64
}

// New, koşu tohumu ve kalibrasyon oranından bir bölümleyici kurar.
//
// ratio, config'deki calibration.split_ratio değeridir; sabit olarak
// gömülmez çünkü plan onu senaryo config'inde tanımlar.
func New(seed int64, calibrationRatio float64) (Splitter, error) {
	if math.IsNaN(calibrationRatio) || calibrationRatio < 0 || calibrationRatio > 1 {
		return Splitter{}, fmt.Errorf(
			"bölümleme: kalibrasyon oranı [0,1] aralığında olmalı (%g)", calibrationRatio)
	}
	s := Splitter{ratio: calibrationRatio}
	binary.BigEndian.PutUint64(s.seedBytes[:], uint64(seed))
	return s, nil
}

// Ratio, kalibrasyon oranını döndürür.
func (s Splitter) Ratio() float64 { return s.ratio }

// Of, olayın hangi kümeye düştüğünü döndürür.
//
// Yalnızca olay kimliğine ve koşu tohumuna bağlıdır: konum, zaman veya ajan
// bilgisi kullanılmaz.
func (s Splitter) Of(eventID uuid.UUID) Partition {
	if s.Uniform(eventID) < s.ratio {
		return Calibration
	}
	return Validation
}

// Uniform, olayın [0,1) aralığındaki bölümleme koordinatını döndürür.
//
// Dışa açıktır çünkü ayrımın denetlenebilir olması gerekir: bir denetçi aynı
// değeri bağımsız olarak hesaplayıp eşikle karşılaştırabilmelidir.
func (s Splitter) Uniform(eventID uuid.UUID) float64 {
	var name [len(uuid.UUID{}) + 8]byte
	copy(name[:16], eventID[:])
	copy(name[16:], s.seedBytes[:])

	sum := sha256.Sum256(name[:])
	h := binary.BigEndian.Uint64(sum[:8])
	return float64(h>>mantissaShift) * mantissaScale
}
