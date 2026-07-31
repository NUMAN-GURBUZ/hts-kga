package params

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Elazığ merkezi — configs/urban_ta.yaml'daki senaryo başlangıcı.
const (
	originLat = 38.6748
	originLon = 39.2225
)

// fakeSource, Redis'e gitmeden envanter döndüren sahte kaynaktır.
//
// Arayüzün tüketici tarafında tanımlanmasının kazancı budur: bu testler
// altyapı olmadan koşar.
type fakeSource struct {
	cells []redis.CellParams
	err   error
}

func (f fakeSource) ScanCells(context.Context, uuid.UUID) ([]redis.CellParams, error) {
	return f.cells, f.err
}

// cellParams, verilen kimlikle geçerli bir Redis kaydı üretir.
func cellParams(n byte, lat, lon float64, model string) redis.CellParams {
	id := uuid.UUID{}
	id[0] = n
	site := uuid.UUID{}
	site[15] = n
	return redis.CellParams{
		CellID:     id,
		SiteID:     site,
		Azimuth:    float64(n%3) * 120,
		BeamWidth:  65,
		FreqMHz:    2100,
		EIRPdBm:    58,
		AntHeightM: 25,
		TiltDeg:    6,
		RMaxM:      5000,
		Morphology: "urban",
		ModelType:  model,
		Lat:        lat,
		Lon:        lon,
	}
}

// TestLoad_AdaptsRedisRecords, Redis kaydının analiz hücresine dönüşümünü
// alan alan sınar (T-E03-02 adaptörü).
func TestLoad_AdaptsRedisRecords(t *testing.T) {
	src := fakeSource{cells: []redis.CellParams{
		cellParams(3, originLat, originLon, "UMa"),
		cellParams(1, originLat+0.01, originLon, "UMi"),
		cellParams(2, originLat, originLon+0.01, "RMa"),
	}}

	inv, err := Load(context.Background(), src, uuid.New(), originLat, originLon)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if inv.Len() != 3 {
		t.Fatalf("hücre sayısı %d, 3 beklenir", inv.Len())
	}
	if got := inv.MaxRMaxM(); got != 5000 {
		t.Errorf("max r_max %.1f, 5000 beklenir", got)
	}
	if o := inv.Origin(); o.Lat != originLat || o.Lon != originLon {
		t.Errorf("ENU başlangıcı %v, (%g, %g) beklenir", o, originLat, originLon)
	}

	// Başlangıç noktasındaki hücre ENU'da (0,0) olmalı.
	id3 := uuid.UUID{}
	id3[0] = 3
	cell, ok := inv.Cell(id3)
	if !ok {
		t.Fatal("hücre kimliğe göre bulunamadı")
	}
	if math.Abs(cell.Site.X) > 1e-6 || math.Abs(cell.Site.Y) > 1e-6 {
		t.Errorf("başlangıçtaki hücre ENU %v, (0,0) beklenir", cell.Site)
	}
	if cell.Model == nil {
		t.Error("yayılım modeli bağlanmadı")
	}
	if cell.EIRPdBm != 58 || cell.FreqMHz != 2100 || cell.BeamWidthDeg != 65 ||
		cell.TiltDeg != 6 || cell.AntHeightM != 25 || cell.RMaxM != 5000 {
		t.Errorf("parametreler taşınmadı: %+v", cell)
	}

	// Kuzeydeki hücre +Y, doğudaki hücre +X yönünde olmalı.
	id1, id2 := uuid.UUID{}, uuid.UUID{}
	id1[0], id2[0] = 1, 2
	north, _ := inv.Cell(id1)
	east, _ := inv.Cell(id2)
	if !(north.Site.Y > 1000 && math.Abs(north.Site.X) < 1) {
		t.Errorf("kuzeydeki hücre ENU %v, +Y yönünde beklenir", north.Site)
	}
	if !(east.Site.X > 800 && math.Abs(east.Site.Y) < 1) {
		t.Errorf("doğudaki hücre ENU %v, +X yönünde beklenir", east.Site)
	}
}

// TestLoad_StableOrder, envanterin fiziksel konuma göre kararlı
// sıralandığını sınar.
//
// K10 için gereklidir: kaynak sırası değişse bile kütle toplamının kayan
// nokta sırası değişmemelidir. Kimliğe göre sıralanmaz (ADR-35): cell_id
// bilinçli olarak run_id içerir (ADR-05) ve ona göre sıralamak K10'u
// bozardı — iki çağrı burada bilerek FARKLI run_id kullanıyor (`uuid.New()`)
// ki sıranın kimlikten değil konum+azimuttan geldiği doğrulansın.
func TestLoad_StableOrder(t *testing.T) {
	forward := []redis.CellParams{
		cellParams(1, originLat, originLon, "UMa"),
		cellParams(2, originLat, originLon, "UMa"),
		cellParams(3, originLat, originLon, "UMa"),
	}
	reverse := []redis.CellParams{forward[2], forward[1], forward[0]}

	a, err := Load(context.Background(), fakeSource{cells: forward}, uuid.New(), originLat, originLon)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := Load(context.Background(), fakeSource{cells: reverse}, uuid.New(), originLat, originLon)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Aynı fiziksel hücre iki farklı run_id altında farklı cell_id taşır;
	// bu yüzden kimlik değil azimut (bu fixture'da konum sabit, azimut
	// hücreyi ayırt eden tek fiziksel özellik) karşılaştırılır.
	for i := range a.Cells() {
		if a.Cells()[i].AzimuthDeg != b.Cells()[i].AzimuthDeg {
			t.Fatalf("sıra kaynağa bağlı: %d. hücre azimut %g vs %g",
				i, a.Cells()[i].AzimuthDeg, b.Cells()[i].AzimuthDeg)
		}
	}
	for i := 1; i < a.Len(); i++ {
		if a.Cells()[i-1].AzimuthDeg >= a.Cells()[i].AzimuthDeg {
			t.Fatalf("sıra artan değil: azimut %g ≥ %g",
				a.Cells()[i-1].AzimuthDeg, a.Cells()[i].AzimuthDeg)
		}
	}
}

// TestLoad_Rejects, bozuk envanterin reddedildiğini sınar.
func TestLoad_Rejects(t *testing.T) {
	base := cellParams(1, originLat, originLon, "UMa")

	bad := map[string][]redis.CellParams{
		"hücre yok":               {},
		"bilinmeyen model":        {cellParams(1, originLat, originLon, "COST231")},
		"r_max sıfır":             {mutate(base, func(c *redis.CellParams) { c.RMaxM = 0 })},
		"hüzme genişliği 0":       {mutate(base, func(c *redis.CellParams) { c.BeamWidth = 0 })},
		"frekans 0":               {mutate(base, func(c *redis.CellParams) { c.FreqMHz = 0 })},
		"anten yüksekliği 0":      {mutate(base, func(c *redis.CellParams) { c.AntHeightM = 0 })},
		"yinelenen hücre kimliği": {base, base},
	}

	for name, cells := range bad {
		if _, err := Load(context.Background(), fakeSource{cells: cells}, uuid.New(), originLat, originLon); err == nil {
			t.Errorf("%s: hata bekleniyordu", name)
		}
	}

	// Kaynak hatası olduğu gibi sarılarak döner.
	if _, err := Load(context.Background(), fakeSource{err: fmt.Errorf("bağlantı yok")}, uuid.New(), originLat, originLon); err == nil {
		t.Error("kaynak hatası yutuldu")
	}
	// Kaynak ve run_id zorunlu.
	if _, err := Load(context.Background(), nil, uuid.New(), originLat, originLon); err == nil {
		t.Error("nil kaynak kabul edildi")
	}
	if _, err := Load(context.Background(), fakeSource{cells: []redis.CellParams{base}}, uuid.Nil, originLat, originLon); err == nil {
		t.Error("boş run_id kabul edildi")
	}
}

func mutate(c redis.CellParams, f func(*redis.CellParams)) redis.CellParams {
	f(&c)
	return c
}

// TestInventory_CellLookup, bilinmeyen kimliğin bulunamadığını sınar.
func TestInventory_CellLookup(t *testing.T) {
	inv, err := Load(context.Background(),
		fakeSource{cells: []redis.CellParams{cellParams(1, originLat, originLon, "UMa")}},
		uuid.New(), originLat, originLon)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := inv.Cell(uuid.New()); ok {
		t.Error("bilinmeyen kimlik bulundu")
	}
}

// TestLoad_RejectsBadOrigin, geçersiz ENU başlangıcını sınar.
func TestLoad_RejectsBadOrigin(t *testing.T) {
	src := fakeSource{cells: []redis.CellParams{cellParams(1, originLat, originLon, "UMa")}}
	if _, err := Load(context.Background(), src, uuid.New(), 95, originLon); err == nil {
		t.Error("geçersiz enlem kabul edildi")
	}
}

// TestInventory_ProjectionRoundTrip, ENU dönüşümünün site konumlarını
// koruduğunu sınar.
func TestInventory_ProjectionRoundTrip(t *testing.T) {
	const lat, lon = originLat + 0.02, originLon - 0.03
	inv, err := Load(context.Background(),
		fakeSource{cells: []redis.CellParams{cellParams(1, lat, lon, "UMa")}},
		uuid.New(), originLat, originLon)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	projector, err := geo.NewProjector(originLat, originLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	back := projector.Inverse(inv.Cells()[0].Site)

	if math.Abs(back.Lat-lat) > 1e-9 || math.Abs(back.Lon-lon) > 1e-9 {
		t.Errorf("tur dönüşü (%.9f, %.9f), beklenen (%.9f, %.9f)",
			back.Lat, back.Lon, lat, lon)
	}
}
