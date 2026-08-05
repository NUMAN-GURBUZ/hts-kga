// T-E04-01 (ADR-04) — `ground_truth` tablosuna toplu yazma.
//
// # Bu dosyaya kimin eriştiği önemlidir
//
// `ground_truth`, kör testin (K6) koruduğu tablodur. Yazan tek bileşen S3a
// ground truth persister'ıdır ve `svc_gt_persister` rolüyle çalışır. Analiz
// (`svc_analysis`) ve bütünlük (`svc_integrity`) rollerinin bu tabloya SELECT
// yetkisi yoktur (004_roles.sql) — bu paketteki fonksiyonları çağırsalar bile
// veritabanı reddeder.
//
// Katman kuralı gereği burada alan tipi yoktur: `true_location` enlem/boylam
// olarak taşınır ve GEOGRAPHY sunucu tarafında kurulur.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// GroundTruthRow, `ground_truth` tablosunun bir satırıdır.
type GroundTruthRow struct {
	RunID        uuid.UUID
	EventID      uuid.UUID
	Time         time.Time
	AgentID      int
	Lat          float64
	Lon          float64
	Covered      bool
	PartitionKey string // 'C' | 'V'
	// InjectedRule, enjeksiyon etiketidir; nil ise kayıt temizdir (ADR-09).
	InjectedRule *int
}

// insertGroundTruthSQL, ground truth satırlarını toplu ekler.
//
// Konum sunucuda kurulur: ST_MakePoint(**lon**, **lat**) — sıra terstir ve
// karıştırılırsa PostGIS sessizce kabul eder, geometri dünyanın öbür ucuna
// düşer. Sıra bu yüzden tek bir yerde, burada sabitlenmiştir.
const insertGroundTruthSQL = `
INSERT INTO ground_truth (
    run_id, event_id, time, agent_id, true_location,
    covered, partition_key, injected_rule
)
SELECT
    g.run_id, g.event_id, g.time, g.agent_id,
    ST_SetSRID(ST_MakePoint(g.lon, g.lat), 4326)::geography,
    g.covered, g.partition_key, g.injected_rule
FROM unnest(
    $1::uuid[], $2::uuid[], $3::timestamptz[], $4::int[],
    $5::float8[], $6::float8[], $7::bool[], $8::varchar[], $9::int[]
) AS g(
    run_id, event_id, time, agent_id, lon, lat, covered, partition_key, injected_rule
)
ON CONFLICT DO NOTHING`

// InsertGroundTruth, ground truth satırlarını toplu yazar.
func (p *Pool) InsertGroundTruth(ctx context.Context, rows []GroundTruthRow) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	n := len(rows)
	runID := make([]string, n)
	eventID := make([]string, n)
	at := make([]time.Time, n)
	agentID := make([]int32, n)
	lon := make([]float64, n)
	lat := make([]float64, n)
	covered := make([]bool, n)
	partition := make([]string, n)
	injected := make([]*int32, n)

	for i, r := range rows {
		switch {
		case r.RunID == uuid.Nil || r.EventID == uuid.Nil:
			return 0, fmt.Errorf("ground_truth[%d]: run_id/event_id boş olamaz (ADR-01)", i)
		case r.Time.IsZero():
			return 0, fmt.Errorf("ground_truth[%d]: zaman damgası boş (bölümleme sütunu)", i)
		case r.PartitionKey != "C" && r.PartitionKey != "V":
			return 0, fmt.Errorf("ground_truth[%d]: partition_key 'C' veya 'V' olmalı (%q)",
				i, r.PartitionKey)
		case r.Lat < -90 || r.Lat > 90 || r.Lon < -180 || r.Lon > 180:
			return 0, fmt.Errorf("ground_truth[%d]: geçersiz konum (%g, %g)", i, r.Lat, r.Lon)
		}
		if r.InjectedRule != nil && (*r.InjectedRule < 1 || *r.InjectedRule > 5) {
			return 0, fmt.Errorf("ground_truth[%d]: injected_rule 1..5 olmalı (%d)",
				i, *r.InjectedRule)
		}

		runID[i] = r.RunID.String()
		eventID[i] = r.EventID.String()
		at[i] = r.Time.UTC()
		agentID[i] = int32(r.AgentID)
		lon[i], lat[i] = r.Lon, r.Lat
		covered[i] = r.Covered
		partition[i] = r.PartitionKey
		if r.InjectedRule != nil {
			v := int32(*r.InjectedRule)
			injected[i] = &v
		}
	}

	tag, err := p.pool.Exec(ctx, insertGroundTruthSQL,
		runID, eventID, at, agentID, lon, lat, covered, partition, injected)
	if err != nil {
		return 0, fmt.Errorf("ground_truth toplu yazma başarısız (%d satır): %w", len(rows), err)
	}
	return tag.RowsAffected(), nil
}

// CountGroundTruth, bir koşuya ait ground truth satır sayısını döndürür.
func (p *Pool) CountGroundTruth(ctx context.Context, runID uuid.UUID) (int64, error) {
	var n int64
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM ground_truth WHERE run_id = $1::uuid`, runID.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("ground truth sayımı başarısız (%s): %w", runID, err)
	}
	return n, nil
}
