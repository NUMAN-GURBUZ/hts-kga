// Package kafka, franz-go üzerine ince bir üretici/tüketici katmanı ve
// W3C TraceContext taşıyıcısı sağlar (T-E02-18, O-05).
//
// Katman kuralı: bu paket alan (domain) tiplerini tanımaz. Kayıt gövdesi
// çağıran katmanda serileştirilir; burada yalnızca anahtar, gövde ve başlıklar
// vardır.
package kafka

import (
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
)

// HeaderCarrier, Kafka kayıt başlıklarını OpenTelemetry taşıyıcısı olarak
// sunar (O-05).
//
// # Neden gerekli
//
// Simülatör bir olayı yayınladığında, analiz motorunun o olayı işlemesi ayrı
// bir süreçte, saniyeler sonra olur. İki iş parçasını tek bir izde (trace)
// birleştirmenin standart yolu, bağlamı mesaj başlığında taşımaktır: üretici
// `traceparent` başlığını yazar, tüketici okur ve span'ini onun altına bağlar.
// Aksi hâlde uçtan uca gecikme ölçülemez, yalnızca parçalar görünür.
//
// propagation.TextMapCarrier arayüzünü uygular.
type HeaderCarrier struct {
	headers *[]kgo.RecordHeader
}

// NewHeaderCarrier, verilen başlık dilimi üzerinde bir taşıyıcı kurar.
func NewHeaderCarrier(headers *[]kgo.RecordHeader) HeaderCarrier {
	return HeaderCarrier{headers: headers}
}

// Get, verilen anahtarın değerini döndürür.
func (c HeaderCarrier) Get(key string) string {
	if c.headers == nil {
		return ""
	}
	for _, h := range *c.headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Set, anahtarı yazar; varsa üzerine yazar.
func (c HeaderCarrier) Set(key, value string) {
	if c.headers == nil {
		return
	}
	for i := range *c.headers {
		if (*c.headers)[i].Key == key {
			(*c.headers)[i].Value = []byte(value)
			return
		}
	}
	*c.headers = append(*c.headers, kgo.RecordHeader{Key: key, Value: []byte(value)})
}

// Keys, taşıyıcıdaki tüm anahtarları döndürür.
func (c HeaderCarrier) Keys() []string {
	if c.headers == nil {
		return nil
	}
	keys := make([]string, 0, len(*c.headers))
	for _, h := range *c.headers {
		keys = append(keys, h.Key)
	}
	return keys
}

// propagator, W3C TraceContext yayıcısıdır.
//
// Paket düzeyinde tutulur: yayıcı durumsuzdur ve her çağrıda yeniden kurmak
// sıcak yolda gereksiz ayırma yapardı.
var propagator = propagation.TraceContext{}

// Propagator, W3C TraceContext yayıcısını döndürür.
func Propagator() propagation.TextMapPropagator { return propagator }

var _ propagation.TextMapCarrier = HeaderCarrier{}
