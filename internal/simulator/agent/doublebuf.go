// T-E02-09 — Çift tamponlu (double buffer) tick güncellemesi.
//
// Plan C.1: "1000 ajan, 1 goroutine/ajan, double-buffer".
//
// # Neden çift tampon
//
// Tek tampon kullanılsaydı, bir goroutine kendi ajanını güncellerken başka
// bir goroutine aynı diziden okuyabilirdi — veri yarışı ve tick içi sıraya
// bağlı sonuçlar doğardı. Çift tamponda okuma ve yazma **farklı** dizilerde
// olur:
//
//	tick N:  tüm işçiler  OKU cur[i]  →  YAZ next[i]
//	         ─────────── bariyer (WaitGroup) ───────────
//	         swap(cur, next)
//
// Her işçi yalnızca kendi indeks aralığına yazdığından kilit gerekmez ve
// sonuç goroutine zamanlamasından bağımsızdır (K10).
//
// # Neden kalıcı işçi havuzu
//
// Tick başına goroutine doğurmak ölçülebilir bir maliyet getiriyordu
// (12 goroutine × 8.640 tick ≈ 104.000 doğuş, tick başına 25 ayırma).
// İşçiler kurulumda bir kez başlatılır ve koşu boyunca yaşar; tick başına
// ayırma sıfıra iner. Havuz Close ile kapatılır.
package agent

import (
	"fmt"
	"runtime"
	"sync"
)

// workRange, bir işçinin tek tick'te işleyeceği ajan indeks aralığıdır.
type workRange struct {
	lo, hi int
}

// DoubleBuffer, ajan durumlarının çift tamponlu deposudur.
//
// Eşzamanlı kullanım sözleşmesi: Step çağrısı süresince her işçi yalnızca
// kendi indeks aralığına yazar; okuma tamponu salt okunurdur.
//
// Kullanım sonunda Close çağrılmalıdır (işçi goroutine'leri sızmasın).
type DoubleBuffer struct {
	cur  []State
	next []State

	workers int
	jobs    chan workRange
	wg      sync.WaitGroup

	// update, geçerli tick'in güncelleme işlevidir. Step tarafından kanal
	// gönderiminden **önce** yazılır; kanal alımı happens-before ilişkisi
	// kurduğundan işçiler güncel değeri görür.
	update func(s State, index int) State

	closeOnce sync.Once
}

// NewDoubleBuffer, verilen başlangıç durumlarından çift tampon kurar ve
// işçi havuzunu başlatır.
//
// workers ≤ 0 verilirse mevcut çekirdek sayısı kullanılır.
// Kullanım bittiğinde Close çağrılmalıdır.
func NewDoubleBuffer(initial []State, workers int) (*DoubleBuffer, error) {
	if len(initial) == 0 {
		return nil, fmt.Errorf("çift tampon: ajan listesi boş")
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > len(initial) {
		workers = len(initial)
	}

	cur := make([]State, len(initial))
	copy(cur, initial)

	b := &DoubleBuffer{
		cur:     cur,
		next:    make([]State, len(initial)),
		workers: workers,
		jobs:    make(chan workRange, workers),
	}
	b.startWorkers()
	return b, nil
}

// startWorkers, kalıcı işçi goroutine'lerini başlatır.
func (b *DoubleBuffer) startWorkers() {
	for w := 0; w < b.workers; w++ {
		go func() {
			for r := range b.jobs {
				for i := r.lo; i < r.hi; i++ {
					// OKU: cur (paylaşılan, salt okunur)
					// YAZ: next (yalnızca bu işçinin indeks aralığı)
					b.next[i] = b.update(b.cur[i], i)
				}
				b.wg.Done()
			}
		}()
	}
}

// Close, işçi havuzunu kapatır. Birden çok kez çağrılabilir.
// Close sonrası Step çağrılmamalıdır.
func (b *DoubleBuffer) Close() {
	b.closeOnce.Do(func() { close(b.jobs) })
}

// Len, ajan sayısını döndürür.
func (b *DoubleBuffer) Len() int { return len(b.cur) }

// Workers, tick güncellemesinde kullanılan işçi sayısını döndürür.
func (b *DoubleBuffer) Workers() int { return b.workers }

// Current, okuma tamponunu döndürür.
//
// Dönen dilim **salt okunur** kabul edilmelidir: üzerinde değişiklik yapmak
// çift tampon sözleşmesini bozar.
func (b *DoubleBuffer) Current() []State { return b.cur }

// Step, tüm ajanları paralel olarak bir sonraki tick'e taşır ve tamponları
// değiştirir.
//
// update, ajan durumunu ve indeksini alır, güncellenmiş durumu döndürür.
// Girdi durumunu değiştirmemeli; yalnızca kendi ajanına dokunmalıdır.
//
// Bariyer sonrası tamponlar takas edilir; Current artık yeni durumları verir.
// Tick başına yığın ayırma yapılmaz.
func (b *DoubleBuffer) Step(update func(s State, index int) State) {
	if update == nil {
		return
	}

	b.update = update
	chunk := (len(b.cur) + b.workers - 1) / b.workers

	b.wg.Add(b.workers)
	for w := 0; w < b.workers; w++ {
		lo := w * chunk
		hi := lo + chunk
		if hi > len(b.cur) {
			hi = len(b.cur)
		}
		if lo > len(b.cur) {
			lo = len(b.cur)
		}
		b.jobs <- workRange{lo: lo, hi: hi}
	}
	b.wg.Wait()

	b.cur, b.next = b.next, b.cur
}

// StepSerial, tüm ajanları tek goroutine'de günceller.
//
// Paralel Step ile **birebir aynı** sonucu vermelidir; determinizmin
// eşzamanlılıktan bağımsız olduğunu doğrulamanın referans yoludur (K10).
func (b *DoubleBuffer) StepSerial(update func(s State, index int) State) {
	if update == nil {
		return
	}
	for i := range b.cur {
		b.next[i] = update(b.cur[i], i)
	}
	b.cur, b.next = b.next, b.cur
}
