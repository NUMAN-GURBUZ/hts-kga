// T-E04-07 — Tam N olaylık örneklem (replay yolu).
//
// Akışta (streaming) seyreltme orandan yapılır: gerçek olay sayısı önceden
// bilinmez, hedef ±%5 tutturulur. Kalibrasyon döngüsü ise (ADR-02) kayıtları
// veritabanından okur ve orada **tam sayı bilinir**; bu yüzden orada kesin N
// seçilebilir.
//
// # Neden kesin N önemli
//
// Bisection'ın 12 iterasyonu aynı olay kümesinde koşmalıdır. Küme
// iterasyondan iterasyona değişseydi, `coverage(λ)` farkı λ'dan mı örneklem
// gürültüsünden mi geldiği ayırt edilemezdi ve λ* ölçülen şeye değil örneklem
// dalgalanmasına yakınsardı.

package sampling

import (
	"sort"

	"github.com/google/uuid"
)

// ExactSample, verilen olaylardan tam `target` tanesini deterministik seçer.
//
// Seçim, olay kimliğinden ve tohumdan türetilen bir karma değerine göre
// sıralanıp ilk `target` tanesinin alınmasıdır. Bu:
//
//   - **girdi sırasından bağımsızdır** — veritabanı sırası değişse bile aynı
//     küme çıkar,
//   - **tekrarlanabilirdir** — aynı tohum, aynı küme (K10),
//   - **yansızdır** — karma düzgün dağıldığı için örneklem 'C' kümesinin
//     rastgele bir alt kümesidir; ilk gelenler ya da belirli bir zaman
//     aralığı değil.
//
// target ≥ len(ids) ise tümü döner. Dönen dilim `(karma, uuid)` sırasındadır;
// çağıran isterse kendi sırasına yeniden dizer.
func ExactSample(ids []uuid.UUID, target int, seed int64) []uuid.UUID {
	if target <= 0 || len(ids) == 0 {
		return nil
	}

	type ranked struct {
		id   uuid.UUID
		rank float64
	}

	p := Policy{seed: seed}
	items := make([]ranked, len(ids))
	for i, id := range ids {
		items[i] = ranked{id: id, rank: p.thin(id)}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		// Karma çakışmasında kimlik sırası: seçim tamamen tanımlı kalmalı.
		return items[i].id.String() < items[j].id.String()
	})

	if target > len(items) {
		target = len(items)
	}

	out := make([]uuid.UUID, target)
	for i := 0; i < target; i++ {
		out[i] = items[i].id
	}
	return out
}

// FilterPartition, olayları verilen bölüme göre süzer (C ya da V).
//
// Bölüm `pkg/split` ile yalnızca olay kimliğinden türetilir; `ground_truth`
// tablosuna gidilmez. Doğrulama servisi o tabloyu görebilse de ayrımı
// buradan hesaplar: tanım tek yerde kalsın, iki uygulama zamanla ayrışmasın.
func (p Policy) FilterPartition(ids []uuid.UUID, want byte) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if byte(p.splitter.Of(id)) == want {
			out = append(out, id)
		}
	}
	return out
}
