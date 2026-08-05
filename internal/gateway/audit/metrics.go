// T-E09 — gateway istek metrikleri (ADR-34/2).
//
// `Written()`/`Dropped()` sayaçları zaten vardı (kapanış raporunda
// görünür); burada aynı olay OTel'e de yansıtılır.
package audit

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/audit")

var requestsTotal = mustCounter(
	"hts_gateway_requests_total",
	"Gateway API isteklerinin uç ve sonuca göre sayısı",
)

func mustCounter(name, desc string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		panic("otel sayaç: " + err.Error())
	}
	return c
}

func actionAttrs(action, outcome string) metric.AddOption {
	return metric.WithAttributes(
		attribute.String("action", action),
		attribute.String("outcome", outcome),
	)
}
