// Package audit, servis erişim günlüğüdür (T-E08, ADR-15, ADR-33/1 KT9.6).
//
// # "Kullanıcı" değil, servis kimliği
//
// ADR-15: kimlik doğrulama kapsam dışı olduğundan denetlenen şey insan
// kullanıcı değil, **principal**dir — Kafka principal'ı ya da PostgreSQL
// rolü. Gateway `svc_gateway` rolüyle bağlanır ve her API isteğini o adla
// günlüğe geçer.
//
// # Yazma neden isteği bloke etmiyor
//
// Denetim izi bir kanıt kaydıdır, bir kilit değil. Yazma başarısız olursa
// istek yine servis edilir ama hata **Error** seviyesinde günlüğe geçer ve
// sayaç artar. Ters tasarım (yazamazsan servis etme) tek bir tablo
// sorununda tüm API'yi düşürürdü.
//
// Kayıp sessiz değildir: `Dropped()` sayacı sıfırdan farklıysa kapanış
// raporunda görünür.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Principal, gateway'in denetim kimliğidir (audit_log.principal).
//
// BÖLÜM D bu sütunu 'analysis-engine' / 'validation' / 'integrity' / 'gateway'
// olarak belgeliyor; değer o listeden gelir.
const Principal = "gateway"

// Entry, tek bir denetim satırıdır.
type Entry struct {
	// RunID, isteğin hedeflediği koşudur. Koşu belirtilmeyen uçlarda (örn.
	// /runs) boştur.
	RunID uuid.UUID
	// Action, uç adıdır ("api_cells", "api_estimates", ...).
	Action string
	// Resource, erişilen kaynaktır (tablo ya da uç yolu).
	Resource string
	// Detail, isteğin süzgeçleri ve sonucudur.
	Detail map[string]any
}

// Logger, denetim satırlarını yazar.
type Logger struct {
	db      *pgxpool.Pool
	log     *slog.Logger
	dropped atomic.Int64
	written atomic.Int64
}

// New, denetim yazıcısını kurar.
func New(db *pgxpool.Pool, log *slog.Logger) (*Logger, error) {
	if db == nil {
		return nil, fmt.Errorf("denetim günlüğü: veritabanı bağlantısı zorunlu")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Logger{db: db, log: log}, nil
}

// insertSQL, denetim satırını ekler.
//
// `run_id` NULL olabilir: koşu belirtmeyen uçlar da günlüğe geçer.
const insertSQL = `
INSERT INTO audit_log (run_id, principal, action, resource, detail)
VALUES ($1, $2, $3, $4, $5)`

// writeTimeout, denetim yazımının üst sınırıdır.
//
// İstek yolunda olduğu için kısa tutulur: yavaş bir denetim yazımı kullanıcıyı
// bekletmemelidir.
const writeTimeout = 3 * time.Second

// Record, bir denetim satırı yazar.
//
// Hata döndürmez (bkz. paket açıklaması): başarısızlık günlüğe geçer ve
// sayaca eklenir.
func (l *Logger) Record(ctx context.Context, e Entry) {
	if e.Action == "" || e.Resource == "" {
		l.log.Error("denetim satırı eksik alanla atlandı",
			"action", e.Action, "resource", e.Resource)
		l.dropped.Add(1)
		return
	}

	detail := []byte("{}")
	if len(e.Detail) > 0 {
		encoded, err := json.Marshal(e.Detail)
		if err != nil {
			l.log.Error("denetim ayrıntısı serileştirilemedi", "action", e.Action, "hata", err)
			l.dropped.Add(1)
			return
		}
		detail = encoded
	}

	var runID any
	if e.RunID != uuid.Nil {
		runID = e.RunID.String()
	}

	// İsteğin bağlamı iptal edilmiş olabilir (istemci bağlantıyı kesti);
	// denetim izi yine de yazılmalıdır — kaydın amacı isteğin GERÇEKLEŞTİĞİNİ
	// belgelemektir, tamamlandığını değil.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	if _, err := l.db.Exec(writeCtx, insertSQL, runID, Principal, e.Action, e.Resource, detail); err != nil {
		l.log.Error("denetim satırı yazılamadı — istek yine de servis edildi",
			"action", e.Action, "resource", e.Resource, "hata", err)
		l.dropped.Add(1)
		return
	}
	l.written.Add(1)
}

// Written, yazılan denetim satırı sayısıdır.
func (l *Logger) Written() int64 { return l.written.Load() }

// Dropped, yazılamayan denetim satırı sayısıdır; sıfır olmalıdır.
func (l *Logger) Dropped() int64 { return l.dropped.Load() }
