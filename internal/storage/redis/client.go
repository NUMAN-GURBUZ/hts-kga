// Package redis implements the Redis key schema and client wrapper for HTS-KGA.
//
// T-E01-09 — Redis key şeması: hts:{run_id}:cell:{cell_id}
// ADR-05: run_id tüm anahtarlarda zorunlu (çoklu koşu desteği)
// TTL yok: hücreler koşu ömürlüdür; koşu bitince run_id ile toplu silinir.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ─── Key şeması (E-05, E-06) ─────────────────────────────────────────────────

// CellKey, hücre envanteri için Redis anahtarı üretir.
//
//	Format: hts:{run_id}:cell:{cell_id}
//	TTL   : yok — koşu ömürlü, koşu bitince DeleteRunKeys ile temizlenir
func CellKey(runID, cellID uuid.UUID) string {
	return fmt.Sprintf("hts:%s:cell:%s", runID, cellID)
}

// SiteIndexKey, bir run'ın tüm site konumlarının indeks anahtarı.
//
//	Format: hts:{run_id}:sites
//	Değer : Redis GEO set — komşu seçimi için mekânsal ön-filtre (ADR-03)
func SiteIndexKey(runID uuid.UUID) string {
	return fmt.Sprintf("hts:%s:sites", runID)
}

// RunMetaKey, koşun aktif olup olmadığını tutan anahtar.
//
//	Format: hts:{run_id}:meta
func RunMetaKey(runID uuid.UUID) string {
	return fmt.Sprintf("hts:%s:meta", runID)
}

// ─── Client ───────────────────────────────────────────────────────────────────

// Client, Redis işlemlerini saran ince sarmalayıcıdır.
type Client struct {
	rdb *redis.Client
}

// NewClient, Redis istemcisini başlatır ve ping ile bağlantıyı doğrular.
func NewClient(ctx context.Context, addr string) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping başarısız (%s): %w", addr, err)
	}

	return &Client{rdb: rdb}, nil
}

// Close, Redis bağlantısını kapatır.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// ─── Hücre envanteri işlemleri ────────────────────────────────────────────────

// SetCell, hücre parametrelerini Redis'e yazar (TTL yok — koşu ömürlü).
// v, JSON marshallanabilir herhangi bir yapı olabilir (örn. CellParams).
func (c *Client) SetCell(ctx context.Context, runID, cellID uuid.UUID, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("hücre marshal hatası: %w", err)
	}
	return c.rdb.Set(ctx, CellKey(runID, cellID), data, 0).Err() // TTL=0 → süresiz
}

// GetCell, hücre parametrelerini Redis'ten okur ve v'ye unmarshal eder.
func (c *Client) GetCell(ctx context.Context, runID, cellID uuid.UUID, v any) error {
	data, err := c.rdb.Get(ctx, CellKey(runID, cellID)).Bytes()
	if err != nil {
		return fmt.Errorf("hücre oku (%s): %w", CellKey(runID, cellID), err)
	}
	return json.Unmarshal(data, v)
}

// AddSiteLocation, site konumunu GEO set'e ekler (komşu mekânsal ön-filtresi).
func (c *Client) AddSiteLocation(ctx context.Context, runID, siteID uuid.UUID, lon, lat float64) error {
	return c.rdb.GeoAdd(ctx, SiteIndexKey(runID), &redis.GeoLocation{
		Name:      siteID.String(),
		Longitude: lon,
		Latitude:  lat,
	}).Err()
}

// NearbySites, verilen konumdan maxDistM metre içindeki siteleri döndürür.
// ADR-03 komşu kümesi seçimi için kaba mekânsal ön-filtre.
func (c *Client) NearbySites(ctx context.Context, runID uuid.UUID, lon, lat, maxDistM float64) ([]string, error) {
	results, err := c.rdb.GeoRadius(ctx, SiteIndexKey(runID), lon, lat, &redis.GeoRadiusQuery{
		Radius:    maxDistM,
		Unit:      "m",
		Count:     50, // üst sınır; tipik komşu sayısı 3–8 (ADR-03)
		Sort:      "ASC",
		WithDist:  false,
		WithCoord: false,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("nearbySites: %w", err)
	}
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Name
	}
	return names, nil
}

// DeleteRunKeys, bir koşa ait tüm Redis anahtarlarını siler (koşu ömürlü temizlik).
// Pattern: hts:{run_id}:*
func (c *Client) DeleteRunKeys(ctx context.Context, runID uuid.UUID) (int64, error) {
	pattern := fmt.Sprintf("hts:%s:*", runID)
	var cursor uint64
	var deleted int64
	for {
		keys, nextCursor, err := c.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return deleted, fmt.Errorf("redis scan hatası: %w", err)
		}
		if len(keys) > 0 {
			n, err := c.rdb.Del(ctx, keys...).Result()
			if err != nil {
				return deleted, fmt.Errorf("redis del hatası: %w", err)
			}
			deleted += n
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return deleted, nil
}
