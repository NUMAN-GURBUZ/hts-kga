// T-E09 — analiz motorunun iş metrikleri (ADR-34/2).
//
// Yeni bir sayaç mantığı icat edilmez: Consumer zaten `analyzed/foreign/
// skipped` sayıyordu (log satırında görünür), burada aynı olaylar OTel
// enstrümanlarına da yansıtılır.
package driver

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/driver")

var eventsTotal = mustInt64Counter(
	"hts_analysis_events_processed_total",
	"Analiz motorunun işlediği hts.records olaylarının sonucu",
)

func mustInt64Counter(name, desc string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		panic("otel sayaç: " + err.Error())
	}
	return c
}

func outcomeAttr(outcome string) metric.AddOption {
	return metric.WithAttributes(attribute.String("outcome", outcome))
}
