// T-E03-08 — Kütle normalizasyonu.
//
// Ham ağırlıkların çarpımı bir olasılık dağılımı değildir; toplamı 1 olacak
// şekilde ölçeklenmelidir. PBT değişmezi 1: |Σ mass − 1| < 1e-9.
//
// # Toplama sırası sabittir
//
// Kayan nokta toplaması birleşmeli (associative) değildir: aynı sayılar farklı
// sırayla toplanırsa sonuç son bitlerde ayrışır. Izgara hücreleri (r, q)
// sırasına göre üretildiği (T-E03-05) ve bu sıra korunduğu için toplam koşudan
// koşuya bit düzeyinde aynıdır (K10).
//
// Ayrıca **Kahan toplaması** kullanılır: 62.000 hücrelik kırsal bir bölgede
// naif toplamın biriken hatası 1e-9 toleransına yaklaşabilir. Kahan, hatayı
// hücre sayısından bağımsız kılar.

package density

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// MassTolerance, Σ mass = 1 değişmezinin toleransıdır (plan BÖLÜM I, PBT #1).
const MassTolerance = 1e-9

// Normalize, ham ağırlıkları toplamı 1 olan kütleye çevirir (yerinde).
//
// cells ve weights aynı sırada ve aynı uzunlukta olmalıdır. Toplam sıfırsa
// hata döner: bu, bölgede hiçbir noktanın kaydı açıklayamadığı anlamına gelir
// ve sessizce sıfır kütle üretmek yerine görünür kılınmalıdır.
func Normalize(cells []geo.Axial, weights []float64) (map[geo.Axial]float64, error) {
	if len(cells) != len(weights) {
		return nil, fmt.Errorf("normalizasyon: hücre (%d) ve ağırlık (%d) sayısı eşleşmiyor",
			len(cells), len(weights))
	}
	if len(cells) == 0 {
		return nil, fmt.Errorf("normalizasyon: bölge boş")
	}

	total := KahanSum(weights)
	if !(total > 0) || math.IsInf(total, 0) || math.IsNaN(total) {
		return nil, fmt.Errorf("normalizasyon: ağırlık toplamı geçersiz (%g) — "+
			"bölgedeki hiçbir nokta kaydı açıklamıyor", total)
	}

	mass := make(map[geo.Axial]float64, len(cells))
	for i, a := range cells {
		if weights[i] < 0 || math.IsNaN(weights[i]) {
			return nil, fmt.Errorf("normalizasyon: hücre %v ağırlığı geçersiz (%g)", a, weights[i])
		}
		mass[a] += weights[i] / total
	}
	return mass, nil
}

// KahanSum, telafi edilmiş (compensated) toplamdır.
//
// Naif toplamda hata O(n·ε) ile büyür; Kahan'da O(ε) sabit kalır. 62.000
// hücrelik bir bölgede fark, 1e-9 toleransının altında kalmakla aşmak
// arasındaki farktır.
func KahanSum(values []float64) float64 {
	sum, compensation := 0.0, 0.0
	for _, v := range values {
		y := v - compensation
		t := sum + y
		compensation = (t - sum) - y
		sum = t
	}
	return sum
}

// TotalMass, kütle haritasının toplamını döndürür (değişmez denetimi).
//
// Harita yinelemesi Go'da rastgele sıralıdır; bu yüzden toplam önce kararlı
// bir sıraya alınmaz — yalnızca **doğrulama** amaçlıdır, üretimde kullanılmaz.
func TotalMass(mass map[geo.Axial]float64) float64 {
	values := make([]float64, 0, len(mass))
	for _, v := range mass {
		values = append(values, v)
	}
	return KahanSum(values)
}
