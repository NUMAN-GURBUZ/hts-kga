// G2 (ADR-22) — `hts_records` tablosuna toplu yazma ve okuma.
//
// Yazma yolu Kafka tüketicisinden gelir (internal/persist): simülatör
// veritabanına doğrudan yazmaz. Gerekçe ADR-22'dedir — özetle, aynı verinin
// iki üretim yolu olsaydı ADR-01 bütünlük denetimi aralarındaki sapmayı
// göremezdi (ikisi de aynı süreçten çıkardı).
//
// Okuma yolu kalibrasyon içindir (ADR-02): bisection döngüsü aynı olayları
// 12 kez yeniden işler ve bunu Kafka'dan yapamaz — akış bir kez tüketilir.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// HTSRecordRow, `hts_records` tablosunun bir satırıdır.
type HTSRecordRow struct {
	RunID        uuid.UUID
	EventID      uuid.UUID
	Time         time.Time
	PseudoMSISDN string
	PseudoIMEI   string
	EventType    string
	CellID       uuid.UUID
	TAValue      *int
	Scenario     string
}

// insertRecordsSQL, kayıtları toplu ekler.
//
// `ON CONFLICT DO NOTHING`: tüketici yeniden başladığında offset commit
// edilmemiş kayıtlar yeniden işlenir (at-least-once). Yinelenen satır bir hata
// değil, beklenen durumdur; UNIQUE indeks (run_id, event_id, time) tekilliği
// zaten garanti eder.
const insertRecordsSQL = `
INSERT INTO hts_records (
    run_id, event_id, time, pseudo_msisdn, pseudo_imei,
    event_type, cell_id, ta_value, scenario
)
SELECT
    r.run_id, r.event_id, r.time, r.pseudo_msisdn, r.pseudo_imei,
    r.event_type, r.cell_id, r.ta_value, r.scenario
FROM unnest(
    $1::uuid[], $2::uuid[], $3::timestamptz[], $4::varchar[], $5::varchar[],
    $6::varchar[], $7::uuid[], $8::int[], $9::varchar[]
) AS r(
    run_id, event_id, time, pseudo_msisdn, pseudo_imei,
    event_type, cell_id, ta_value, scenario
)
ON CONFLICT DO NOTHING`

// InsertHTSRecords, kayıtları toplu yazar ve eklenen satır sayısını döndürür.
func (p *Pool) InsertHTSRecords(ctx context.Context, rows []HTSRecordRow) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	n := len(rows)
	runID := make([]string, n)
	eventID := make([]string, n)
	at := make([]time.Time, n)
	msisdn := make([]string, n)
	imei := make([]string, n)
	eventType := make([]string, n)
	cellID := make([]string, n)
	taValue := make([]*int32, n)
	scenario := make([]string, n)

	for i, r := range rows {
		switch {
		case r.RunID == uuid.Nil || r.EventID == uuid.Nil:
			return 0, fmt.Errorf("hts_records[%d]: run_id/event_id boş olamaz (ADR-01)", i)
		case r.CellID == uuid.Nil:
			return 0, fmt.Errorf("hts_records[%d]: cell_id boş olamaz", i)
		case r.Time.IsZero():
			return 0, fmt.Errorf("hts_records[%d]: zaman damgası boş (bölümleme sütunu)", i)
		case r.PseudoMSISDN == "":
			return 0, fmt.Errorf("hts_records[%d]: takma ad boş (E-08)", i)
		case len(r.Scenario) != 1:
			return 0, fmt.Errorf("hts_records[%d]: scenario tek harf olmalı (%q)", i, r.Scenario)
		}

		runID[i] = r.RunID.String()
		eventID[i] = r.EventID.String()
		at[i] = r.Time.UTC()
		msisdn[i] = r.PseudoMSISDN
		imei[i] = r.PseudoIMEI
		eventType[i] = r.EventType
		cellID[i] = r.CellID.String()
		scenario[i] = r.Scenario
		if r.TAValue != nil {
			v := int32(*r.TAValue)
			taValue[i] = &v
		}
	}

	tag, err := p.pool.Exec(ctx, insertRecordsSQL,
		runID, eventID, at, msisdn, imei, eventType, cellID, taValue, scenario)
	if err != nil {
		return 0, fmt.Errorf("hts_records toplu yazma başarısız (%d satır): %w", len(rows), err)
	}
	return tag.RowsAffected(), nil
}

// CountHTSRecords, bir koşuya ait kayıt sayısını döndürür.
func (p *Pool) CountHTSRecords(ctx context.Context, runID uuid.UUID) (int64, error) {
	var n int64
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM hts_records WHERE run_id = $1::uuid`, runID.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("kayıt sayımı başarısız (%s): %w", runID, err)
	}
	return n, nil
}

// SelectHTSRecords, koşunun kayıtlarını deterministik sırada okur.
//
// Sıra `(time, event_id)`'dir: kalibrasyonun 12 iterasyonu aynı kayıtları aynı
// sırada işlemelidir, yoksa kayan nokta toplamları iterasyonlar arasında
// ayrışır ve λ* ölçülen şeye değil sıraya bağlı olurdu (K10).
//
// `ground_truth` tablosuna **dokunmaz**: bu sorgu doğrulama servisinde de
// analiz tarafında da kullanılabilir olmalıdır ve kör testi delmemelidir.
// C/V ayrımı çağıran tarafta `pkg/split` ile türetilir.
func (p *Pool) SelectHTSRecords(ctx context.Context, runID uuid.UUID) ([]HTSRecordRow, error) {
	rows, err := p.pool.Query(ctx, `
        SELECT run_id, event_id, time, pseudo_msisdn, coalesce(pseudo_imei, ''),
               event_type, cell_id, ta_value, scenario
          FROM hts_records
         WHERE run_id = $1::uuid
         ORDER BY time, event_id`, runID.String())
	if err != nil {
		return nil, fmt.Errorf("kayıt okuma başarısız (%s): %w", runID, err)
	}
	defer rows.Close()

	var out []HTSRecordRow
	for rows.Next() {
		var r HTSRecordRow
		var ta *int32
		if err := rows.Scan(&r.RunID, &r.EventID, &r.Time, &r.PseudoMSISDN, &r.PseudoIMEI,
			&r.EventType, &r.CellID, &ta, &r.Scenario); err != nil {
			return nil, fmt.Errorf("kayıt satırı okunamadı: %w", err)
		}
		if ta != nil {
			v := int(*ta)
			r.TAValue = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
