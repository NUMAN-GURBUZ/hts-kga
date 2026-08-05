package geometry

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// decoder, üretilen WKB'yi bağımsız olarak çözer.
//
// Kodlayıcının kendi yardımcılarını kullanmaz: aynı hatayı iki kez yapıp
// testin geçmesi böyle engellenir.
type decoder struct {
	buf []byte
	pos int
	t   *testing.T
}

func (d *decoder) byteVal() byte {
	d.t.Helper()
	if d.pos >= len(d.buf) {
		d.t.Fatalf("WKB erken bitti (konum %d)", d.pos)
	}
	v := d.buf[d.pos]
	d.pos++
	return v
}

func (d *decoder) uint32Val() uint32 {
	d.t.Helper()
	if d.pos+4 > len(d.buf) {
		d.t.Fatalf("WKB erken bitti (konum %d)", d.pos)
	}
	v := binary.LittleEndian.Uint32(d.buf[d.pos:])
	d.pos += 4
	return v
}

func (d *decoder) float64Val() float64 {
	d.t.Helper()
	if d.pos+8 > len(d.buf) {
		d.t.Fatalf("WKB erken bitti (konum %d)", d.pos)
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(d.buf[d.pos:]))
	d.pos += 8
	return v
}

// decodeRing, halkayı çözer ve (lon, lat) çiftleri döndürür.
func (d *decoder) decodeRing() GeoRing {
	n := d.uint32Val()
	ring := make(GeoRing, n)
	for i := range ring {
		ring[i].Lon = d.float64Val()
		ring[i].Lat = d.float64Val()
	}
	return ring
}

// decodeMultiPolygon, çoklu poligonu çözer.
func (d *decoder) decodeMultiPolygon() GeoMultiPolygon {
	d.t.Helper()

	if b := d.byteVal(); b != 1 {
		d.t.Fatalf("bayt sırası %d, 1 (little-endian) beklenir", b)
	}
	if g := d.uint32Val(); g != wkbMultiPolygon {
		d.t.Fatalf("geometri tipi %d, %d (MULTIPOLYGON) beklenir", g, wkbMultiPolygon)
	}

	out := make(GeoMultiPolygon, d.uint32Val())
	for i := range out {
		if b := d.byteVal(); b != 1 {
			d.t.Fatalf("poligon %d: bayt sırası %d", i, b)
		}
		if g := d.uint32Val(); g != wkbPolygon {
			d.t.Fatalf("poligon %d: tip %d, %d (POLYGON) beklenir", i, g, wkbPolygon)
		}
		rings := d.uint32Val()
		out[i].Exterior = d.decodeRing()
		for r := uint32(1); r < rings; r++ {
			out[i].Holes = append(out[i].Holes, d.decodeRing())
		}
	}
	return out
}

// sampleGeometry, delikli ve iki bileşenli bir geometri üretir.
func sampleGeometry(t testing.TB) GeoMultiPolygon {
	t.Helper()

	hex := testHex(t)
	center := geo.Axial{Q: 0, R: 0}

	cells := neighbours(center) // ortası boş halka → delik
	cells = append(cells, geo.Axial{Q: 30, R: 0}, geo.Axial{Q: 31, R: 0})

	mp, err := Polygonize(cells, hex)
	if err != nil {
		t.Fatalf("Polygonize: %v", err)
	}

	pr, err := geo.NewProjector(38.6748, 39.2225)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	return mp.Project(pr)
}

// TestWKB_RoundTrip, kodlanan geometrinin bit düzeyinde geri okunduğunu
// doğrular — K10'un depolama karşılığı.
func TestWKB_RoundTrip(t *testing.T) {
	want := sampleGeometry(t)
	if want.PartCount() != 2 || len(want[0].Holes) != 1 {
		t.Fatalf("örnek geometri beklendiği gibi değil: %d bileşen, %d delik",
			want.PartCount(), len(want[0].Holes))
	}

	d := &decoder{buf: want.WKB(), t: t}
	got := d.decodeMultiPolygon()

	if d.pos != len(d.buf) {
		t.Errorf("WKB'de %d bayt artakaldı", len(d.buf)-d.pos)
	}
	if len(got) != len(want) {
		t.Fatalf("bileşen sayısı %d, %d beklenir", len(got), len(want))
	}

	for i := range want {
		if len(got[i].Holes) != len(want[i].Holes) {
			t.Fatalf("bileşen %d: delik sayısı %d, %d beklenir",
				i, len(got[i].Holes), len(want[i].Holes))
		}
		for j, ring := range append([]GeoRing{want[i].Exterior}, want[i].Holes...) {
			decoded := got[i].Exterior
			if j > 0 {
				decoded = got[i].Holes[j-1]
			}
			if len(decoded) != len(ring) {
				t.Fatalf("bileşen %d halka %d: köşe sayısı %d, %d beklenir",
					i, j, len(decoded), len(ring))
			}
			for k := range ring {
				// Bit düzeyinde eşitlik: WKB float64'ü yuvarlamaz.
				if decoded[k] != ring[k] {
					t.Fatalf("bileşen %d halka %d köşe %d: %v ≠ %v", i, j, k, decoded[k], ring[k])
				}
			}
		}
	}
}

// TestWKB_CoordinateOrderIsLonLat, koordinat sırasının (X=boylam, Y=enlem)
// olduğunu doğrular.
//
// Ters sıra PostGIS tarafından sessizce kabul edilir: geometri Hint Okyanusu'na
// düşer, alan ve merkez anlamsızlaşır ama hiçbir hata görülmez. Bu yüzden ayrı
// bir test vardır.
func TestWKB_CoordinateOrderIsLonLat(t *testing.T) {
	g := sampleGeometry(t)

	d := &decoder{buf: g.WKB(), t: t}
	got := d.decodeMultiPolygon()

	first := got[0].Exterior[0]
	// Senaryo merkezi Elazığ: enlem ≈ 38,7, boylam ≈ 39,2 — ikisi de yakın,
	// bu yüzden karşılaştırma kaynak geometriyle yapılır.
	want := g[0].Exterior[0]
	if first.Lon != want.Lon || first.Lat != want.Lat {
		t.Errorf("ilk köşe (lon %.9f, lat %.9f), (lon %.9f, lat %.9f) beklenir",
			first.Lon, first.Lat, want.Lon, want.Lat)
	}
	// Boylam ve enlem karışsaydı fark 0,5°'den büyük olurdu.
	if math.Abs(want.Lon-want.Lat) < 0.1 {
		t.Skip("test bölgesinde enlem ve boylam birbirine çok yakın — ayrım anlamsız")
	}
}

// TestPointWKB, nokta kodlamasını doğrular.
func TestPointWKB(t *testing.T) {
	p := geo.WGS84{Lat: 38.674812345678, Lon: 39.222587654321}

	d := &decoder{buf: PointWKB(p), t: t}
	if b := d.byteVal(); b != 1 {
		t.Fatalf("bayt sırası %d, 1 beklenir", b)
	}
	if g := d.uint32Val(); g != wkbPoint {
		t.Fatalf("tip %d, %d (POINT) beklenir", g, wkbPoint)
	}
	lon, lat := d.float64Val(), d.float64Val()

	if lon != p.Lon || lat != p.Lat {
		t.Errorf("(lon %.12f, lat %.12f), (lon %.12f, lat %.12f) beklenir", lon, lat, p.Lon, p.Lat)
	}
	if d.pos != len(d.buf) {
		t.Errorf("WKB %d bayt, 21 beklenir", len(d.buf))
	}
}

// TestWKB_Deterministic, aynı geometrinin daima aynı baytları verdiğini
// doğrular (K10).
func TestWKB_Deterministic(t *testing.T) {
	g := sampleGeometry(t)

	want := g.WKB()
	for i := 0; i < 10; i++ {
		got := g.WKB()
		if len(got) != len(want) {
			t.Fatalf("iter %d: uzunluk %d ≠ %d", i, len(got), len(want))
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("iter %d: bayt %d ayrıştı", i, j)
			}
		}
	}
}

// TestWKB_Size, kodun beklenen boyutta olduğunu doğrular: köşe başına 16 bayt.
//
// Hacim tahmini (Gün 6 disk ölçümü) bu ilişkiye dayanır.
func TestWKB_Size(t *testing.T) {
	g := sampleGeometry(t)

	// MULTIPOLYGON başlığı 9 bayt (sıra + tip + bileşen sayısı); her poligon
	// 9 bayt (sıra + tip + halka sayısı); her halka 4 bayt (köşe sayısı) +
	// köşe başına 16 bayt.
	want := 9
	for _, p := range g {
		want += 9 + 4 + 16*len(p.Exterior)
		for _, h := range p.Holes {
			want += 4 + 16*len(h)
		}
	}

	if got := len(g.WKB()); got != want {
		t.Errorf("WKB %d bayt, %d beklenir (%d köşe)", got, want, g.VertexCount())
	}
	t.Logf("örnek geometri: %d köşe, %d bayt (köşe başına %.1f bayt)",
		g.VertexCount(), len(g.WKB()), float64(len(g.WKB()))/float64(g.VertexCount()))
}
