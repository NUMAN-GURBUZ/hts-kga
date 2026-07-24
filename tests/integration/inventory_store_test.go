// T-E02-06 — Envanter yükleme entegrasyon testi (PostgreSQL + Redis).
//
// Bu test gerçek altyapı ister. Ortam değişkenleri tanımlı değilse atlanır,
// böylece `go test ./...` altyapısız makinelerde de yeşil kalır:
//
//	HTS_TEST_PG_DSN     postgres://user:pass@localhost:5432/hts?sslmode=disable
//	HTS_TEST_REDIS_ADDR localhost:6379
//
// Çalıştırma:  make test-integration
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

const (
	envPGDSN     = "HTS_TEST_PG_DSN"
	envRedisAddr = "HTS_TEST_REDIS_ADDR"
	configsDir   = "../../configs"
)

// requireInfra, altyapı bağlantılarını kurar; ortam tanımlı değilse testi atlar.
func requireInfra(t *testing.T) (*postgres.Pool, *redis.Client) {
	t.Helper()

	dsn := os.Getenv(envPGDSN)
	addr := os.Getenv(envRedisAddr)
	if dsn == "" || addr == "" {
		t.Skipf("altyapı testi atlandı: %s ve %s tanımlı değil", envPGDSN, envRedisAddr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("PostgreSQL bağlantısı: %v", err)
	}
	t.Cleanup(pool.Close)

	rdb, err := redis.NewClient(ctx, addr)
	if err != nil {
		t.Fatalf("Redis bağlantısı: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	return pool, rdb
}

// TestInventoryStore_EndToEnd, envanterin üretilip her iki depoya yazılmasını
// ve geri okunabilmesini uçtan uca doğrular.
func TestInventoryStore_EndToEnd(t *testing.T) {
	pool, rdb := requireInfra(t)
	ctx := context.Background()

	for _, file := range []string{"urban_ta.yaml", "rural_ta.yaml"} {
		t.Run(file, func(t *testing.T) {
			scn, err := config.Load(configsDir + "/" + file)
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
			if err != nil {
				t.Fatalf("geo.NewProjector: %v", err)
			}

			runID := uuid.New()
			t.Cleanup(func() {
				if err := pool.DeleteRun(ctx, runID); err != nil {
					t.Errorf("temizlik (postgres): %v", err)
				}
				if _, err := rdb.DeleteRunKeys(ctx, runID); err != nil {
					t.Errorf("temizlik (redis): %v", err)
				}
			})

			inv, err := inventory.Build(runID, scn, proj)
			if err != nil {
				t.Fatalf("inventory.Build: %v", err)
			}

			store := inventory.Store{DB: pool, Cache: rdb}
			if err := store.Persist(ctx, inv, scn, postgres.GitSHA()); err != nil {
				t.Fatalf("Persist: %v", err)
			}

			// PostgreSQL doğrulaması
			pgCount, err := pool.CountCells(ctx, runID)
			if err != nil {
				t.Fatalf("CountCells (postgres): %v", err)
			}
			if pgCount != int64(inv.CellCount()) {
				t.Errorf("PostgreSQL'de %d hücre, beklenen %d", pgCount, inv.CellCount())
			}

			// Redis doğrulaması
			redisCount, err := rdb.CountCells(ctx, runID)
			if err != nil {
				t.Fatalf("CountCells (redis): %v", err)
			}
			if redisCount != int64(inv.CellCount()) {
				t.Errorf("Redis'te %d hücre, beklenen %d", redisCount, inv.CellCount())
			}

			siteCount, err := rdb.CountSites(ctx, runID)
			if err != nil {
				t.Fatalf("CountSites (redis): %v", err)
			}
			if siteCount != int64(inv.SiteCount()) {
				t.Errorf("Redis GEO indeksinde %d site, beklenen %d", siteCount, inv.SiteCount())
			}

			// Hücre parametreleri kayıpsız okunabilmeli
			var got redis.CellParams
			want := inventory.CellParams(inv)[0]
			if err := rdb.GetCell(ctx, runID, want.CellID, &got); err != nil {
				t.Fatalf("GetCell: %v", err)
			}
			if got != want {
				t.Errorf("Redis'ten okunan hücre farklı:\n  %+v\n  %+v", got, want)
			}

			// GEO ön-filtresi çalışmalı (ADR-03): merkeze yakın site bulunmalı
			near, err := rdb.NearbySites(ctx, runID,
				scn.Area.OriginLon, scn.Area.OriginLat, inv.Layout.EffectiveISDM*2)
			if err != nil {
				t.Fatalf("NearbySites: %v", err)
			}
			if len(near) == 0 {
				t.Error("GEO ön-filtresi merkez yakınında hiç site bulamadı")
			}

			t.Logf("%s: %d site / %d hücre — PostgreSQL %d, Redis %d hücre + %d site",
				file, inv.SiteCount(), inv.CellCount(), pgCount, redisCount, siteCount)
		})
	}
}

// TestInventoryStore_ForeignKeyEnforced, cells → run_config FK'sının gerçekten
// uygulandığını doğrular: koşu kaydı olmadan hücre yazılamaz.
func TestInventoryStore_ForeignKeyEnforced(t *testing.T) {
	pool, _ := requireInfra(t)
	ctx := context.Background()

	orphan := postgres.CellRow{
		CellID: uuid.New(), RunID: uuid.New(), SiteID: uuid.New(),
		Azimuth: 0, BeamWidth: 65, FreqMHz: 2100, EIRPdBm: 58,
		AntHeightM: 25, TiltDeg: 6, RMaxM: 5000,
		Morphology: "urban", ModelType: "UMa",
		Lat: 38.6748, Lon: 39.2225,
	}

	if _, err := pool.InsertCells(ctx, []postgres.CellRow{orphan}); err == nil {
		_ = pool.DeleteRun(ctx, orphan.RunID)
		t.Fatal("koşu kaydı olmayan hücre yazıldı — FK uygulanmıyor")
	}
}

// TestInventoryStore_TransactionRollback, işlem bütünlüğünü doğrular:
// hücrelerden biri geçersizse run_config satırı da yazılmamalıdır.
func TestInventoryStore_TransactionRollback(t *testing.T) {
	pool, _ := requireInfra(t)
	ctx := context.Background()

	scn, err := config.Load(configsDir + "/urban_ta.yaml")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("geo.NewProjector: %v", err)
	}

	runID := uuid.New()
	t.Cleanup(func() { _ = pool.DeleteRun(ctx, runID) })

	inv, err := inventory.Build(runID, scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}

	run, err := inventory.RunRowFor(inv, scn, "test")
	if err != nil {
		t.Fatalf("RunRowFor: %v", err)
	}

	rows := inventory.CellRows(inv)
	rows[len(rows)/2].Azimuth = 400 // cells.azimuth CHECK ihlali

	if _, err := pool.InsertRunWithCells(ctx, run, rows); err == nil {
		t.Fatal("geçersiz azimut kabul edildi")
	}

	n, err := pool.CountCells(ctx, runID)
	if err != nil {
		t.Fatalf("CountCells: %v", err)
	}
	if n != 0 {
		t.Errorf("geri alma sonrası %d hücre kaldı, beklenen 0", n)
	}
}
