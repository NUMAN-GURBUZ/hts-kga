// T-E02-15, T-E02-16 — HTS kaydı ve ground truth üretimi.
//
// Her olay **iki** kayıt üretir:
//
//	hts_records   : operatörün tutmuş olacağı kayıt — konum bilgisi YOK
//	ground_truth  : gerçekte olan — konum, kapsama, bölüm anahtarı, enjeksiyon etiketi
//
// İkisi `event_id` ile bağlıdır (ADR-01) ve iki ayrı Kafka topic'ine gider.
// `ground_truth` topic'i S2/S4'e ACL ile kapalıdır: kör testin birinci
// katmanı budur.
//
// # TA kendi hesabı değildir
//
// Timing Advance `pkg/ta`'dan gelir. Aynı paket analizde halkayı kurar
// (T-E03-04); iki taraf ayrı hesap yapsaydı kayıt ile çıkarım sessizce
// ayrışırdı. Bu dosyada `floor`, `78.12` veya `550` geçmez.
package event

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/agent"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/split"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// EventType, olay tipidir (hts_records.event_type, VARCHAR(8)).
type EventType string

const (
	// EventCall, sesli arama kaydıdır.
	EventCall EventType = "CALL"
	// EventSMS, kısa mesaj kaydıdır.
	EventSMS EventType = "SMS"
	// EventData, veri oturumu kaydıdır.
	EventData EventType = "DATA"
)

// eventTypes, olay tipi seçimi için sıralı listedir.
//
// Dağılım plan tarafından tanımlanmamıştır; tip kütle üretimini, kapsama
// hesabını veya metrikleri **etkilemez**. Bu yüzden ajan-tick hash'inden
// düzgün dağılımla seçilir ve bilimsel bir varsayım yapılmaz.
var eventTypes = [...]EventType{EventCall, EventSMS, EventData}

// HTSRecord, operatör kaydıdır (hts_records satırı).
//
// Konum alanı **yoktur**: kaydın gerçek konumu bilmemesi çalışmanın temel
// varsayımıdır. Analiz motoru buradan yalnızca hücre kimliği ve TA görür.
type HTSRecord struct {
	RunID        uuid.UUID
	EventID      uuid.UUID
	Time         time.Time
	PseudoMSISDN string
	PseudoIMEI   string
	EventType    EventType
	CellID       uuid.UUID
	// TAValue, Timing Advance değeridir; nil ise senaryoda TA yoktur.
	TAValue  *int
	Scenario string
}

// GroundTruth, gerçeğin kaydıdır (ground_truth satırı).
//
// S2 ve S4 bu yapıyı **hiçbir yoldan** göremez (ADR-04, K6).
type GroundTruth struct {
	RunID        uuid.UUID
	EventID      uuid.UUID
	Time         time.Time
	AgentID      int
	TrueLocation geo.WGS84
	// Covered, ajanın kapsama içinde olup olmadığıdır (ADR-08).
	Covered bool
	// PartitionKey, kalibrasyon/doğrulama ayrımıdır (pkg/split).
	PartitionKey split.Partition
	// InjectedRule, enjeksiyon etiketidir; nil ise kayıt temizdir (ADR-09).
	InjectedRule *int
}

// Builder, gözlemlerden kayıt çifti üretir.
//
// Değişmezdir; ajan goroutine'leri tarafından paylaşılabilir.
type Builder struct {
	runID     uuid.UUID
	scenario  string
	runStart  time.Time
	clock     agent.Clock
	projector *geo.Projector
	pseudo    *Pseudonymizer
	splitter  split.Splitter
	tech      ta.Technology
	taEnabled bool
}

// BuilderConfig, kayıt üreticisinin kurulum parametreleridir.
type BuilderConfig struct {
	// RunID, koşu kimliğidir (ADR-05).
	RunID uuid.UUID
	// Scenario, senaryo harfidir (A/B/C/D).
	Scenario string
	// RunStart, tick 0'ın karşılık geldiği zamandır.
	RunStart time.Time
	// Clock, tick → takvim dönüşümüdür.
	Clock agent.Clock
	// Projector, ENU → WGS84 dönüşümüdür (ground truth konumu için).
	Projector *geo.Projector
	// Pseudonymizer, takma ad üreticisidir.
	Pseudonymizer *Pseudonymizer
	// Splitter, 80/20 bölümleyicisidir (pkg/split).
	Splitter split.Splitter
	// Technology, TA teknolojisidir; TAEnabled false ise yok sayılır.
	Technology ta.Technology
	// TAEnabled, senaryoda TA olup olmadığıdır (timing_advance.enabled).
	TAEnabled bool
}

// NewBuilder, kayıt üreticisini kurar.
func NewBuilder(cfg BuilderConfig) (*Builder, error) {
	switch {
	case cfg.RunID == uuid.Nil:
		return nil, fmt.Errorf("kayıt üreticisi: run_id zorunlu (ADR-05)")
	case len(cfg.Scenario) != 1:
		return nil, fmt.Errorf("kayıt üreticisi: senaryo tek harf olmalı (%q)", cfg.Scenario)
	case cfg.Projector == nil:
		return nil, fmt.Errorf("kayıt üreticisi: ENU projeksiyonu zorunlu")
	case cfg.Pseudonymizer == nil:
		return nil, fmt.Errorf("kayıt üreticisi: takma ad üreticisi zorunlu")
	case cfg.Clock.TickMinutes() <= 0:
		return nil, fmt.Errorf("kayıt üreticisi: geçersiz saat")
	case cfg.RunStart.IsZero():
		return nil, fmt.Errorf("kayıt üreticisi: koşu başlangıcı zorunlu")
	case cfg.TAEnabled && !cfg.Technology.Valid():
		return nil, fmt.Errorf("kayıt üreticisi: TA açık ama teknoloji geçersiz")
	}

	return &Builder{
		runID:     cfg.RunID,
		scenario:  cfg.Scenario,
		runStart:  cfg.RunStart,
		clock:     cfg.Clock,
		projector: cfg.Projector,
		pseudo:    cfg.Pseudonymizer,
		splitter:  cfg.Splitter,
		tech:      cfg.Technology,
		taEnabled: cfg.TAEnabled,
	}, nil
}

// TimeAt, tick indeksinin takvim zamanını döndürür.
func (b *Builder) TimeAt(tick int) time.Time {
	return b.runStart.Add(time.Duration(tick*b.clock.TickMinutes()) * time.Minute)
}

// Pair, tek bir olayın kayıt çiftidir.
//
// Record nil ise olay yalnızca ground truth üretir: ya ajan kapsama dışıdır
// (ADR-08/3) ya da enjeksiyon kural 4 kaydı silmiştir (ADR-09).
type Pair struct {
	Record *HTSRecord
	Truth  GroundTruth
}

// Build, tek bir olay için kayıt çiftini üretir.
//
// seq, aynı ajanın aynı tick içindeki olay sırasıdır (Poisson birden fazla
// olay verebilir).
func (b *Builder) Build(obs agent.Observation, seq int) (Pair, error) {
	key := Key{RunID: b.runID, AgentID: obs.AgentID, Tick: obs.Tick, Seq: seq}
	eventID, err := NewID(key)
	if err != nil {
		return Pair{}, fmt.Errorf("kayıt üretimi: %w", err)
	}

	at := b.TimeAt(obs.Tick)
	truth := GroundTruth{
		RunID:        b.runID,
		EventID:      eventID,
		Time:         at,
		AgentID:      obs.AgentID,
		TrueLocation: b.projector.Inverse(obs.Pos),
		Covered:      obs.Serving.Covered,
		PartitionKey: b.splitter.Of(eventID),
	}

	// ADR-08/3: kapsama dışında olay üretilmez, yalnızca ground truth yazılır.
	if !obs.Serving.Covered {
		return Pair{Truth: truth}, nil
	}

	taValue, err := b.timingAdvance(obs.Serving.DistanceM)
	if err != nil {
		return Pair{}, fmt.Errorf("kayıt üretimi (olay %s): %w", eventID, err)
	}

	record := &HTSRecord{
		RunID:        b.runID,
		EventID:      eventID,
		Time:         at,
		PseudoMSISDN: b.pseudo.PseudoMSISDN(obs.AgentID),
		PseudoIMEI:   b.pseudo.PseudoIMEI(obs.AgentID),
		EventType:    b.eventType(obs, seq),
		CellID:       uuid.UUID(obs.Serving.CellID),
		TAValue:      taValue,
		Scenario:     b.scenario,
	}
	return Pair{Record: record, Truth: truth}, nil
}

// timingAdvance, yatay mesafeden TA değerini türetir.
//
// Hesap `pkg/ta`'dadır: analiz halkayı aynı paketten kurar, bu yüzden
// "kayıt ne diyorsa gerçek konum halkanın içindedir" garantisi yapısaldır.
func (b *Builder) timingAdvance(distanceM float64) (*int, error) {
	if !b.taEnabled {
		return nil, nil
	}
	value, err := ta.FromDistance(distanceM, b.tech)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// eventType, olay tipini deterministik olarak seçer.
func (b *Builder) eventType(obs agent.Observation, seq int) EventType {
	h := hashUnit(uint64(b.runID[0])|uint64(b.runID[1])<<8, streamEventGeneration,
		purposeEventType, obs.AgentID, obs.Tick*maxEventsPerTick+seq)
	idx := int(h * float64(len(eventTypes)))
	if idx >= len(eventTypes) {
		idx = len(eventTypes) - 1
	}
	return eventTypes[idx]
}
