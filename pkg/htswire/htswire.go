// Package htswire, Kafka'da taşınan kayıtların tel biçimini tanımlar.
//
// # Bu paketin var oluş nedeni
//
// Biçim iki tarafta kullanılır:
//
//	simülatör (T-E02-18) : publisher, kaydı JSON'a çevirir
//	analiz    (T-E03-01) : consumer, JSON'u geri çözer
//
// İki taraf birbirini import **edemez**: ADR-20 analiz katmanının
// internal/simulator'a bağımlı olmasını yasaklar. Biçim iki yerde ayrı
// tanımlansaydı bir alan adı değişikliği derleme hatası vermez, sessizce
// çözümlenemeyen kayıt üretirdi — `pkg/ta` ve `pkg/split` ile birebir aynı
// ikiz tuzağı.
//
// Bu yüzden sözleşme buradadır ve iç bağımlılığı yoktur (stdlib + uuid).
//
// # Alan adları veritabanı sütunlarıyla aynıdır
//
// `hts_records` ve `ground_truth` sütun adları birebir kullanılır. Böylece
// S3a persister için ayrı eşleme tablosu gerekmez ve kayıtlar
// `kafka-console-consumer` ile elle okunabilir kalır — hata ayıklamada ve
// savunmada bu değerlidir.
//
// # injected_rule yalnızca ground truth'ta
//
// `Record` yapısında enjeksiyon etiketi **yoktur ve eklenmeyecektir** (ADR-09).
// S4 yalnızca `Record`'u görür; etiketi görseydi bütünlük tespiti kör test
// olmaktan çıkardı.
package htswire

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Record, `hts_records` satırının tel biçimidir.
//
// Konum alanı yoktur: kaydın gerçek konumu bilmemesi çalışmanın temel
// varsayımıdır.
type Record struct {
	RunID        uuid.UUID `json:"run_id"`
	EventID      uuid.UUID `json:"event_id"`
	Time         time.Time `json:"time"`
	PseudoMSISDN string    `json:"pseudo_msisdn"`
	PseudoIMEI   string    `json:"pseudo_imei"`
	EventType    string    `json:"event_type"`
	CellID       uuid.UUID `json:"cell_id"`
	// TAValue nil ise senaryoda TA yoktur (ta_value NULL).
	TAValue  *int   `json:"ta_value"`
	Scenario string `json:"scenario"`
}

// GroundTruth, `ground_truth` satırının tel biçimidir.
//
// Bu yapı yalnızca `hts.groundtruth` topic'inde taşınır; o topic S2 ve S4
// principal'lerine ACL ile kapalıdır (kör test, katman 1).
type GroundTruth struct {
	RunID   uuid.UUID `json:"run_id"`
	EventID uuid.UUID `json:"event_id"`
	Time    time.Time `json:"time"`
	AgentID int       `json:"agent_id"`
	Lat     float64   `json:"lat"`
	Lon     float64   `json:"lon"`
	Covered bool      `json:"covered"`
	// PartitionKey, 'C' (kalibrasyon) veya 'V' (doğrulama) — pkg/split.
	PartitionKey string `json:"partition_key"`
	// InjectedRule nil ise kayıt temizdir; 1..5 enjeksiyon kuralıdır (ADR-09).
	InjectedRule *int `json:"injected_rule"`
}

// EncodeRecord, kaydı JSON'a çevirir.
func EncodeRecord(r Record) ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("kayıt serileştirilemedi (olay %s): %w", r.EventID, err)
	}
	return data, nil
}

// DecodeRecord, JSON'dan kaydı çözer ve zorunlu alanları denetler.
//
// Boş `event_id` veya `cell_id`, kaydın analizde kullanılamaz olduğu anlamına
// gelir; sessizce geçirmek yerine hata verilir.
func DecodeRecord(data []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, fmt.Errorf("kayıt çözümlenemedi: %w", err)
	}
	switch {
	case r.EventID == uuid.Nil:
		return Record{}, fmt.Errorf("kayıt çözümlenemedi: event_id boş")
	case r.CellID == uuid.Nil:
		return Record{}, fmt.Errorf("kayıt çözümlenemedi: cell_id boş (olay %s)", r.EventID)
	case r.RunID == uuid.Nil:
		return Record{}, fmt.Errorf("kayıt çözümlenemedi: run_id boş (olay %s)", r.EventID)
	}
	return r, nil
}

// EncodeGroundTruth, ground truth kaydını JSON'a çevirir.
func EncodeGroundTruth(g GroundTruth) ([]byte, error) {
	data, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("ground truth serileştirilemedi (olay %s): %w", g.EventID, err)
	}
	return data, nil
}

// DecodeGroundTruth, JSON'dan ground truth kaydını çözer.
func DecodeGroundTruth(data []byte) (GroundTruth, error) {
	var g GroundTruth
	if err := json.Unmarshal(data, &g); err != nil {
		return GroundTruth{}, fmt.Errorf("ground truth çözümlenemedi: %w", err)
	}
	if g.EventID == uuid.Nil {
		return GroundTruth{}, fmt.Errorf("ground truth çözümlenemedi: event_id boş")
	}
	return g, nil
}
