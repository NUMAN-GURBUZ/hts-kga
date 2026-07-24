// T-E02-04 — Taşıyıcı frekans ataması (ADR-17 freq_distribution).
//
// Profil, morfolojiye göre band ağırlıkları verir (kentsel: yüksek bantlar
// ağırlıklı, kırsal: düşük bantlar ağırlıklı). Sektörlere band ataması bu
// ağırlıklara göre, koşu tohumundan türeyen bağımsız akışla yapılır (K10).
package inventory

import (
	"fmt"
	"sort"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// bandSelector, ağırlıklı band seçimi yapar.
//
// Kümülatif ağırlık dizisi üzerinde ikili arama uygular: O(log n) seçim,
// ayırma (allocation) yok.
type bandSelector struct {
	bands []config.FreqBand
	cum   []float64 // kümülatif ağırlıklar; son eleman = 1.0
}

// newBandSelector, band dağılımından seçici kurar.
// Dağılım ValidateFreqBands kurallarına uymak zorundadır (toplam ağırlık = 1).
func newBandSelector(bands []config.FreqBand) (*bandSelector, error) {
	if err := config.ValidateFreqBands(bands); err != nil {
		return nil, fmt.Errorf("frekans ataması: %w", err)
	}

	// Girdi diliminin sahipliğini almamak için kopyalanır.
	cp := make([]config.FreqBand, len(bands))
	copy(cp, bands)

	cum := make([]float64, len(cp))
	var running float64
	for i, b := range cp {
		running += b.Weight
		cum[i] = running
	}
	// Kayan nokta birikimi son sınırı 1'in altında bırakabilir; u < 1 için
	// daima bir band bulunmasını garantiye al.
	cum[len(cum)-1] = 1.0

	return &bandSelector{bands: cp, cum: cum}, nil
}

// pick, [0,1) aralığındaki u değerine karşılık gelen bandı döndürür (MHz).
//
// Ters kümülatif dağılım (inverse CDF) yöntemi: u değeri hangi ağırlık
// dilimine düşüyorsa o band seçilir.
func (s *bandSelector) pick(u float64) int {
	i := sort.SearchFloat64s(s.cum, u)
	if i >= len(s.bands) {
		i = len(s.bands) - 1
	}
	return s.bands[i].MHz
}
