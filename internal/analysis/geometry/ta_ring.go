// T-E03-04 — TA halkası, örtüşme ağırlığı ve boş kesişim geri düşüşü.
//
// Halkanın tanımı bu pakette değildir: `pkg/ta` hem simülatörün TA türetmesini
// (T-E02-15) hem buradaki halkayı tanımlar. İki taraf aynı fonksiyonu
// çağırdığı için "kayıt ne diyorsa halka onu içerir" garantisi yapısaldır,
// uyum meselesi değildir.
//
// Bu dosya halkayı ızgaraya bağlar: her hücrenin ağırlığı, alanının halkayla
// örtüşme oranıdır (ADR-18/3).

package geometry

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// Window, bir kaydın TA bilgisinin geometrik karşılığıdır.
//
// Sıfır değeri "TA yok" anlamına gelir ve her yere 1 ağırlık verir — TA'sız
// senaryolarda (B, D) ve geri düşüş durumunda kullanılan hâldir.
type Window struct {
	ring    ta.Ring
	enabled bool
}

// NoWindow, TA bilgisi olmayan kayıtlar için etkisiz pencereyi döndürür.
func NoWindow() Window { return Window{} }

// NewWindow, TA değerinden pencere kurar.
func NewWindow(taValue int, tech ta.Technology) (Window, error) {
	ring, err := ta.NewRing(taValue, tech)
	if err != nil {
		return Window{}, fmt.Errorf("TA penceresi: %w", err)
	}
	return Window{ring: ring, enabled: true}, nil
}

// Enabled, pencerenin ağırlığı etkileyip etkilemediğini bildirir.
//
// estimates.ta_used sütununa yazılan değer budur.
func (w Window) Enabled() bool { return w.enabled }

// Ring, pencerenin halkasını döndürür. Etkisiz pencerede sıfır halkadır.
func (w Window) Ring() ta.Ring { return w.ring }

// Weight, tek bir ızgara hücresinin TA ağırlığıdır [0,1].
//
// Pencere etkisizse 1 döner: TA'sız senaryoda radyal bileşen yalnızca kapsama
// olasılığından gelir.
func (w Window) Weight(cell []geo.Point, site geo.Point) float64 {
	if !w.enabled {
		return 1
	}
	return OverlapRatio(cell, site, w.ring.InnerM, w.ring.OuterM)
}

// Weights, bir hücre kümesinin TA ağırlıklarını üretir ve boş kesişim
// durumunda geri düşüşü uygular (plan T-E03-04, "∅ fallback").
//
// Halka arama bölgesiyle hiç kesişmiyorsa — hiçbir hücrenin örtüşme oranı
// pozitif değilse — TA **yok sayılır**: tüm ağırlıklar 1 olur ve dönen pencere
// devre dışıdır. Bu, tahmini kaybetmek yerine bilgiyi düşürmektir; hangi
// tahminlerin TA'sız üretildiği `ta_used = false` ile izlenebilir kalır.
//
// Dönen ikinci değer, kaydedilecek pencere durumudur.
func (w Window) Weights(cells [][]geo.Point, site geo.Point) ([]float64, Window) {
	weights := make([]float64, len(cells))

	if !w.enabled {
		for i := range weights {
			weights[i] = 1
		}
		return weights, w
	}

	any := false
	for i, cell := range cells {
		weights[i] = w.Weight(cell, site)
		if weights[i] > 0 {
			any = true
		}
	}
	if any {
		return weights, w
	}

	// Boş kesişim: TA'yı at, bölgeyi olduğu gibi bırak.
	for i := range weights {
		weights[i] = 1
	}
	return weights, NoWindow()
}
