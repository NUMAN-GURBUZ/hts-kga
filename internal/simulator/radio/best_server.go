// T-E02-12 — Best-server seçimi ve kapsama kararı (ADR-08).
//
// Telefon, alınan gücü en yüksek hücreye bağlanır:
//
//	RX(p, hücre) = EIRP − PathLoss(bağ, LOS) − A_beam(θ, φ) + S
//
// Alınan güç alıcı duyarlılığının altındaysa şebekeye bağlanılamaz; ADR-08/3
// gereği olay üretilmez, ground truth'a yalnızca covered=false yazılır.
//
// # Site düzeyinde gruplama
//
// LOS ve gölgeleme site düzeyinde çözülür (T-E02-11). Bu yüzden ağ, hücre
// listesi değil **site listesi** olarak tutulur: pahalı olan ortam çözümlemesi
// site başına bir kez yapılır, üç sektöre paylaştırılır. Doğrudan hücre
// üzerinden dönmek aynı işi üç kez yaptırırdı.
package radio

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Cell, best-server hesabı için gereken en küçük sektör görünümüdür.
//
// Envanterdeki inventory.Cell'in radyo katmanı izdüşümüdür: veritabanı
// biçimindeki alanlar (WGS84 konum, morfoloji metni) dışarıda bırakılır,
// yayılım modeli çözülmüş olarak taşınır. Dönüşüm çağıran katmanda yapılır —
// radyo paketi envanteri tanımaz.
type Cell struct {
	// ID, hücrenin kimliğidir (cells.cell_id ham baytları).
	ID [16]byte
	// AzimuthDeg, sektör azimutudur (0 / 120 / 240).
	AzimuthDeg float64
	// BeamWidthDeg, yatay 3 dB hüzme genişliğidir.
	BeamWidthDeg float64
	// TiltDeg, elektriksel aşağı eğimdir.
	TiltDeg float64
	// EIRPdBm, sektörün yayılan izotropik gücüdür.
	EIRPdBm float64
	// FreqMHz, taşıyıcı frekanstır.
	FreqMHz int
	// RMaxM, kapsama yarıçapı önhesabıdır (T-E02-05).
	RMaxM float64
}

// Site, tek bir baz istasyonu direği ve üzerindeki sektörlerdir.
type Site struct {
	// Source, ortam çözümlemesi için site kimliği, konumu ve modelidir.
	Source
	// AntHeightM, direk anten yüksekliğidir (tüm sektörler ortak).
	AntHeightM float64
	// Cells, direğin sektörleridir (tipik olarak 3).
	Cells []Cell
	// maxRMaxM, ön filtre için sektörlerin en büyük kapsama yarıçapıdır.
	maxRMaxM float64
}

// Network, bir koşunun şebeke envanterinin radyo katmanı görünümüdür.
// Değişmezdir; eşzamanlı kullanımda güvenlidir.
type Network struct {
	sites []Site
}

// NewNetwork, site listesinden bir şebeke kurar ve ön filtre verisini hazırlar.
func NewNetwork(sites []Site) (*Network, error) {
	if len(sites) == 0 {
		return nil, fmt.Errorf("şebeke: en az bir site gerekli")
	}

	prepared := make([]Site, len(sites))
	for i, s := range sites {
		if len(s.Cells) == 0 {
			return nil, fmt.Errorf("şebeke: site[%d] sektörsüz", i)
		}
		if s.Model == nil {
			return nil, fmt.Errorf("şebeke: site[%d] yayılım modeli yok", i)
		}
		if !isPositiveFinite(s.AntHeightM) {
			return nil, fmt.Errorf("şebeke: site[%d] anten yüksekliği geçersiz (%g)", i, s.AntHeightM)
		}

		for j, c := range s.Cells {
			if !isPositiveFinite(c.RMaxM) {
				return nil, fmt.Errorf("şebeke: site[%d] hücre[%d] r_max geçersiz (%g)", i, j, c.RMaxM)
			}
			if !isPositiveFinite(c.BeamWidthDeg) {
				return nil, fmt.Errorf("şebeke: site[%d] hücre[%d] hüzme genişliği geçersiz (%g)", i, j, c.BeamWidthDeg)
			}
			if c.FreqMHz <= 0 {
				return nil, fmt.Errorf("şebeke: site[%d] hücre[%d] frekans geçersiz (%d)", i, j, c.FreqMHz)
			}
			if c.RMaxM > s.maxRMaxM {
				s.maxRMaxM = c.RMaxM
			}
		}
		prepared[i] = s
	}
	return &Network{sites: prepared}, nil
}

// SiteCount, şebekedeki site sayısını döndürür.
func (n *Network) SiteCount() int { return len(n.sites) }

// CellCount, şebekedeki toplam sektör sayısını döndürür.
func (n *Network) CellCount() int {
	total := 0
	for _, s := range n.sites {
		total += len(s.Cells)
	}
	return total
}

// Serving, bir noktadaki best-server sonucudur.
//
// Değer tipidir ve işaretçi içermez: sıcak yolda yığın ayırma yapılmaz.
type Serving struct {
	// CellID, seçilen sektörün kimliğidir. Covered false ise anlamsızdır.
	CellID [16]byte
	// SiteKey, seçilen sektörün sitesidir.
	SiteKey uint64
	// RxDBm, seçilen sektörden alınan güçtür. Hiç aday yoksa −Inf.
	RxDBm float64
	// DistanceM, alıcı ile seçilen sitenin yatay mesafesidir.
	DistanceM float64
	// LOS, seçilen siteye görüş hattının açık olup olmadığıdır.
	LOS bool
	// Covered, alınan gücün alıcı duyarlılığını sağlayıp sağlamadığıdır (ADR-08).
	Covered bool
}

// Selector, verilen bir şebeke ve gölgeleme alanı için best-server hesaplar.
// Değişmezdir; 1000 goroutine tarafından kilitsiz paylaşılabilir.
type Selector struct {
	net              *Network
	field            *ShadowingField
	rxSensitivityDBm float64
	utHeightM        float64
}

// NewSelector, best-server seçicisini kurar.
//
// rxSensitivityDBm, senaryo config'inden gelir (network.rx_sensitivity_dbm) ve
// negatif olmalıdır.
func NewSelector(net *Network, field *ShadowingField, rxSensitivityDBm, utHeightM float64) (*Selector, error) {
	if net == nil {
		return nil, fmt.Errorf("best-server: şebeke zorunlu")
	}
	if field == nil {
		return nil, fmt.Errorf("best-server: gölgeleme alanı zorunlu")
	}
	if rxSensitivityDBm >= 0 {
		return nil, fmt.Errorf("best-server: rx duyarlılığı negatif olmalı (%g dBm)", rxSensitivityDBm)
	}
	if !isPositiveFinite(utHeightM) {
		return nil, fmt.Errorf("best-server: h_UT pozitif ve sonlu olmalı (%g)", utHeightM)
	}
	return &Selector{
		net:              net,
		field:            field,
		rxSensitivityDBm: rxSensitivityDBm,
		utHeightM:        utHeightM,
	}, nil
}

// RxSensitivityDBm, seçicinin kapsama eşiğini döndürür.
func (s *Selector) RxSensitivityDBm() float64 { return s.rxSensitivityDBm }

// Select, verilen noktadaki best-server'ı ve kapsama durumunu döndürür.
//
// Saf fonksiyondur: aynı nokta daima aynı sonucu verir (K10). Yığın ayırma
// yapmaz — tick başına ajan sayısı kadar çağrılır.
//
// Ön filtre: bir sitenin hiçbir sektörü noktayı kapsama yarıçapına almıyorsa
// site tamamen atlanır; pahalı ortam çözümlemesi hiç çalıştırılmaz.
func (s *Selector) Select(p geo.Point) Serving {
	best := Serving{RxDBm: math.Inf(-1)}

	for i := range s.net.sites {
		site := &s.net.sites[i]

		dx := p.X - site.ENU.X
		dy := p.Y - site.ENU.Y
		d2D := math.Hypot(dx, dy)

		// Ön filtre: kapsama yarıçapı dışındaki siteler elenir (ADR-03).
		if d2D > site.maxRMaxM {
			continue
		}

		// Ortam site başına BİR KEZ çözülür ve üç sektöre paylaştırılır.
		env := s.field.EnvironmentAt(site.Source, p)

		bearing := BearingDeg(dx, dy)
		elevation := ElevationDeg(site.AntHeightM-s.utHeightM, d2D)

		for j := range site.Cells {
			c := &site.Cells[j]
			if d2D > c.RMaxM {
				continue
			}

			link := Link{
				D2DM:    d2D,
				HBSm:    site.AntHeightM,
				HUTm:    s.utHeightM,
				FreqMHz: c.FreqMHz,
				LOS:     env.LOS,
			}

			pathLoss := site.Model.PathLossDB(link)
			beamLoss := AntennaAttenuationDB(
				bearing-c.AzimuthDeg, elevation, c.BeamWidthDeg, c.TiltDeg)

			rx := c.EIRPdBm - pathLoss - beamLoss + env.ShadowingDB

			if rx > best.RxDBm {
				best = Serving{
					CellID:    c.ID,
					SiteKey:   site.Key,
					RxDBm:     rx,
					DistanceM: d2D,
					LOS:       env.LOS,
				}
			}
		}
	}

	// ADR-08/3: eşiğin altındaysa telefon şebekeye bağlanamaz.
	best.Covered = best.RxDBm >= s.rxSensitivityDBm
	return best
}

// MaxRxDBm, verilen noktadaki en yüksek alınan gücü döndürür (dBm).
//
// Ajan yerleşiminde reddetme örneklemesinin kabul ölçütüdür (ADR-08/2):
// aday nokta bu değer duyarlılığın altındaysa reddedilir.
func (s *Selector) MaxRxDBm(p geo.Point) float64 {
	return s.Select(p).RxDBm
}

// IsCovered, verilen noktanın kapsama içinde olup olmadığını bildirir.
func (s *Selector) IsCovered(p geo.Point) bool {
	return s.Select(p).Covered
}
