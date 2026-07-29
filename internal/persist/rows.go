// Tel biçiminden veritabanı satırına dönüşümler.
//
// Dönüşüm burada, tüketici katmanında yapılır: `pkg/htswire` depolamayı
// tanımaz, `internal/storage/postgres` de tel biçimini. İkisini birbirine
// bağlayan tek yer burasıdır.

package persist

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
)

// DecodeRecordRow, `hts.records` mesajını tablo satırına çevirir.
func DecodeRecordRow(data []byte) (postgres.HTSRecordRow, error) {
	rec, err := htswire.DecodeRecord(data)
	if err != nil {
		return postgres.HTSRecordRow{}, err
	}
	return postgres.HTSRecordRow{
		RunID:        rec.RunID,
		EventID:      rec.EventID,
		Time:         rec.Time,
		PseudoMSISDN: rec.PseudoMSISDN,
		PseudoIMEI:   rec.PseudoIMEI,
		EventType:    rec.EventType,
		CellID:       rec.CellID,
		TAValue:      rec.TAValue,
		Scenario:     rec.Scenario,
	}, nil
}

// DecodeGroundTruthRow, `hts.groundtruth` mesajını tablo satırına çevirir.
//
// `partition_key` tel biçiminde dizedir; tablo CHAR(1) bekler ve 'C'/'V'
// dışındaki bir değer CHECK ihlaliyle tüm partiyi düşürür. Doğrulama bu
// yüzden çözümleme anında yapılır: hangi mesajın bozuk olduğu ancak burada
// bilinir.
func DecodeGroundTruthRow(data []byte) (postgres.GroundTruthRow, error) {
	gt, err := htswire.DecodeGroundTruth(data)
	if err != nil {
		return postgres.GroundTruthRow{}, err
	}
	if gt.PartitionKey != "C" && gt.PartitionKey != "V" {
		return postgres.GroundTruthRow{}, fmt.Errorf(
			"ground truth (olay %s): partition_key 'C' veya 'V' olmalı (%q)",
			gt.EventID, gt.PartitionKey)
	}
	return postgres.GroundTruthRow{
		RunID:        gt.RunID,
		EventID:      gt.EventID,
		Time:         gt.Time,
		AgentID:      gt.AgentID,
		Lat:          gt.Lat,
		Lon:          gt.Lon,
		Covered:      gt.Covered,
		PartitionKey: gt.PartitionKey,
		InjectedRule: gt.InjectedRule,
	}, nil
}
