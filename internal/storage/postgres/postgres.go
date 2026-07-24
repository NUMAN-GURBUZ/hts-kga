// Package postgres, PostgreSQL/PostGIS erişim katmanıdır.
//
// T-E02-06 — Envanterin kalıcı deposu. Katman kuralı: bu paket alan (domain)
// paketlerine bağımlı değildir; girdi/çıktı olarak kendi satır tiplerini
// (CellRow, RunRow) kullanır. Eşleme, çağıran katmanda yapılır.
package postgres

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool, bağlantı havuzunu saran ince sarmalayıcıdır.
type Pool struct {
	pool *pgxpool.Pool
}

// NewPool, DSN'den bağlantı havuzu açar ve ping ile doğrular.
//
// DSN .env'den gelir (POSTGRES_* değişkenleri); bu paket ortam değişkeni okumaz.
func NewPool(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres DSN çözümlenemedi: %w", err)
	}
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres havuzu açılamadı: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping başarısız: %w", err)
	}

	return &Pool{pool: pool}, nil
}

// Close, havuzu kapatır.
func (p *Pool) Close() { p.pool.Close() }

// Ping, bağlantıyı denetler. health.Register ile /ready ucuna bağlanır.
func (p *Pool) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

// GitSHA, çalışan ikilinin sürüm damgasını döndürür (ADR-05, run_config.git_sha).
//
// Derleme VCS bilgisi yoksa "unknown" döner — run_config.git_sha NOT NULL
// olduğu için boş dize yerine açık bir yer tutucu kullanılır.
func GitSHA() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > 40 { // git_sha VARCHAR(40)
				return s.Value[:40]
			}
			return s.Value
		}
	}
	return "unknown"
}
