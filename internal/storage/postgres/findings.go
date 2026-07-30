// T-E05-01c (ADR-28, ADR-31) — `integrity_findings` yazma ve talep defteri
// okuma.
//
// # İdempotanslık şemada, kodda değil
//
// `ON CONFLICT DO NOTHING` + `UNIQUE (run_id, event_id, rule_id)`. Kafka
// at-least-once semantiğinde tüketici çökerse parti yeniden işlenir; yinelenen
// bulgu bir hata değil beklenen durumdur. Kısıt olmasaydı yinelenen satır
// F.5'in precision denominatörünü şişirir ve ölçümü **sessizce** düşürürdü.
//
// # Neden UPDATE/DELETE yok
//
// Bulgu tablosu ekle-yalnız bir adli kayıttır (ADR-27 reddedilen alternatif B).
// `svc_integrity` rolünün o yetkileri yoktur ve verilmeyecektir. Bastırma bile
// güncelleme değildir: `suppressed_by` satır yazılırken belirlenir (ADR-28/7
// "ilk talep kazanır" kuralının nedeni de budur).

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// FindingRow, `integrity_findings` tablosunun bir satırıdır.
type FindingRow struct {
	RunID    uuid.UUID
	EventID  uuid.UUID
	Time     time.Time
	RuleID   int
	RuleName string
	Margin   float64
	Evidence []byte // JSONB gövdesi (serileştirilmiş)
	Scenario string
	// DetectedIn, "stream" veya "batch" (ADR-27).
	DetectedIn string
	// SuppressedBy nil ise bulgu kanoniktir (ADR-28/4).
	SuppressedBy *int
}

// insertFindingsSQL, bulguları toplu ekler.
const insertFindingsSQL = `
INSERT INTO integrity_findings (
    run_id, event_id, time, rule_id, rule_name,
    margin, evidence, scenario, detected_in, suppressed_by
)
SELECT
    f.run_id, f.event_id, f.time, f.rule_id, f.rule_name,
    f.margin, f.evidence, f.scenario, f.detected_in, f.suppressed_by
FROM unnest(
    $1::uuid[], $2::uuid[], $3::timestamptz[], $4::int[], $5::varchar[],
    $6::float8[], $7::jsonb[], $8::varchar[], $9::varchar[], $10::int[]
) AS f(
    run_id, event_id, time, rule_id, rule_name,
    margin, evidence, scenario, detected_in, suppressed_by
)
ON CONFLICT DO NOTHING`

// InsertFindings, bulguları toplu yazar ve eklenen satır sayısını döndürür.
//
// Dönen sayı verilen satır sayısından küçük olabilir: fark yinelenen
// bulgulardır ve idempotanslığın çalıştığının kanıtıdır.
func (p *Pool) InsertFindings(ctx context.Context, rows []FindingRow) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	n := len(rows)
	runID := make([]string, n)
	eventID := make([]string, n)
	at := make([]time.Time, n)
	ruleID := make([]int32, n)
	ruleName := make([]string, n)
	margin := make([]float64, n)
	evidence := make([][]byte, n)
	scenario := make([]string, n)
	detectedIn := make([]string, n)
	suppressedBy := make([]*int32, n)

	for i, r := range rows {
		if err := r.validate(i); err != nil {
			return 0, err
		}

		runID[i] = r.RunID.String()
		eventID[i] = r.EventID.String()
		at[i] = r.Time.UTC()
		ruleID[i] = int32(r.RuleID)
		ruleName[i] = r.RuleName
		margin[i] = r.Margin
		evidence[i] = r.Evidence
		scenario[i] = r.Scenario
		detectedIn[i] = r.DetectedIn
		if r.SuppressedBy != nil {
			v := int32(*r.SuppressedBy)
			suppressedBy[i] = &v
		}
	}

	tag, err := p.pool.Exec(ctx, insertFindingsSQL,
		runID, eventID, at, ruleID, ruleName,
		margin, evidence, scenario, detectedIn, suppressedBy)
	if err != nil {
		return 0, fmt.Errorf("integrity_findings toplu yazma başarısız (%d satır): %w", len(rows), err)
	}
	return tag.RowsAffected(), nil
}

// validate, satırı şema kısıtlarına karşı önceden denetler.
//
// Veritabanı da reddederdi; ama reddedilen parti **tüm** bulguları düşürürdü
// (zehirli satır). Hata burada, satırın hangisi olduğu bilinerek verilir.
func (r FindingRow) validate(i int) error {
	switch {
	case r.RunID == uuid.Nil || r.EventID == uuid.Nil:
		return fmt.Errorf("integrity_findings[%d]: run_id/event_id boş olamaz", i)
	case r.Time.IsZero():
		return fmt.Errorf("integrity_findings[%d]: zaman damgası boş", i)
	case r.RuleID < 1 || r.RuleID > 5:
		return fmt.Errorf("integrity_findings[%d]: rule_id 1..5 olmalı (%d)", i, r.RuleID)
	case r.RuleName == "":
		return fmt.Errorf("integrity_findings[%d]: rule_name boş", i)
	case len(r.RuleName) > 50:
		return fmt.Errorf("integrity_findings[%d]: rule_name VARCHAR(50)'e sığmıyor (%d bayt)", i, len(r.RuleName))
	case r.Margin < 1.0 || r.Margin > 1e6:
		return fmt.Errorf("integrity_findings[%d]: margin [1, 1e6] aralığında olmalı (%g) — ADR-31/4", i, r.Margin)
	case len(r.Evidence) == 0:
		return fmt.Errorf("integrity_findings[%d]: kanıt gövdesi boş", i)
	case len(r.Scenario) != 1:
		return fmt.Errorf("integrity_findings[%d]: scenario tek harf olmalı (%q)", i, r.Scenario)
	case r.DetectedIn != "stream" && r.DetectedIn != "batch":
		return fmt.Errorf("integrity_findings[%d]: detected_in 'stream' veya 'batch' olmalı (%q)", i, r.DetectedIn)
	case r.SuppressedBy != nil && (*r.SuppressedBy < 1 || *r.SuppressedBy > 5):
		return fmt.Errorf("integrity_findings[%d]: suppressed_by 1..5 olmalı (%d)", i, *r.SuppressedBy)
	case r.SuppressedBy != nil && *r.SuppressedBy == r.RuleID:
		return fmt.Errorf("integrity_findings[%d]: kural kendisini bastıramaz (%d)", i, r.RuleID)
	}

	// Kanıt sürümü şema kısıtıyla (evidence ? 'v') aynı kapıdan geçer.
	var probe map[string]any
	if err := json.Unmarshal(r.Evidence, &probe); err != nil {
		return fmt.Errorf("integrity_findings[%d]: kanıt geçerli JSON değil: %w", i, err)
	}
	if _, ok := probe["v"]; !ok {
		return fmt.Errorf("integrity_findings[%d]: kanıt sürümsüz (evidence ? 'v' kısıtı) — ADR-31/5", i)
	}
	return nil
}

// SelectCanonicalClaims, koşunun kanonik bulgularını (olay → kural) döndürür.
//
// ADR-28/6: toplu faz talep defterini buradan yükler. Bellekte taşınsaydı
// toplu fazı tek başına yeniden koşmak öncelik bilgisini kaybederdi.
//
// Yalnızca `suppressed_by IS NULL` satırları okunur: bastırılmış bir isabet
// talep sahibi değildir.
func (p *Pool) SelectCanonicalClaims(ctx context.Context, runID uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := p.pool.Query(ctx, `
        SELECT event_id, rule_id
          FROM integrity_findings
         WHERE run_id = $1::uuid
           AND suppressed_by IS NULL`, runID.String())
	if err != nil {
		return nil, fmt.Errorf("talep defteri okunamadı (%s): %w", runID, err)
	}
	defer rows.Close()

	claims := make(map[uuid.UUID]int)
	for rows.Next() {
		var eventID uuid.UUID
		var ruleID int
		if err := rows.Scan(&eventID, &ruleID); err != nil {
			return nil, fmt.Errorf("talep satırı okunamadı: %w", err)
		}
		claims[eventID] = ruleID
	}
	return claims, rows.Err()
}

// CountFindings, koşunun bulgu sayısını döndürür (toplam ve kanonik).
func (p *Pool) CountFindings(ctx context.Context, runID uuid.UUID) (total, canonical int64, err error) {
	err = p.pool.QueryRow(ctx, `
        SELECT count(*), count(*) FILTER (WHERE suppressed_by IS NULL)
          FROM integrity_findings
         WHERE run_id = $1::uuid`, runID.String()).Scan(&total, &canonical)
	if err != nil {
		return 0, 0, fmt.Errorf("bulgu sayımı başarısız (%s): %w", runID, err)
	}
	return total, canonical, nil
}

// SubscriberSequence, bir abonenin olay-zamanı sıralı kayıtlarıdır (toplu faz).
type SubscriberSequence struct {
	Subscriber string
	Records    []HTSRecordRow
}

// ForEachSubscriberSequence, koşunun kayıtlarını abone bazlı diziler hâlinde
// akıtır (ADR-27 toplu faz).
//
// # Neden tek sıralı tarama
//
// Sorgu `ORDER BY pseudo_msisdn, time, event_id` ile tek geçişte okunur ve
// abone değiştiğinde dizi teslim edilir. Alternatif — abone listesi çekip her
// biri için ayrı sorgu — 1000 gidiş-dönüş demekti.
//
// Bellek tek abonenin dizisiyle sınırlıdır (~300 kayıt, ~30 KB): kayıt
// sayısıyla büyüyen bir tampon yok.
//
// `(time, event_id)` ikincil sırası determinizm içindir: aynı damgayı taşıyan
// tick-içi olaylar her koşuda aynı sırada gelir (K10).
func (p *Pool) ForEachSubscriberSequence(
	ctx context.Context, runID uuid.UUID, fn func(SubscriberSequence) error,
) error {
	rows, err := p.pool.Query(ctx, `
        SELECT run_id, event_id, time, pseudo_msisdn, coalesce(pseudo_imei, ''),
               event_type, cell_id, ta_value, scenario
          FROM hts_records
         WHERE run_id = $1::uuid
         ORDER BY pseudo_msisdn, time, event_id`, runID.String())
	if err != nil {
		return fmt.Errorf("abone dizisi okunamadı (%s): %w", runID, err)
	}
	defer rows.Close()

	var current SubscriberSequence
	for rows.Next() {
		var r HTSRecordRow
		var ta *int32
		if err := rows.Scan(&r.RunID, &r.EventID, &r.Time, &r.PseudoMSISDN, &r.PseudoIMEI,
			&r.EventType, &r.CellID, &ta, &r.Scenario); err != nil {
			return fmt.Errorf("kayıt satırı okunamadı: %w", err)
		}
		if ta != nil {
			v := int(*ta)
			r.TAValue = &v
		}

		if r.PseudoMSISDN != current.Subscriber {
			if len(current.Records) > 0 {
				if err := fn(current); err != nil {
					return err
				}
			}
			current = SubscriberSequence{Subscriber: r.PseudoMSISDN}
		}
		current.Records = append(current.Records, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("abone dizisi taraması yarıda kesildi: %w", err)
	}

	if len(current.Records) > 0 {
		return fn(current)
	}
	return nil
}

// SetInspectedRecords, bütünlük akış fazının incelediği kayıt sayısını yazar
// (ADR-31/7).
//
// `verify_integrity` beşinci denetimi bu sayıyı `published_events` ile
// karşılaştırır: yarım koşan bir S4'ün düşük recall'u böylece bilimsel bulgu
// gibi görünemez.
func (p *Pool) SetInspectedRecords(ctx context.Context, runID uuid.UUID, n int64) error {
	tag, err := p.pool.Exec(ctx, `
        UPDATE run_config SET inspected_records = $2 WHERE run_id = $1::uuid`,
		runID.String(), n)
	if err != nil {
		return fmt.Errorf("inspected_records yazılamadı (%s): %w", runID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("inspected_records yazılamadı: koşu bulunamadı (%s)", runID)
	}
	return nil
}
