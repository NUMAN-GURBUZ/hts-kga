// T-E09 — bütünlük motorunun iş metrikleri (ADR-34/2).
//
// Motorun kendi sayaçlarına (internal/integrity/detector) dokunulmaz —
// yalnızca fazın sonunda zaten hesaplanan Stats() anlık görüntüsü OTel
// enstrümanlarına yansıtılır. Bu, engine.go'nun tek-goroutine varsayımını
// (Sprint 6, PBT ile doğrulanmış) korur.
package main

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/detector"
)

var integrityMeter = otel.Meter("github.com/NUMAN-GURBUZ/hts-kga/cmd/integrity")

var integrityRecordsTotal = mustInt64Counter(
	"hts_integrity_records_total",
	"Bütünlük motorunun faz sonunda ürettiği sayaçlar (Stats() anlık görüntüsü)",
)

func mustInt64Counter(name, desc string) metric.Int64Counter {
	c, err := integrityMeter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		panic("otel sayaç: " + err.Error())
	}
	return c
}

// recordStats, bir fazın bitişindeki Stats() anlık görüntüsünü OTel
// sayaçlarına yazar. Faz başına bir kez çağrılır (akış bitince, toplu
// bitince) — çağrı sırasında motorun hot path'i artık çalışmıyordur.
func recordStats(ctx context.Context, phase string, st detector.Stats) {
	add := func(outcome string, n int64) {
		if n == 0 {
			return
		}
		integrityRecordsTotal.Add(ctx, n, metric.WithAttributes(
			attribute.String("phase", phase),
			attribute.String("outcome", outcome),
		))
	}
	add("inspected", st.Inspected)
	add("canonical", st.Canonical)
	add("suppressed", st.Suppressed)
	add("invalid", st.Invalid)
	add("demoted", st.Demoted)
	add("written", st.Written)
}
