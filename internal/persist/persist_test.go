package persist

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
)

// sampleRecord, tel biçiminde örnek bir kayıttır.
func sampleRecord(t testing.TB) htswire.Record {
	t.Helper()
	ta := 12
	return htswire.Record{
		RunID:        uuid.New(),
		EventID:      uuid.New(),
		Time:         time.Date(2026, 8, 3, 9, 30, 0, 0, time.UTC),
		PseudoMSISDN: "pseudo-msisdn",
		PseudoIMEI:   "pseudo-imei",
		EventType:    "MOC",
		CellID:       uuid.New(),
		TAValue:      &ta,
		Scenario:     "A",
	}
}

// sampleTruth, tel biçiminde örnek bir ground truth satırıdır.
func sampleTruth(t testing.TB, partition string) htswire.GroundTruth {
	t.Helper()
	rule := 3
	return htswire.GroundTruth{
		RunID:        uuid.New(),
		EventID:      uuid.New(),
		Time:         time.Date(2026, 8, 3, 9, 30, 0, 0, time.UTC),
		AgentID:      17,
		Lat:          38.6748,
		Lon:          39.2225,
		Covered:      true,
		PartitionKey: partition,
		InjectedRule: &rule,
	}
}

// TestDecodeRecordRow_PreservesFields, dönüşümün alan kaybetmediğini doğrular.
func TestDecodeRecordRow_PreservesFields(t *testing.T) {
	want := sampleRecord(t)

	data, err := htswire.EncodeRecord(want)
	if err != nil {
		t.Fatalf("EncodeRecord: %v", err)
	}
	got, err := DecodeRecordRow(data)
	if err != nil {
		t.Fatalf("DecodeRecordRow: %v", err)
	}

	if got.RunID != want.RunID || got.EventID != want.EventID || got.CellID != want.CellID {
		t.Error("kimlik alanları kayboldu")
	}
	if !got.Time.Equal(want.Time) {
		t.Errorf("zaman %v, %v beklenir", got.Time, want.Time)
	}
	if got.PseudoMSISDN != want.PseudoMSISDN || got.PseudoIMEI != want.PseudoIMEI {
		t.Error("takma adlar kayboldu")
	}
	if got.TAValue == nil || *got.TAValue != *want.TAValue {
		t.Errorf("ta_value %v, %d beklenir", got.TAValue, *want.TAValue)
	}
	if got.EventType != want.EventType || got.Scenario != want.Scenario {
		t.Error("olay tipi / senaryo kayboldu")
	}
}

// TestDecodeRecordRow_NilTA, TA'sız kaydın NULL taşıdığını doğrular.
func TestDecodeRecordRow_NilTA(t *testing.T) {
	rec := sampleRecord(t)
	rec.TAValue = nil

	data, err := htswire.EncodeRecord(rec)
	if err != nil {
		t.Fatalf("EncodeRecord: %v", err)
	}
	got, err := DecodeRecordRow(data)
	if err != nil {
		t.Fatalf("DecodeRecordRow: %v", err)
	}
	if got.TAValue != nil {
		t.Errorf("ta_value %d, NULL beklenir", *got.TAValue)
	}
}

// TestDecodeGroundTruthRow_PreservesFields, GT dönüşümünü doğrular.
func TestDecodeGroundTruthRow_PreservesFields(t *testing.T) {
	want := sampleTruth(t, "V")

	data, err := htswire.EncodeGroundTruth(want)
	if err != nil {
		t.Fatalf("EncodeGroundTruth: %v", err)
	}
	got, err := DecodeGroundTruthRow(data)
	if err != nil {
		t.Fatalf("DecodeGroundTruthRow: %v", err)
	}

	if got.Lat != want.Lat || got.Lon != want.Lon {
		t.Errorf("konum (%g, %g), (%g, %g) beklenir", got.Lat, got.Lon, want.Lat, want.Lon)
	}
	if got.PartitionKey != "V" {
		t.Errorf("partition_key %q, \"V\" beklenir", got.PartitionKey)
	}
	if got.InjectedRule == nil || *got.InjectedRule != 3 {
		t.Errorf("injected_rule %v, 3 beklenir", got.InjectedRule)
	}
	if got.AgentID != want.AgentID || got.Covered != want.Covered {
		t.Error("ajan kimliği / kapsama kayboldu")
	}
}

// TestDecodeGroundTruthRow_RejectsBadPartition, geçersiz partition anahtarını
// çözümleme anında reddeder.
//
// Tabloya gitseydi CHECK ihlali tüm partiyi düşürürdü ve hangi mesajın bozuk
// olduğu görülmezdi.
func TestDecodeGroundTruthRow_RejectsBadPartition(t *testing.T) {
	gt := sampleTruth(t, "X")

	data, err := htswire.EncodeGroundTruth(gt)
	if err != nil {
		t.Fatalf("EncodeGroundTruth: %v", err)
	}
	if _, err := DecodeGroundTruthRow(data); err == nil {
		t.Fatal("geçersiz partition_key kabul edildi")
	}
}

// TestDecodeRow_RejectsGarbage, bozuk gövdeyi reddeder.
func TestDecodeRow_RejectsGarbage(t *testing.T) {
	if _, err := DecodeRecordRow([]byte("{bozuk")); err == nil {
		t.Error("bozuk kayıt gövdesi kabul edildi")
	}
	if _, err := DecodeGroundTruthRow([]byte("{bozuk")); err == nil {
		t.Error("bozuk GT gövdesi kabul edildi")
	}
}

// TestNew_Rejects, eksik yapılandırmayı reddeder.
func TestNew_Rejects(t *testing.T) {
	base := Config[int]{
		Brokers: []string{"localhost:9092"},
		Topic:   "t",
		Group:   "g",
		Decode:  func([]byte) (int, error) { return 0, nil },
		Write:   func(context.Context, []int) (int64, error) { return 0, nil },
	}

	cases := []struct {
		name string
		mut  func(*Config[int])
	}{
		{"broker yok", func(c *Config[int]) { c.Brokers = nil }},
		{"topic yok", func(c *Config[int]) { c.Topic = "" }},
		{"grup yok", func(c *Config[int]) { c.Group = "" }},
		{"çözümleyici yok", func(c *Config[int]) { c.Decode = nil }},
		{"yazıcı yok", func(c *Config[int]) { c.Write = nil }},
	}
	for _, tc := range cases {
		cfg := base
		tc.mut(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}
