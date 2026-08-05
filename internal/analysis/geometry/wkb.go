// T-E03-13 — WKB kodlaması.
//
// Geometri veritabanına **ikili** (WKB) olarak gider, metin (WKT) olarak
// değil. Gerekçe iki tanedir:
//
//  1. Tam yuvarlama. WKT, float64'ü ondalık basamağa çevirir; `%.7f` kullanan
//     yaygın uygulama 1 cm'lik bir kayma bırakır ve K10'un "iki koşu bit
//     düzeyinde aynı" ölçütü, veritabanına gidip dönen geometride kırılırdı.
//     WKB float64'ü olduğu gibi taşır.
//  2. Hacim. Kırsalda tek bir kontur binlerce köşe içerir; WKT'nin tam
//     duyarlıklı hâli (`%.17g`) köşe başına ~45 bayt sürer, WKB 16 bayt.
//
// SRID gömülmez (EWKB değil, düz OGC WKB): sunucu tarafında
// `ST_GeomFromWKB(wkb, 4326)` ile verilir. Böylece kodlayıcı PostGIS'in
// genişletme biçimine bağlı kalmaz.
//
// Bayt sırası little-endian sabitlenmiştir — hedef mimari (amd64/arm64)
// zaten little-endian, ama biçim makineye göre değişmemelidir: aynı geometri
// her makinede aynı baytları vermelidir (K10).

package geometry

import (
	"encoding/binary"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// WKB geometri tipi kodları (OGC).
const (
	wkbPoint        uint32 = 1
	wkbPolygon      uint32 = 3
	wkbMultiPolygon uint32 = 6
)

// wkbLittleEndian, bayt sırası göstergesidir.
const wkbLittleEndian byte = 1

// WKB, çoklu poligonu OGC Well-Known Binary olarak kodlar.
//
// Koordinat sırası (X, Y) = (boylam, enlem)'dir. Ters çevrilmesi, PostGIS'in
// sessizce kabul edip alanı ve konumu bozmasına yol açar — GEOGRAPHY sütunu
// enlem/boylam sırasını kendisi denetlemez.
func (g GeoMultiPolygon) WKB() []byte {
	size := 9 // bayt sırası + tip + poligon sayısı
	for _, p := range g {
		size += 9 // bayt sırası + tip + halka sayısı
		size += 4 + 16*len(p.Exterior)
		for _, h := range p.Holes {
			size += 4 + 16*len(h)
		}
	}

	buf := make([]byte, 0, size)
	buf = appendHeader(buf, wkbMultiPolygon)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(g)))

	for _, p := range g {
		buf = appendHeader(buf, wkbPolygon)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(1+len(p.Holes)))
		buf = appendRing(buf, p.Exterior)
		for _, h := range p.Holes {
			buf = appendRing(buf, h)
		}
	}
	return buf
}

// PointWKB, tek bir koordinatı OGC WKB noktası olarak kodlar.
func PointWKB(p geo.WGS84) []byte {
	buf := make([]byte, 0, 21)
	buf = appendHeader(buf, wkbPoint)
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(p.Lon))
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(p.Lat))
	return buf
}

func appendHeader(buf []byte, geomType uint32) []byte {
	buf = append(buf, wkbLittleEndian)
	return binary.LittleEndian.AppendUint32(buf, geomType)
}

func appendRing(buf []byte, r GeoRing) []byte {
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(r)))
	for _, v := range r {
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(v.Lon))
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(v.Lat))
	}
	return buf
}
