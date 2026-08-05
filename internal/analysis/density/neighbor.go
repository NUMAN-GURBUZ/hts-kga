// T-E03-07 — Komşu hücre kısıtı (ADR-03).
//
// Kayıt, `s` hücresinin **best server** olduğunu söyler. Öyleyse `p` noktasında
// s'nin alınan gücü tüm komşularınkinden büyük olmalıydı. Gölgeleme rastgele
// olduğundan bu bir olasılıktır ve hesaplanabilir:
//
//	Δ(p)     = P_s(p) − max_n P_n(p)                 # dB marj
//	w_nbr(p) = Φ( Δ(p) / (σ_eff · √2) )
//
// √2, iki bağımsız log-normal gölgelemenin farkının standart sapmasından gelir.
// Δ büyükse serving baskındır (w → 1); negatifse "burada olsaydı başka hücreye
// bağlanırdı" demektir (w → 0) ve o bölge doğal olarak elenir.
//
// # Komşu sinyali simüle edilmez
//
// Analiz, komşuların gerçekleşmiş gölgelemesini bilmez; envanterdeki
// deterministik parametrelerden hesaplar (ADR-19 log-alanı karışımı). Bilgi
// asimetrisi korunur.
//
// # Ön filtre neden yetmiyor (G5 + T-E02-05 revizyonu)
//
// Plan E.2 komşu kümesini "p'yi kapsama yarıçapı içine alan hücreler" diye
// tanımlar ve tipik |N| = 3–8 bekler. Link budget revizyonundan sonra kırsalda
// r_max ≈ 31 km, alan yarıçapı ise 20 km: **her hücre her noktayı kapsıyor**,
// yarıçap filtresi hiçbir şey elemiyor. Bu yüzden seçim yarıçapa değil güce
// göre yapılır — bölge merkezindeki alınan güce göre en güçlü N komşu alınır
// (`analysis.neighbor_max_count`, varsayılan 8). Eşitlikte hücre kimliği
// sıralar: seçim koşudan koşuya aynı kalmalıdır (K10).
package density

import (
	"math"
	"sort"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// sqrt2, iki bağımsız gölgeleme farkının standart sapma çarpanıdır.
var sqrt2 = math.Sqrt2

// SelectNeighbours, bölge için komşu adaylarını seçer.
//
// regionCenter, sıralamanın yapıldığı referans noktasıdır (dilimin ağırlık
// merkezi ya da direk konumu). Seçim bölge başına **bir kez** yapılır: ızgara
// hücresi başına yeniden sıralamak, kırsalda 62.000 hücre × 326 aday demek
// olurdu.
//
// maxCount ≤ 0 ise komşu kısıtı uygulanmaz (boş küme döner).
func SelectNeighbours(inv *params.Inventory, serving params.Cell, regionCenter geo.Point,
	maxCount int, utHeightM float64) []params.Cell {

	if inv == nil || maxCount <= 0 {
		return nil
	}

	type ranked struct {
		cell   params.Cell
		powerD float64
	}

	candidates := make([]ranked, 0, maxCount*4)
	for _, c := range inv.Cells() {
		if c.ID == serving.ID {
			continue
		}
		// Yarıçap ön filtresi: bölge merkezi hücrenin erişiminin tamamen
		// dışındaysa aday değildir. Kentselde eler, kırsalda elemez —
		// asıl seçim güce göredir.
		if geo.Distance(regionCenter, c.Site) > c.RMaxM {
			continue
		}
		candidates = append(candidates, ranked{cell: c, powerD: ReceivedPowerDBm(c, regionCenter, utHeightM)})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].powerD != candidates[j].powerD {
			return candidates[i].powerD > candidates[j].powerD
		}
		return candidates[i].cell.ID.String() < candidates[j].cell.ID.String()
	})

	if len(candidates) > maxCount {
		candidates = candidates[:maxCount]
	}

	out := make([]params.Cell, len(candidates))
	for i, c := range candidates {
		out[i] = c.cell
	}
	return out
}

// NeighbourMarginDB, verilen noktadaki serving–komşu güç marjıdır Δ(p).
//
// Komşu yoksa +Inf döner: kısıt uygulanmaz, w_nbr = 1.
func NeighbourMarginDB(serving params.Cell, neighbours []params.Cell, p geo.Point, utHeightM float64) float64 {
	if len(neighbours) == 0 {
		return math.Inf(1)
	}
	best := math.Inf(-1)
	for _, n := range neighbours {
		if power := ReceivedPowerDBm(n, p, utHeightM); power > best {
			best = power
		}
	}
	return ReceivedPowerDBm(serving, p, utHeightM) - best
}

// Neighbour, komşu kısıtı ağırlığıdır w_nbr(p) ∈ [0,1] (ADR-03).
//
// Komşu kümesi boşsa 1 döner — plan E.2'nin "|N| = 0 ise w_nbr(p) = 1" kuralı.
func Neighbour(serving params.Cell, neighbours []params.Cell, p geo.Point, cfg Config) float64 {
	margin := NeighbourMarginDB(serving, neighbours, p, cfg.UTHeightM)
	if math.IsInf(margin, 1) {
		return 1
	}
	return NormalCDF(margin / (cfg.SigmaEffDB() * sqrt2))
}
