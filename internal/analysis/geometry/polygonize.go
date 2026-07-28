// T-E03-11 — Hücre kümesi → MULTIPOLYGON (kenar izleme).
//
// # Neden PostGIS ST_Union değil (ADR-21)
//
// Plan BÖLÜM E.3 birleştirmeyi `ST_Union` ile tarif eder. Ölçüm bunun bedelini
// gösteriyor: kırsal senaryoda olay başına bölge 8.872 hücredir (ADR-18/6
// benchmark'ı, 250 m çözünürlük). Kontur başına binlerce hex poligonunu
// veritabanına gönderip birleştirtmek, kalibrasyonun 12 iterasyonunda
// (ADR-02) PostGIS'i darboğaz yapar ve — daha kötüsü — sonucu GEOS sürümüne
// bağlar: K10 "aynı seed, iki koşu, bit düzeyinde aynı" der.
//
// Hex kafeste birleştirme zaten bir kesişim problemi değildir. Komşusu kümede
// olmayan her kenar sınırdadır; sınır kenarlarını uç uca eklemek yeterlidir.
// Sonuç tam (approximation yok), O(n) ve tamamen deterministiktir.
//
// # Hex kafeste köşe ikilemi yoktur
//
// Kare ızgarada iki hücre yalnızca köşeden değebilir (dama tahtası deseni);
// sınır izlemesi orada "hangi kenardan devam edilecek" ikilemine düşer ve
// üretilen halka kendine değdiği için `ST_IsValid` düşer.
//
// Altıgen kafeste bu **imkânsızdır**: her köşede tam üç hücre buluşur ve bu üç
// hücre birbirine ikişer ikişer komşudur. Dolayısıyla bir köşede kümeden kaç
// hücre bulunursa bulunsun (1, 2 ya da 3), o köşeye değen sınır kenarı sayısı
// daima 0 veya 2'dir — biri giren, biri çıkan. İzleme tek yönlüdür, seçim
// kuralı gerekmez, üretilen halkalar kendine değmez.
//
// Bu yüzden çıktı yapı gereği geçerlidir. Yazım anındaki `ST_IsValid` denetimi
// (T-E03-13) yine de durur: `repaired_ratio` metriği (K8) ancak ölçülerek
// iddia edilebilir, varsayılarak değil.
//
// # Köşeler kayan noktada değil, tam sayı kafeste eşleşir
//
// Komşu iki hücrenin paylaştığı köşe iki farklı merkezden hesaplanır; kayan
// nokta sonuçları son bitlerde ayrışabilir ve halkalar birleşmezdi. Bu yüzden
// köşeler tam sayı kafes kimliğiyle (`vertex`) eşlenir:
//
//	V(a, k) = ( 2·q + r + dx[k] , 3·r + dy[k] )
//
// Bu, pointy-top yerleşimin (ADR-07) tam sayı yeniden ölçeklenmesidir:
// x birimi √3·R/2, y birimi R/2. ENU'ya dönüşüm köşe **kimliği başına bir kez**
// yapılır, böylece paylaşılan köşeler bit düzeyinde aynı sayıyı alır.

package geometry

import (
	"fmt"
	"math"
	"sort"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// vertex, hex köşesinin tam sayı kafes kimliğidir.
type vertex struct{ X, Y int }

// cornerOffsets, hücre merkezinden köşelere olan kafes ötelemeleridir.
//
// Köşe k, geo.HexGrid.Corners ile aynı sıradadır: açı = 60°·k − 30°,
// yani k=0 doğu-güneydoğu, saat yönünün tersine ilerler.
var cornerOffsets = [6]vertex{
	{X: +1, Y: -1}, // −30°
	{X: +1, Y: +1}, //  30°
	{X: 0, Y: +2},  //  90°  (sivri uç, kuzey)
	{X: -1, Y: +1}, // 150°
	{X: -1, Y: -1}, // 210°
	{X: 0, Y: -2},  // 270°
}

// edgeNeighbours, kenar k'nın karşısındaki komşunun eksenel yönüdür.
//
// Kenar k, köşe k ile köşe k+1 arasındadır ve 60°·k yönündeki komşuyla
// paylaşılır. Sıra geo.Axial.Neighbors'tan farklıdır: orası komşuluk için
// keyfi ama sabit bir sıra kullanır, burada sıranın köşe indeksiyle
// hizalanması zorunludur.
var edgeNeighbours = [6]geo.Axial{
	{Q: +1, R: 0},  //   0°
	{Q: 0, R: +1},  //  60°
	{Q: -1, R: +1}, // 120°
	{Q: -1, R: 0},  // 180°
	{Q: 0, R: -1},  // 240°
	{Q: +1, R: -1}, // 300°
}

// Polygonize, hücre kümesinin sınırını MULTIPOLYGON'a çevirir (ENU metre).
//
// Küme yinelenen hücre içerebilir; tekilleştirilir. Sonuç, girdi sırasından
// bağımsızdır (K10): halkalar kafes kimliğine göre sıralı başlangıçlardan
// izlenir, poligonlar alan/konum sırasına göre kararlı biçimde dizilir.
func Polygonize(cells []geo.Axial, hex *geo.HexGrid) (MultiPolygon, error) {
	if hex == nil {
		return nil, fmt.Errorf("poligonlaştırma: ızgara zorunlu")
	}
	if len(cells) == 0 {
		return nil, fmt.Errorf("poligonlaştırma: hücre kümesi boş")
	}

	set := make(map[geo.Axial]struct{}, len(cells))
	unique := make([]geo.Axial, 0, len(cells))
	for _, a := range cells {
		if _, dup := set[a]; dup {
			continue
		}
		set[a] = struct{}{}
		unique = append(unique, a)
	}
	sort.Slice(unique, func(i, j int) bool {
		if unique[i].R != unique[j].R {
			return unique[i].R < unique[j].R
		}
		return unique[i].Q < unique[j].Q
	})

	next, err := boundaryEdges(unique, set)
	if err != nil {
		return nil, err
	}

	rings, err := traceRings(next, hex)
	if err != nil {
		return nil, err
	}
	return assemble(rings)
}

// boundaryEdges, sınır kenarlarını "çıkan köşe → giren köşe" olarak toplar.
//
// Kenarlar hücrenin çevresinde saat yönünün tersine yürütülür (köşe k → k+1);
// bu yönelim dış halkaları saat yönünün tersi, delikleri saat yönünde üretir.
func boundaryEdges(cells []geo.Axial, set map[geo.Axial]struct{}) (map[vertex]vertex, error) {
	next := make(map[vertex]vertex, len(cells))

	for _, a := range cells {
		for k := 0; k < 6; k++ {
			nb := geo.Axial{Q: a.Q + edgeNeighbours[k].Q, R: a.R + edgeNeighbours[k].R}
			if _, inside := set[nb]; inside {
				continue // iç kenar
			}

			from, to := vertexAt(a, k), vertexAt(a, (k+1)%6)
			if existing, dup := next[from]; dup {
				// Hex kafeste imkânsızdır (dosya başlığı); tetiklenirse köşe
				// kimliği ya da komşuluk tablosu bozulmuş demektir.
				return nil, fmt.Errorf("poligonlaştırma: köşe %v'den iki çıkan kenar (%v ve %v) — "+
					"kafes tutarsız", from, existing, to)
			}
			next[from] = to
		}
	}
	return next, nil
}

// vertexAt, hücrenin k'ıncı köşesinin kafes kimliğidir.
func vertexAt(a geo.Axial, k int) vertex {
	return vertex{
		X: 2*a.Q + a.R + cornerOffsets[k].X,
		Y: 3*a.R + cornerOffsets[k].Y,
	}
}

// traceRings, sınır kenarlarını kapalı halkalara zincirler.
//
// Her köşede tam bir giren ve bir çıkan kenar olduğu için `next` bir
// permütasyondur ve izleme daima başlangıca döner. Yine de adım sayısı
// sınırlanır: bozuk bir haritada sonsuz döngü, üretimde teşhisi en zor
// hatadır — hata döndürmek her zaman daha iyidir.
func traceRings(next map[vertex]vertex, hex *geo.HexGrid) ([]Ring, error) {
	starts := make([]vertex, 0, len(next))
	for v := range next {
		starts = append(starts, v)
	}
	sort.Slice(starts, func(i, j int) bool {
		if starts[i].Y != starts[j].Y {
			return starts[i].Y < starts[j].Y
		}
		return starts[i].X < starts[j].X
	})

	// Kafes kimliğinden ENU'ya dönüşüm: x birimi √3·R/2, y birimi R/2.
	unitX := math.Sqrt(3) * hex.Size() / 2
	unitY := hex.Size() / 2
	point := func(v vertex) geo.Point {
		return geo.Point{X: float64(v.X) * unitX, Y: float64(v.Y) * unitY}
	}

	visited := make(map[vertex]bool, len(next))
	rings := make([]Ring, 0, 4)

	for _, start := range starts {
		if visited[start] {
			continue
		}
		ring := Ring{point(start)}
		for v := next[start]; v != start; v = next[v] {
			if _, ok := next[v]; !ok {
				return nil, fmt.Errorf("poligonlaştırma: köşe %v'den çıkan kenar yok — zincir koptu", v)
			}
			if visited[v] {
				return nil, fmt.Errorf("poligonlaştırma: köşe %v iki halkada — kenar kümesi tutarsız", v)
			}
			visited[v] = true
			ring = append(ring, point(v))

			if len(ring) > len(next) {
				return nil, fmt.Errorf("poligonlaştırma: halka kapanmadı (%d kenar)", len(next))
			}
		}
		visited[start] = true
		ring = append(ring, point(start)) // halkayı kapat
		rings = append(rings, ring)
	}
	return rings, nil
}

// assemble, halkaları dış halka/delik olarak sınıflandırıp poligonlara böler.
//
// Delik, kendisini içeren **en küçük** dış halkaya atanır: bir deliğin içinde
// ada, adanın içinde başka bir delik olabilir (kütle alanı çok tepeliyse
// gerçekten olur), ve o iç delik dıştaki büyük halkaya değil adaya aittir.
func assemble(rings []Ring) (MultiPolygon, error) {
	type shell struct {
		ring Ring
		area float64
		idx  int
	}

	var shells []shell
	var holes []Ring
	for _, r := range rings {
		if a := r.SignedAreaM2(); a > 0 {
			shells = append(shells, shell{ring: r, area: a})
		} else if a < 0 {
			holes = append(holes, r)
		}
		// a == 0: dejenere halka — hex kafeste üretilemez, sessizce atılır
	}
	if len(shells) == 0 {
		return nil, fmt.Errorf("poligonlaştırma: hiç dış halka üretilemedi (%d halka)", len(rings))
	}

	// Kararlı sıra: büyük bileşen önce, eşitlikte ilk köşeye göre (K10).
	sort.Slice(shells, func(i, j int) bool {
		if shells[i].area != shells[j].area {
			return shells[i].area > shells[j].area
		}
		a, b := shells[i].ring[0], shells[j].ring[0]
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.X < b.X
	})
	for i := range shells {
		shells[i].idx = i
	}

	out := make(MultiPolygon, len(shells))
	for i, s := range shells {
		out[i] = Polygon{Exterior: s.ring}
	}

	for _, h := range holes {
		probe := h[0]
		owner := -1
		for _, s := range shells {
			if !s.ring.Contains(probe) {
				continue
			}
			if owner < 0 || s.area < shells[owner].area {
				owner = s.idx
			}
		}
		if owner < 0 {
			return nil, fmt.Errorf("poligonlaştırma: sahipsiz delik (köşe %v) — halka sınıflandırması tutarsız", probe)
		}
		out[owner].Holes = append(out[owner].Holes, h)
	}

	return out, nil
}
