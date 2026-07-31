// T-E09 — consumer lag ölçümü (ADR-34/2, "lag" gereksinimi).

package kafka

import (
	"context"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// RegisterLagGauge, tüketici grubunun partition başına gecikmesini
// gözlemlenebilir ölçer olarak kaydeder.
//
// Callback yalnızca `/metrics` scrape edildiğinde çalışır (Prometheus pull
// modeli, OTel ObservableGauge sözleşmesi) — ayrı bir polling goroutine
// gerekmez ve tüketici döngüsünün tek-goroutine varsayımına dokunulmaz.
func RegisterLagGauge(meter metric.Meter, client *kgo.Client, group string) error {
	gauge, err := meter.Int64ObservableGauge(
		"hts_kafka_consumer_lag",
		metric.WithDescription("Consumer group için partition başına Kafka gecikmesi"),
	)
	if err != nil {
		return err
	}

	adm := kadm.NewClient(client)
	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		lagCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		lags, lagErr := adm.Lag(lagCtx, group)
		if lagErr != nil {
			// Kafka geçici olarak erişilemezse metrik toplama durmaz —
			// bir sonraki scrape'te yeniden denenir.
			slog.Warn("kafka lag ölçülemedi", "grup", group, "hata", lagErr)
			return nil
		}
		lags.Each(func(l kadm.DescribedGroupLag) {
			for _, partitions := range l.Lag {
				for _, pl := range partitions {
					if pl.Lag < 0 {
						continue
					}
					o.ObserveInt64(gauge, pl.Lag,
						metric.WithAttributes(
							attribute.String("group", group),
							attribute.String("topic", pl.Topic),
							attribute.Int("partition", int(pl.Partition)),
						))
				}
			}
		})
		return nil
	}, gauge)
	return err
}
