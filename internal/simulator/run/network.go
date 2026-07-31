// T-E02-* tamamlayıcısı (G1) — envanterin radyo katmanı görünümü.
//
// `internal/simulator/radio` envanteri **tanımaz** (paket başlığındaki kural):
// veritabanı biçimindeki alanları (WGS84 konum, morfoloji metni) görmez,
// yayılım modelini çözülmüş olarak alır. Dönüşüm bu yüzden çağıran katmanda,
// yani burada yapılır.
//
// Eşleme sektörleri **siteye göre gruplar**: gölgeleme ve LOS durumu site
// başınadır (ADR-19), sektör başına değil. Aynı direğin üç sektörü aynı
// `Source` anahtarını paylaşmalıdır, aksi hâlde üç farklı gölgeleme
// gerçekleşmesi üretilir ve best-server seçimi fiziksel olarak anlamsızlaşır.

package run

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
)

// buildNetwork, envanteri radyo katmanı şebekesine çevirir.
//
// Sıra korunur: siteler `Layout.Sites` sırasında, sektörler envanter
// sırasında dizilir. Best-server eşitlik durumunda ilk adayı seçtiği için
// bu sıra sonucun bir parçasıdır (K10).
func buildNetwork(inv *inventory.Inventory) (*radio.Network, error) {
	if inv == nil {
		return nil, fmt.Errorf("şebeke dönüşümü: envanter zorunlu")
	}
	if len(inv.Layout.Sites) == 0 {
		return nil, fmt.Errorf("şebeke dönüşümü: envanterde site yok")
	}

	index := make(map[string]int, len(inv.Layout.Sites))
	sites := make([]radio.Site, 0, len(inv.Layout.Sites))

	for _, s := range inv.Layout.Sites {
		index[s.ID.String()] = len(sites)
		sites = append(sites, radio.Site{
			Source: radio.Source{
				// AxialKey — run_id'den bağımsız (ADR-35). s.ID (UUID)
				// bilinçli olarak run_id içerir (ADR-05, DB birincil anahtar
				// çakışmasını önler) ve gölgeleme/LOS anahtarı için
				// KULLANILMAMALIDIR — kullanılırsa aynı fiziksel site aynı
				// seed'le bile koşudan koşuya farklı gölgeleme alır ve K10
				// kırılır.
				Key: radio.AxialKey(s.Axial.Q, s.Axial.R),
				ENU: s.ENU,
			},
		})
	}

	for i, c := range inv.Cells {
		pos, ok := index[c.SiteID.String()]
		if !ok {
			return nil, fmt.Errorf("şebeke dönüşümü: hücre[%d] (%s) sitesi %s yerleşimde yok",
				i, c.ID, c.SiteID)
		}
		site := &sites[pos]

		model, err := rf.ModelFor(c.ModelType)
		if err != nil {
			return nil, fmt.Errorf("şebeke dönüşümü: hücre[%d] (%s) modeli çözülemedi: %w",
				i, c.ID, err)
		}

		// Model ve anten yüksekliği site düzeyindedir; ilk sektör belirler,
		// sonrakiler uyuşmak zorundadır — uyuşmazlık envanter hatasıdır.
		if len(site.Cells) == 0 {
			site.Source.Model = model
			site.AntHeightM = c.AntHeightM
		} else if site.AntHeightM != c.AntHeightM {
			return nil, fmt.Errorf("şebeke dönüşümü: site %s sektörleri farklı anten yüksekliği "+
				"taşıyor (%g ≠ %g)", c.SiteID, site.AntHeightM, c.AntHeightM)
		}

		site.Cells = append(site.Cells, radio.Cell{
			ID:           c.ID,
			AzimuthDeg:   c.Azimuth,
			BeamWidthDeg: c.BeamWidth,
			TiltDeg:      c.TiltDeg,
			EIRPdBm:      c.EIRPdBm,
			FreqMHz:      c.FreqMHz,
			RMaxM:        c.RMaxM,
		})
	}

	return radio.NewNetwork(sites)
}
