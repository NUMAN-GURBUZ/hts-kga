// T-E04-05 — 80/20 ayrımının kod düzeyinde zorlanması (K4).
//
// # Neden bir tip
//
// K4'ün ölçütü şudur: "kalibrasyon ve doğrulama kümelerinin karıştığı bir
// sorgu **hata** vermelidir". Bunu bir çalışma zamanı denetimiyle sağlamak
// mümkündü (`if pkey != "C" && pkey != "V" { return err }`), ama o denetim
// yalnızca **yanlış değeri** yakalar; asıl tehlike `IN ('C','V')` gibi
// geçerli ama karışık bir sorgudur.
//
// Çözüm: bölüm anahtarı dizge değil, alanı dışa kapalı bir **tiptir**. Paket
// dışından ancak iki değeri üretilebilir (Calibration, Validation) ve sorgu
// kurucular tek bir değer alır. "Her ikisi" diye bir değer yoktur; karışık
// sorgu yazılamaz, çünkü onu ifade edecek bir tip yoktur.
//
// # Modelin kendi verisinde ölçülmesi
//
// Bu, bilimsel geçersizliğin en yaygın biçimidir: λ 'C' kümesinde kalibre
// edilir, sonra aynı küme üzerinde "kapsama %90 çıktı" diye raporlanır. Kümeyi
// ayıran mekanizma (pkg/split) zaten vardır; buradaki tip, o ayrımın ölçüm
// katmanında **kazara** delinmesini engeller.

package metrics

import "fmt"

// PartitionKey, ölçümün yapıldığı olay kümesidir.
//
// Alan dışa kapalıdır: sıfır değeri geçersizdir ve paket dışında yeni bir
// değer üretilemez.
type PartitionKey struct {
	value string
}

var (
	// Calibration, λ'nın kalibre edildiği kümedir ('C').
	Calibration = PartitionKey{value: "C"}
	// Validation, final ölçümün yapıldığı kümedir ('V') — K1–K3 buradan çıkar.
	Validation = PartitionKey{value: "V"}
)

// String, veritabanı gösterimini döndürür ('C' | 'V').
func (p PartitionKey) String() string { return p.value }

// Valid, anahtarın kullanılabilir olup olmadığını bildirir.
//
// Sıfır değeri geçersizdir: `var p PartitionKey` ile kurulan bir anahtar
// sorguya giremez.
func (p PartitionKey) Valid() bool {
	return p.value == "C" || p.value == "V"
}

// ParsePartition, metinden bölüm anahtarını çözer.
//
// Yalnızca tek bir değer kabul eder. "C,V", "both", "*" gibi girdiler
// reddedilir — K4'ün "karışık sorgu → hata" ölçütü burada da geçerlidir.
func ParsePartition(s string) (PartitionKey, error) {
	switch s {
	case "C":
		return Calibration, nil
	case "V":
		return Validation, nil
	default:
		return PartitionKey{}, fmt.Errorf(
			"bölüm anahtarı 'C' veya 'V' olmalı, karışık küme kabul edilmez (%q) — K4", s)
	}
}

// validate, sorgu kurucularının ortak ön denetimidir.
func (p PartitionKey) validate() error {
	if !p.Valid() {
		return fmt.Errorf("ölçüm: bölüm anahtarı belirtilmemiş " +
			"(kalibrasyon ve doğrulama kümeleri karıştırılamaz — K4)")
	}
	return nil
}
