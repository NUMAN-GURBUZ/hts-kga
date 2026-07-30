// Kural motoru: faz yürütmesi, öncelik uygulaması ve bulgu tamponu
// (ADR-27, ADR-28).

package detector

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// defaultBufferSize, tek yazma sorgusuna giden bulgu sayısıdır.
//
// Bulgular seyrektir (koşu başına ~4.000); 500'lük tampon tüm koşuyu ~8
// sorguda yazar ve akış fazında offset commit'leri arasında birikmez.
const defaultBufferSize = 500

// Sink, bulguları kalıcılaştıran yazıcıdır.
//
// Eklenen satır sayısını döndürür. Yinelenen bulgu bir hata değildir:
// `ON CONFLICT DO NOTHING` beklenen davranıştır (ADR-31/2), bu yüzden dönen
// sayı verilen satır sayısından küçük olabilir.
type Sink interface {
	WriteFindings(ctx context.Context, findings []Finding) (int64, error)
}

// SinkFunc, işlev tipindeki Sink uyarlayıcısıdır.
type SinkFunc func(ctx context.Context, findings []Finding) (int64, error)

// WriteFindings, Sink arayüzünü uygular.
func (f SinkFunc) WriteFindings(ctx context.Context, findings []Finding) (int64, error) {
	return f(ctx, findings)
}

// Config, motorun kurulum parametreleridir.
type Config struct {
	// Phase, motorun koştuğu fazdır (ClassStream veya ClassBatch).
	//
	// Kurallar bu fazla uyumsuzsa kurulum **başarısız olur** (ADR-27/1):
	// yanlış faz bir çalışma zamanı hatası bile değildir.
	Phase integrityrule.Class
	// Claims, olay talep defteridir. nil ise boş defter açılır.
	Claims *Claims
	// Sink, bulgu yazıcısıdır (zorunlu).
	Sink Sink
	// MarginCap, `margin` alanının üst sınırıdır (ADR-31/4).
	// 0 ise DefaultMarginCap kullanılır.
	MarginCap float64
	// BufferSize, yazma tamponu eşiğidir. 0 ise varsayılan kullanılır.
	BufferSize int
	// Logger, isteğe bağlıdır.
	Logger *slog.Logger
}

// Engine, kuralları koşturur, önceliği uygular ve bulguları yazar.
//
// Eşzamanlı kullanım için güvenli değildir: akış fazı tek tüketiciden, toplu
// faz tek taramadan çağırır.
type Engine struct {
	phase  integrityrule.Class
	claims *Claims
	sink   Sink
	cap    float64
	size   int
	log    *slog.Logger

	stream []StreamRule
	batch  []BatchRule

	buffer []Finding

	// Sayaçlar — hiçbiri sessiz kalmaz, koşu sonunda raporlanır.
	inspected  int64 // görülen kayıt (akış) / dizi kaydı (toplu)
	canonical  int64 // kanonik bulgu
	suppressed int64 // bastırılmış bulgu
	invalid    int64 // geçerlilik denetiminden düşen isabet
	demoted    int64 // güçlü olduğu hâlde bastırılan isabet (ADR-28/7)
	written    int64 // veritabanına eklenen satır
}

// NewStreamEngine, akış fazı motorunu kurar.
func NewStreamEngine(cfg Config, rules ...StreamRule) (*Engine, error) {
	cfg.Phase = integrityrule.ClassStream
	e, err := newEngine(cfg)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if err := checkRuleClass(r.ID(), integrityrule.ClassStream); err != nil {
			return nil, err
		}
		e.stream = append(e.stream, r)
	}
	e.sortStreamRules()
	return e, nil
}

// NewBatchEngine, toplu faz motorunu kurar.
func NewBatchEngine(cfg Config, rules ...BatchRule) (*Engine, error) {
	cfg.Phase = integrityrule.ClassBatch
	e, err := newEngine(cfg)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if err := checkRuleClass(r.ID(), integrityrule.ClassBatch); err != nil {
			return nil, err
		}
		e.batch = append(e.batch, r)
	}
	e.sortBatchRules()
	return e, nil
}

// newEngine, ortak kurulumu yapar.
func newEngine(cfg Config) (*Engine, error) {
	if cfg.Sink == nil {
		return nil, fmt.Errorf("kural motoru: bulgu yazıcısı zorunlu")
	}
	if !cfg.Phase.Valid() {
		return nil, fmt.Errorf("kural motoru: geçersiz faz (%q)", cfg.Phase)
	}
	if cfg.Claims == nil {
		cfg.Claims = NewClaims()
	}
	if !(cfg.MarginCap > 0) {
		cfg.MarginCap = DefaultMarginCap
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = defaultBufferSize
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	return &Engine{
		phase:  cfg.Phase,
		claims: cfg.Claims,
		sink:   cfg.Sink,
		cap:    cfg.MarginCap,
		size:   cfg.BufferSize,
		log:    log,
		buffer: make([]Finding, 0, cfg.BufferSize),
	}, nil
}

// checkRuleClass, kuralın faza uygun olduğunu denetler (ADR-27/1).
func checkRuleClass(id integrityrule.ID, want integrityrule.Class) error {
	if !id.Valid() {
		return fmt.Errorf("kural motoru: tanımsız kural kimliği (%d)", int(id))
	}
	if got := id.Class(); got != want {
		return fmt.Errorf(
			"kural motoru: %s kuralı %q sınıfında, %q fazına alınamaz (ADR-27). "+
				"Kural 3'ün kanıtı varış sırasındadır, kural 5'in kanıtı kapalı "+
				"popülasyondadır; yanlış faz precision'ı çökertir",
			id, got, want)
	}
	return nil
}

// sortStreamRules, akış kurallarını öncelik sırasına dizer (ADR-28/1).
func (e *Engine) sortStreamRules() {
	sortByPrecedence(e.stream, func(r StreamRule) integrityrule.ID { return r.ID() })
}

// sortBatchRules, toplu kuralları öncelik sırasına dizer.
func (e *Engine) sortBatchRules() {
	sortByPrecedence(e.batch, func(r BatchRule) integrityrule.ID { return r.ID() })
}

// sortByPrecedence, kural dilimini kanıt gücüne göre sıralar.
//
// Ekleme sıralaması kullanılır: kural sayısı en fazla ikidir, sort paketini
// çağırmak gereksiz. Sıra kararlıdır — aynı öncelikli iki kural olamaz.
func sortByPrecedence[T any](rules []T, idOf func(T) integrityrule.ID) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0 && idOf(rules[j]).StrongerThan(idOf(rules[j-1])); j-- {
			rules[j], rules[j-1] = rules[j-1], rules[j]
		}
	}
}

// Observe, akış fazında tek kaydı işler.
//
// Kurallar öncelik sırasında koşar; isabetler toplanır ve öncelik uygulanır.
// Tampon dolarsa yazılır — çağıran offset commit'inden önce Flush çağırmak
// zorundadır (bkz. internal/persist offset disiplini).
func (e *Engine) Observe(ctx context.Context, rec Record) error {
	if e.phase != integrityrule.ClassStream {
		return fmt.Errorf("kural motoru: Observe yalnızca akış fazında çağrılabilir (faz %q)", e.phase)
	}
	e.inspected++

	var hits []Hit
	for _, r := range e.stream {
		hits = append(hits, r.Observe(rec)...)
	}
	e.resolve(rec.EventID, hits)

	return e.maybeFlush(ctx)
}

// Evaluate, toplu fazda bir abonenin dizisini işler.
func (e *Engine) Evaluate(ctx context.Context, seq Sequence) error {
	if e.phase != integrityrule.ClassBatch {
		return fmt.Errorf("kural motoru: Evaluate yalnızca toplu fazda çağrılabilir (faz %q)", e.phase)
	}
	if err := seq.Validate(); err != nil {
		return fmt.Errorf("kural motoru: %w", err)
	}
	e.inspected += int64(seq.Len())

	// İsabetler olaya göre gruplanır: bir dizide birden çok olay bulgu
	// üretebilir ve öncelik **olay bazında** uygulanır.
	byEvent := make(map[uuid.UUID][]Hit)
	order := make([]uuid.UUID, 0, 4)
	for _, r := range e.batch {
		for _, h := range r.Evaluate(seq) {
			if _, seen := byEvent[h.EventID]; !seen {
				order = append(order, h.EventID)
			}
			byEvent[h.EventID] = append(byEvent[h.EventID], h)
		}
	}

	// Deterministik sıra: harita yineleme sırası rastgeledir ve bulgu yazma
	// sırasının koşudan koşuya değişmesi K10 karşılaştırmasını zorlaştırırdı.
	for _, eventID := range order {
		e.resolve(eventID, byEvent[eventID])
	}

	return e.maybeFlush(ctx)
}

// resolve, tek bir olayın isabetlerine öncelik uygular (ADR-28).
//
// Kararlar:
//   - Olay daha önce talep edilmişse (aynı faz ya da önceki faz) tüm yeni
//     isabetler bastırılmış yazılır — ilk talep kazanır (ADR-28/7).
//   - Talep edilmemişse en güçlü isabet kanonik olur, diğerleri onun adına
//     bastırılır.
func (e *Engine) resolve(eventID uuid.UUID, hits []Hit) {
	if len(hits) == 0 {
		return
	}

	valid := hits[:0:0]
	for _, h := range hits {
		normalised, err := e.normalise(h)
		if err != nil {
			e.invalid++
			e.log.Error("isabet geçerlilik denetiminden düştü, yazılmıyor",
				"kural", h.Rule, "event_id", h.EventID, "hata", err)
			continue
		}
		valid = append(valid, normalised)
	}
	if len(valid) == 0 {
		return
	}

	winner, claimed := e.claims.Of(eventID)
	if !claimed {
		winner = valid[0].Rule
		for _, h := range valid[1:] {
			if h.Rule.StrongerThan(winner) {
				winner = h.Rule
			}
		}
		e.claims.Claim(eventID, winner)
	} else {
		// Önceki fazın talebi duruyor. Daha güçlü bir isabet geldiyse bu bir
		// ölçüm notudur ve sessiz kalmaz (ADR-28/7, gerekçe 3).
		for _, h := range valid {
			if h.Rule.StrongerThan(winner) {
				e.demoted++
				e.log.Error("daha güçlü isabet önceki talep nedeniyle bastırıldı",
					"event_id", eventID, "gelen_kural", h.Rule, "talep_sahibi", winner,
					"not", "ADR-28/7: ilk talep kazanır; bulgu tablosu ekle-yalnızdır")
			}
		}
	}

	for _, h := range valid {
		f := Finding{Hit: h, DetectedIn: e.phase}
		if h.Rule == winner && !claimed {
			e.canonical++
		} else {
			suppressor := winner
			f.SuppressedBy = &suppressor
			e.suppressed++
		}
		e.buffer = append(e.buffer, f)
	}
}

// normalise, isabeti şema kısıtlarına uygun hâle getirir ve doğrular.
//
// `margin` kapılır (ADR-31/4) ve kanıt gövdesi denetlenir. Geçersiz isabet
// yazılmaz: veritabanı zaten reddederdi ve reddedilen parti diğer bulguları da
// düşürürdü (zehirli satır).
func (e *Engine) normalise(h Hit) (Hit, error) {
	if !h.Rule.Valid() {
		return h, fmt.Errorf("tanımsız kural kimliği (%d)", int(h.Rule))
	}
	if !h.Rule.EventAnchored() {
		return h, fmt.Errorf("%s olay-çıpalı değil, bulgu yazamaz (ADR-30)", h.Rule)
	}
	if h.EventID == uuid.Nil {
		return h, fmt.Errorf("%s: event_id boş", h.Rule)
	}
	if h.Time.IsZero() {
		return h, fmt.Errorf("%s: zaman damgası boş", h.Rule)
	}
	if len(h.Scenario) != 1 {
		return h, fmt.Errorf("%s: senaryo tek harf olmalı (%q)", h.Rule, h.Scenario)
	}
	if len(h.Evidence) == 0 {
		return h, fmt.Errorf("%s: kanıt gövdesi boş (adli açıklanabilirlik zorunlu)", h.Rule)
	}
	if v, ok := h.Evidence["v"]; !ok || v != EvidenceSchemaVersion {
		return h, fmt.Errorf("%s: kanıt sürümü eksik veya yanlış (%v)", h.Rule, v)
	}
	if msg, bad := h.Evidence["error"]; bad {
		return h, fmt.Errorf("%s: kanıt kurulamadı: %v", h.Rule, msg)
	}

	margin, _ := ClampMargin(h.Margin, e.cap)
	h.Margin = margin
	return h, nil
}

// maybeFlush, tampon eşiği aşıldıysa yazar.
func (e *Engine) maybeFlush(ctx context.Context) error {
	if len(e.buffer) < e.size {
		return nil
	}
	return e.Flush(ctx)
}

// Flush, tamponu yazıcıya boşaltır.
//
// Akış fazında offset commit'inden **önce** çağrılmak zorundadır: tampon dolu
// kalıp offset ilerleseydi, o andaki bir çökme yazılmamış bulguları geri
// getirilemez biçimde kaybederdi.
func (e *Engine) Flush(ctx context.Context) error {
	if len(e.buffer) == 0 {
		return nil
	}
	n, err := e.sink.WriteFindings(ctx, e.buffer)
	if err != nil {
		return fmt.Errorf("kural motoru: bulgular yazılamadı (%d satır): %w", len(e.buffer), err)
	}
	e.written += n
	e.buffer = e.buffer[:0]
	return nil
}

// Stats, motorun sayaçlarıdır.
type Stats struct {
	// Inspected, görülen kayıt sayısıdır (run_config.inspected_records).
	Inspected int64
	// Canonical, F.5 kanonik ölçümüne giren bulgu sayısıdır.
	Canonical int64
	// Suppressed, öncelik nedeniyle bastırılan bulgu sayısıdır.
	Suppressed int64
	// Invalid, geçerlilik denetiminden düşen isabet sayısıdır; sıfır olmalı.
	Invalid int64
	// Demoted, güçlü olduğu hâlde önceki talep nedeniyle bastırılan isabet
	// sayısıdır (ADR-28/7). Sıfırdan farklıysa raporda not edilir.
	Demoted int64
	// Written, veritabanına eklenen satır sayısıdır. Canonical+Suppressed'ten
	// küçükse yinelenen bulgu vardı (idempotanslık çalıştı).
	Written int64
	// Claims, defterdeki talep sayısıdır.
	Claims int
}

// Stats, sayaçların anlık kopyasını döndürür.
func (e *Engine) Stats() Stats {
	return Stats{
		Inspected:  e.inspected,
		Canonical:  e.canonical,
		Suppressed: e.suppressed,
		Invalid:    e.invalid,
		Demoted:    e.demoted,
		Written:    e.written,
		Claims:     e.claims.Len(),
	}
}

// Pending, henüz yazılmamış bulgu sayısıdır (test ve teşhis için).
func (e *Engine) Pending() int { return len(e.buffer) }
