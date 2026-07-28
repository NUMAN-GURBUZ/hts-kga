// T-E02-06 — Envanterin Redis'e toplu yüklenmesi.
//
// client.go'daki tekil işlemler (SetCell, AddSiteLocation) korunur; bu dosya
// koşu başlangıcındaki toplu yükleme için boru hattı (pipeline) ekler:
// ~330 hücre + ~110 site için tek gidiş-dönüş.
//
// Katman kuralı: bu paket alan (domain) paketlerine bağımlı değildir; envanter
// tipleri CellParams/SiteLocation'a çağıran katmanda eşlenir.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// CellParams, bir sektörün Redis'te tutulan parametreleridir.
//
// Analiz motoru (S2) serving hücre parametrelerini buradan okur; PostgreSQL'e
// gitmeden sıcak yoldaki tüm alanlar bulunmalıdır.
type CellParams struct {
	CellID     uuid.UUID `json:"cell_id"`
	SiteID     uuid.UUID `json:"site_id"`
	Azimuth    float64   `json:"azimuth"`
	BeamWidth  float64   `json:"beam_width"`
	FreqMHz    int       `json:"freq_mhz"`
	EIRPdBm    float64   `json:"eirp_dbm"`
	AntHeightM float64   `json:"ant_height"`
	TiltDeg    float64   `json:"tilt_deg"`
	RMaxM      float64   `json:"r_max_m"`
	Morphology string    `json:"morphology"`
	ModelType  string    `json:"model_type"`
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
}

// SiteLocation, GEO indeksine eklenecek bir site konumudur.
type SiteLocation struct {
	SiteID uuid.UUID
	Lat    float64
	Lon    float64
}

// BulkLoadCells, hücre parametrelerini tek boru hattında yazar.
//
// TTL yoktur: hücreler koşu ömürlüdür, DeleteRunKeys ile temizlenir (client.go).
func (c *Client) BulkLoadCells(ctx context.Context, runID uuid.UUID, cells []CellParams) error {
	if runID == uuid.Nil {
		return fmt.Errorf("redis toplu yükleme: run_id boş (ADR-05)")
	}
	if len(cells) == 0 {
		return fmt.Errorf("redis toplu yükleme: yazılacak hücre yok")
	}

	pipe := c.rdb.Pipeline()
	for _, cell := range cells {
		data, err := json.Marshal(cell)
		if err != nil {
			return fmt.Errorf("hücre marshal hatası (%s): %w", cell.CellID, err)
		}
		pipe.Set(ctx, CellKey(runID, cell.CellID), data, 0)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis hücre boru hattı başarısız (%d hücre): %w", len(cells), err)
	}
	return nil
}

// BulkLoadSites, site konumlarını GEO indeksine tek çağrıda ekler (ADR-03
// komşu ön-filtresi).
func (c *Client) BulkLoadSites(ctx context.Context, runID uuid.UUID, sites []SiteLocation) error {
	if runID == uuid.Nil {
		return fmt.Errorf("redis toplu yükleme: run_id boş (ADR-05)")
	}
	if len(sites) == 0 {
		return fmt.Errorf("redis toplu yükleme: yazılacak site yok")
	}

	locations := make([]*redis.GeoLocation, len(sites))
	for i, s := range sites {
		locations[i] = &redis.GeoLocation{
			Name:      s.SiteID.String(),
			Longitude: s.Lon,
			Latitude:  s.Lat,
		}
	}

	if err := c.rdb.GeoAdd(ctx, SiteIndexKey(runID), locations...).Err(); err != nil {
		return fmt.Errorf("redis site GEO yüklemesi başarısız (%d site): %w", len(sites), err)
	}
	return nil
}

// ScanCells, koşuya ait tüm hücre parametrelerini okur (T-E03-02).
//
// Analiz motoru envanterin tamamını koşu başında belleğe alır: ~110 site ×
// 3 sektör ≈ 330 kayıt, birkaç yüz kilobayt. ADR-03'ün komşu ön-filtresi için
// mekânsal bir Redis indeksi kurmaya gerek yoktur — bu boyutta doğrusal
// tarama, ağ gidiş-dönüşünden ucuzdur.
//
// Sonuç cell_id'ye göre sıralı döner. SCAN'in dönüş sırası garantili
// olmadığından bu sıralama K10 için zorunludur: envanterin bellek düzeni
// koşudan koşuya değişirse kayan nokta toplama sırası da değişebilir.
func (c *Client) ScanCells(ctx context.Context, runID uuid.UUID) ([]CellParams, error) {
	if runID == uuid.Nil {
		return nil, fmt.Errorf("redis hücre taraması: run_id boş (ADR-05)")
	}
	pattern := fmt.Sprintf("hts:%s:cell:*", runID)

	var keys []string
	var cursor uint64
	for {
		batch, next, err := c.rdb.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, fmt.Errorf("redis hücre taraması başarısız: %w", err)
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("redis hücre taraması: koşu %s için hücre yok", runID)
	}

	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis hücre okuma başarısız (%d anahtar): %w", len(keys), err)
	}

	cells := make([]CellParams, 0, len(values))
	for i, v := range values {
		raw, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("redis hücre okuma: %s beklenmeyen tipte (%T)", keys[i], v)
		}
		var cell CellParams
		if err := json.Unmarshal([]byte(raw), &cell); err != nil {
			return nil, fmt.Errorf("hücre unmarshal hatası (%s): %w", keys[i], err)
		}
		cells = append(cells, cell)
	}

	sort.Slice(cells, func(i, j int) bool {
		return cells[i].CellID.String() < cells[j].CellID.String()
	})
	return cells, nil
}

// CountCells, koşuya ait Redis'teki hücre anahtarı sayısını döndürür (doğrulama).
func (c *Client) CountCells(ctx context.Context, runID uuid.UUID) (int64, error) {
	pattern := fmt.Sprintf("hts:%s:cell:*", runID)

	var cursor uint64
	var count int64
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return 0, fmt.Errorf("redis hücre sayımı başarısız: %w", err)
		}
		count += int64(len(keys))
		cursor = next
		if cursor == 0 {
			return count, nil
		}
	}
}

// CountSites, GEO indeksindeki site sayısını döndürür (doğrulama).
func (c *Client) CountSites(ctx context.Context, runID uuid.UUID) (int64, error) {
	n, err := c.rdb.ZCard(ctx, SiteIndexKey(runID)).Result()
	if err != nil {
		return 0, fmt.Errorf("redis site sayımı başarısız: %w", err)
	}
	return n, nil
}
