package kafka

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSpanPropagationAcrossHeaderCarrier, ADR-34/3'ün dayandığı iddiayı
// doğrudan sınar: üretici tarafında açılan span'in trace_id'si, başlıklar
// üzerinden Extract edilip tüketici tarafında açılan span'e taşınıyor mu?
//
// Çalışan bir OTLP arka ucu (Jaeger/Tempo) gerektirmez — ADR-34/3'ün kararı
// tam olarak budur: uçtan uca yayılımın kanıtı çalışan bir arka uca değil
// bu teste bağlıdır.
func TestSpanPropagationAcrossHeaderCarrier(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tr := tp.Tracer("test")

	// Üretici: pkg/kafka/producer.go'nun yaptığı gibi ctx'teki span'i
	// başlıklara yazar.
	prodCtx, prodSpan := tr.Start(context.Background(), "kafka.publish")
	var headers []kgo.RecordHeader
	propagator.Inject(prodCtx, NewHeaderCarrier(&headers))
	prodSpan.End()

	if len(headers) == 0 {
		t.Fatal("Inject hiçbir başlık yazmadı — traceparent üretilmedi")
	}

	// Tüketici: internal/analysis/driver ve internal/integrity/source'un
	// yaptığı gibi başlıklardan bağlamı çıkarır ve altına span açar.
	consCtx := propagator.Extract(context.Background(), NewHeaderCarrier(&headers))
	_, consSpan := tr.Start(consCtx, "kafka.consume")
	consSpan.End()

	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("beklenen 2 span, alınan %d", len(ended))
	}
	prodTraceID := ended[0].SpanContext().TraceID()
	consTraceID := ended[1].SpanContext().TraceID()

	if prodTraceID != consTraceID {
		t.Fatalf("iz sürekliliği kırıldı: üretici trace_id=%s, tüketici trace_id=%s",
			prodTraceID, consTraceID)
	}
	if ended[1].Parent().SpanID() != ended[0].SpanContext().SpanID() {
		t.Fatalf("tüketici span'i üretici span'inin altına bağlanmadı")
	}
}
