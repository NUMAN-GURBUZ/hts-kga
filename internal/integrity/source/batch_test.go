package source

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
)

// stubReader, toplu fazın veritabanı yüzeyini taklit eder.
type stubReader struct {
	status postgres.RunStatus
	stored int64
	seqs   []postgres.SubscriberSequence
}

func (r stubReader) RunStatusOf(context.Context, uuid.UUID) (postgres.RunStatus, error) {
	return r.status, nil
}

func (r stubReader) CountHTSRecords(context.Context, uuid.UUID) (int64, error) {
	return r.stored, nil
}

func (r stubReader) ForEachSubscriberSequence(
	_ context.Context, _ uuid.UUID, fn func(postgres.SubscriberSequence) error,
) error {
	for _, s := range r.seqs {
		if err := fn(s); err != nil {
			return err
		}
	}
	return nil
}

func ptr(v int64) *int64 { return &v }

var runID = uuid.MustParse("44444444-4444-4444-4444-444444444444")

// TestCheckPrecondition, iki önkoşulun her ihlal biçimini sınar.
//
// Önkoşul atlanabilirse eksik taleple koşulur ve düşük recall bilimsel bulgu
// gibi görünür. Bu testin her satırı gerçek bir sessiz bozulma yoludur.
func TestCheckPrecondition(t *testing.T) {
	tests := []struct {
		name    string
		reader  stubReader
		wantErr string
	}{
		{
			name: "simülasyon bitmedi (published_events NULL)",
			reader: stubReader{
				status: postgres.RunStatus{},
			},
			wantErr: "published_events yazılmamış",
		},
		{
			name: "kayıtlar eksik (persister bitmedi)",
			reader: stubReader{
				status: postgres.RunStatus{PublishedEvents: ptr(1000)},
				stored: 800,
			},
			wantErr: "hts_records eksik (800/1000)",
		},
		{
			name: "akış fazı koşmadı (inspected_records NULL)",
			reader: stubReader{
				status: postgres.RunStatus{PublishedEvents: ptr(1000)},
				stored: 1000,
			},
			wantErr: "inspected_records yazılmamış",
		},
		{
			name: "akış fazı yarım kaldı",
			reader: stubReader{
				status: postgres.RunStatus{
					PublishedEvents:  ptr(1000),
					InspectedRecords: ptr(225), // Sprint 5 hatası #4'ün büyüklüğü
				},
				stored: 1000,
			},
			wantErr: "akış fazı yarım kaldı (225/1000 incelendi)",
		},
		{
			name: "tam koşu — önkoşul sağlanır",
			reader: stubReader{
				status: postgres.RunStatus{
					PublishedEvents:  ptr(1000),
					InspectedRecords: ptr(1000),
				},
				stored: 1000,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := NewBatch(BatchConfig{RunID: runID}, tc.reader, nil)
			if err == nil {
				t.Fatal("motorsuz kurulum reddedilmeliydi")
			}

			// Motor gerekmiyor: CheckPrecondition ona dokunmuyor. Yine de
			// NewBatch motoru zorunlu kıldığı için sahte bir yapı kurulur.
			b = &Batch{reader: tc.reader, cfg: BatchConfig{RunID: runID}, log: testLogger()}

			err = b.CheckPrecondition(context.Background())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("önkoşul sağlanmalıydı: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("hata bekleniyordu (%q)", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("hata mesajı %q içermeli:\n%v", tc.wantErr, err)
			}
		})
	}
}

// TestNewBatch_Validation, kurulum denetimlerini sınar.
func TestNewBatch_Validation(t *testing.T) {
	if _, err := NewBatch(BatchConfig{}, stubReader{}, nil); err == nil {
		t.Error("run_id ve motor olmadan kurulmamalı")
	}
	if _, err := NewBatch(BatchConfig{RunID: runID}, nil, nil); err == nil {
		t.Error("okuyucu olmadan kurulmamalı")
	}
}

// TestToSequence_PreservesFields, veritabanı satırlarının dedektör kaydına
// eksiksiz çevrildiğini sınar.
func TestToSequence_PreservesFields(t *testing.T) {
	ta := 12
	raw := postgres.SubscriberSequence{
		Subscriber: "msisdn-1",
		Records: []postgres.HTSRecordRow{{
			RunID:        runID,
			EventID:      uuid.New(),
			PseudoMSISDN: "msisdn-1",
			PseudoIMEI:   "imei-1",
			EventType:    "MOC",
			CellID:       uuid.New(),
			TAValue:      &ta,
			Scenario:     "A",
		}},
	}

	seq := toSequence(raw)
	if seq.Subscriber != "msisdn-1" || seq.Len() != 1 {
		t.Fatalf("dizi çevrilemedi: %+v", seq)
	}
	rec := seq.Records[0]
	if rec.Subscriber != "msisdn-1" || rec.Device != "imei-1" {
		t.Errorf("abone/cihaz alanları yanlış: %+v", rec)
	}
	if rec.TAValue == nil || *rec.TAValue != 12 {
		t.Errorf("ta_value taşınmadı: %v", rec.TAValue)
	}
	if rec.CellID != raw.Records[0].CellID || rec.EventID != raw.Records[0].EventID {
		t.Error("kimlikler taşınmadı")
	}
}

// testLogger, testlerde gürültüyü bastırır.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
