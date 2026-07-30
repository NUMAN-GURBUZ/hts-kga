// API hata sınıfları (ADR-13, ADR-33/1 KT9.1–KT9.2).
//
// Hatalar tiplenir ki hem gRPC hem REST katmanı **aynı** sınıflandırmayı
// yapabilsin: biri 400, diğeri InvalidArgument döndürsün ama karar tek yerde
// verilsin.

package query

import (
	"errors"
	"fmt"
)

// Sentinel hatalar — çağıran `errors.Is` ile sınıflandırır.
var (
	// ErrRunIDRequired, ADR-13'ün zorunlu süzgeç kuralıdır.
	ErrRunIDRequired = errors.New("run_id zorunlu (ADR-13: görselleştirme daima filtrelidir)")

	// ErrSubscriberRequired, tahmin ucunda abone süzgecinin zorunluluğudur.
	ErrSubscriberRequired = errors.New(
		"subscriber zorunlu (ADR-13: tek abone, tek zaman aralığı)")

	// ErrNotFound, istenen koşunun bulunamamasıdır.
	ErrNotFound = errors.New("koşu bulunamadı")
)

// TooManyGeometriesError, ADR-13'ün 500 geometri sınırının aşılmasıdır.
//
// # Neden kırpma değil hata
//
// Sessizce kırpmak, kullanıcıya eksik bir haritayı tam sanmasına yol açardı.
// ADR-13 açıkça "aşılırsa 400 + açıklama" diyor; açıklama, kaç geometrinin
// istendiğini ve nasıl daraltılacağını söyler.
type TooManyGeometriesError struct {
	Requested int
	Limit     int
	Hint      string
}

func (e *TooManyGeometriesError) Error() string {
	return fmt.Sprintf(
		"istek %d geometri döndürürdü, üst sınır %d (ADR-13). %s",
		e.Requested, e.Limit, e.Hint)
}

// IsBadRequest, hatanın istemci hatası olup olmadığını bildirir.
//
// REST katmanı 400, gRPC katmanı InvalidArgument döndürür; sınıflandırma
// burada tek yerde yapılır.
func IsBadRequest(err error) bool {
	var tooMany *TooManyGeometriesError
	switch {
	case errors.Is(err, ErrRunIDRequired),
		errors.Is(err, ErrSubscriberRequired),
		errors.As(err, &tooMany):
		return true
	}
	// Süzgeç doğrulama hataları da istemci hatasıdır; sentinel değiller
	// çünkü mesajları alan adını taşıyor (örn. "confidence 0.50, 0.90 ...").
	return false
}
