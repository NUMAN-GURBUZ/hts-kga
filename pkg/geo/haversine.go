// TD-03 — Küresel (büyük daire) mesafe referansı.
//
// Plan Bölüm C.3 `pkg/geo/{haversine,axial,enu}.go` olarak bu dosyayı öngörür.
//
// Kullanım sınırı: bu hesap, **ortak bir ENU origin'i bulunmayan** iki coğrafi
// nokta arasında mesafe gerektiğinde kullanılır — örneğin ardışık HTS
// kayıtları arası hız denetimi (bütünlük Kural 2) veya birim testlerde
// düzlemsel yaklaşımın çapraz doğrulanması.
//
// Sıcak yolda (tick başına ajan-hücre mesafesi) kullanılmaz: orada tüm
// noktalar zaten ENU düzlemindedir ve Distance() hem doğru hem çok daha
// ucuzdur (bkz. enu.go, ADR-07).
package geo

import "math"

// EarthRadiusM, IUGG ortalama Dünya yarıçapıdır (metre).
//
// Küresel yaklaşım kullanılır: WGS84 elipsoidine göre en büyük sapma ~%0.3'tür.
// Elipsoid doğruluğu gereken yerlerde (metrik üretimi) PostGIS ST_Distance
// GEOGRAPHY üzerinden hesaplar — plan Bölüm F.3.
const EarthRadiusM = 6_371_008.8

// degToRad, dereceyi radyana çeviren katsayı.
const degToRad = math.Pi / 180

// Haversine, iki WGS84 koordinatı arasındaki büyük daire mesafesini döndürür
// (metre). Simetriktir ve aynı nokta için 0 döndürür.
func Haversine(a, b WGS84) float64 {
	φ1, φ2 := a.Lat*degToRad, b.Lat*degToRad
	dφ := (b.Lat - a.Lat) * degToRad
	dλ := (b.Lon - a.Lon) * degToRad

	sinφ, sinλ := math.Sin(dφ/2), math.Sin(dλ/2)
	h := sinφ*sinφ + math.Cos(φ1)*math.Cos(φ2)*sinλ*sinλ

	// Antipodal noktalarda kayan nokta birikimi h'yi 1'in çok az üstüne
	// taşıyabilir; Sqrt(h) > 1 olunca Asin NaN döner. Üst sınıra kırp.
	if h > 1 {
		h = 1
	}
	return 2 * EarthRadiusM * math.Asin(math.Sqrt(h))
}
