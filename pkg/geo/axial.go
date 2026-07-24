// T-E02-03 — Pointy-top axial hex ızgara matematiği (ADR-07).
//
// Tüm hesaplar yerel ENU düzleminde metre cinsindendir (bkz. enu.go).
// Izgara iki yerde kullanılır:
//   - S1 simülatör: site yerleşimi (merkez-merkez aralık = profil ISD)
//   - S2 analiz motoru: yoğunluk ızgarası (merkez-merkez aralık = 100 m)
//
// Bu yüzden çözünürlük parametreden gelir, sabit değildir.
package geo

import (
	"fmt"
	"math"
)

// sqrt3, pointy-top hex matematiğinde tekrar eden √3 katsayısı.
var sqrt3 = math.Sqrt(3)

// Axial, eksenel (axial) hex koordinatıdır.
//
// Küp koordinatlarının (q, r, s) kısıtlı gösterimidir: s = −q − r.
// Tamsayı olduğu için harita anahtarı olarak güvenle kullanılabilir.
type Axial struct {
	Q int
	R int
}

// S, küp koordinatının üçüncü bileşenidir (q + r + s = 0).
func (a Axial) S() int {
	return -a.Q - a.R
}

// axialDirections, pointy-top ızgarada altı komşunun eksenel yön vektörleri.
var axialDirections = [6]Axial{
	{Q: +1, R: 0}, {Q: +1, R: -1}, {Q: 0, R: -1},
	{Q: -1, R: 0}, {Q: -1, R: +1}, {Q: 0, R: +1},
}

// Neighbors, hücrenin altı komşusunu döndürür (sabit sırada — deterministik).
func (a Axial) Neighbors() [6]Axial {
	var n [6]Axial
	for i, d := range axialDirections {
		n[i] = Axial{Q: a.Q + d.Q, R: a.R + d.R}
	}
	return n
}

// HexDistance, iki hücre arasındaki hücre sayısı cinsinden uzaklıktır.
func HexDistance(a, b Axial) int {
	dq, dr := a.Q-b.Q, a.R-b.R
	ds := a.S() - b.S()
	return (abs(dq) + abs(dr) + abs(ds)) / 2
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ─── Izgara ───────────────────────────────────────────────────────────────────

// HexGrid, belirli bir çözünürlükte pointy-top axial hex ızgaradır (ADR-07).
//
// Değişmezdir (immutable); eşzamanlı kullanımda güvenlidir.
type HexGrid struct {
	// resolution, merkez-merkez aralıktır (metre). Altı komşunun tamamına
	// olan mesafe bu değere eşittir.
	resolution float64

	// size, merkez→köşe yarıçapıdır: R = resolution / √3 (ADR-07).
	size float64
}

// NewHexGrid, verilen merkez-merkez çözünürlükte bir ızgara oluşturur.
func NewHexGrid(resolutionM float64) (*HexGrid, error) {
	if !(resolutionM > 0) || math.IsInf(resolutionM, 1) {
		return nil, fmt.Errorf("hex ızgara çözünürlüğü pozitif ve sonlu olmalı (%g)", resolutionM)
	}
	return &HexGrid{
		resolution: resolutionM,
		size:       resolutionM / sqrt3,
	}, nil
}

// Resolution, merkez-merkez aralığı döndürür (metre).
func (g *HexGrid) Resolution() float64 { return g.resolution }

// Size, merkez→köşe yarıçapını döndürür (metre): R = resolution / √3.
func (g *HexGrid) Size() float64 { return g.size }

// CellAreaM2, tek bir hücrenin alanıdır: (3√3/2)·R² (ADR-07).
// 100 m çözünürlükte ≈ 8.660 m².
func (g *HexGrid) CellAreaM2() float64 {
	return 1.5 * sqrt3 * g.size * g.size
}

// Center, eksenel koordinatın ENU düzlemindeki merkezini döndürür (ADR-07):
//
//	x = R · √3 · (q + r/2)
//	y = R · (3/2) · r
func (g *HexGrid) Center(a Axial) Point {
	return Point{
		X: g.size * sqrt3 * (float64(a.Q) + float64(a.R)/2),
		Y: g.size * 1.5 * float64(a.R),
	}
}

// At, ENU noktasını içeren hücrenin eksenel koordinatını döndürür.
// Center'ın tersidir; standart küp yuvarlama (cube-round) uygulanır (ADR-07).
func (g *HexGrid) At(p Point) Axial {
	qf := (sqrt3/3*p.X - p.Y/3) / g.size
	rf := (2.0 / 3.0 * p.Y) / g.size
	return cubeRound(qf, rf)
}

// cubeRound, kesirli küp koordinatını en yakın hex hücresine yuvarlar.
//
// q + r + s = 0 kısıtı korunmalıdır: en çok yuvarlanan bileşen diğer ikisinden
// yeniden türetilir.
func cubeRound(qf, rf float64) Axial {
	sf := -qf - rf

	q := math.Round(qf)
	r := math.Round(rf)
	s := math.Round(sf)

	dq := math.Abs(q - qf)
	dr := math.Abs(r - rf)
	ds := math.Abs(s - sf)

	switch {
	case dq > dr && dq > ds:
		q = -r - s
	case dr > ds:
		r = -q - s
	}
	return Axial{Q: int(q), R: int(r)}
}

// Corners, hücrenin altı köşesini saat yönünün tersine döndürür (ENU, metre).
//
// Pointy-top yönelimde köşe açıları 60°·i − 30°'dir: hücrenin sivri ucu
// +Y (kuzey) yönünü gösterir.
func (g *HexGrid) Corners(a Axial) []Point {
	c := g.Center(a)
	corners := make([]Point, 6)
	for i := range corners {
		angle := math.Pi / 180 * (60*float64(i) - 30)
		sin, cos := math.Sincos(angle)
		corners[i] = Point{X: c.X + g.size*cos, Y: c.Y + g.size*sin}
	}
	return corners
}

// Cover, merkezi center olan radius yarıçaplı diski kaplayan hücreleri döndürür.
//
// Bir hücre, **merkezi** disk içinde kalıyorsa sonuca dâhil edilir. Dönen dilim
// (r, q) sırasına göre deterministiktir — aynı girdi her koşuda aynı sırayı verir.
func (g *HexGrid) Cover(center Point, radius float64) []Axial {
	if !(radius > 0) {
		return nil
	}

	// Eksenel satır (r) aralığı: y = R·1.5·r → r = y / (1.5·R)
	rowHeight := 1.5 * g.size
	rMin := int(math.Floor((center.Y - radius) / rowHeight))
	rMax := int(math.Ceil((center.Y + radius) / rowHeight))

	colWidth := sqrt3 * g.size
	radiusSq := radius * radius

	cells := make([]Axial, 0, estimateCellCount(radius, g.CellAreaM2()))

	for r := rMin; r <= rMax; r++ {
		// Satır içi sütun (q) aralığı: x = R·√3·(q + r/2)
		offset := float64(r) / 2
		qMin := int(math.Floor((center.X-radius)/colWidth - offset))
		qMax := int(math.Ceil((center.X+radius)/colWidth - offset))

		for q := qMin; q <= qMax; q++ {
			a := Axial{Q: q, R: r}
			c := g.Center(a)
			dx, dy := c.X-center.X, c.Y-center.Y
			if dx*dx+dy*dy <= radiusSq {
				cells = append(cells, a)
			}
		}
	}
	return cells
}

// estimateCellCount, Cover için ön ayırma büyüklüğü tahminidir (disk alanı / hücre alanı).
func estimateCellCount(radius, cellArea float64) int {
	n := math.Pi * radius * radius / cellArea
	if n < 1 || math.IsInf(n, 1) {
		return 1
	}
	return int(n) + 1
}
