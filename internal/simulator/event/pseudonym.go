// T-E02-15 — Abone ve cihaz takma adları (E-08, ADR-15).
//
// Kayıtlarda gerçek kimlik bulunmaz: hem `hts_records` hem raporlar yalnızca
// HMAC-SHA256 ile üretilmiş takma adlar taşır.
//
// # İki aşama, tek nedenle
//
// Simülatörde gerçek bir MSISDN yoktur; ajanın sıra numarası vardır. Yine de
// önce **deterministik sahte bir MSISDN** üretilir (905XXXXXXXXX), sonra o
// HMAC'lenir. Doğrudan `agent_id` HMAC'lemek de çalışırdı ama:
//
//   - S6 bütünlük kuralları ve raporlar gerçek bir numara biçimi üzerinde
//     çalışmış olur; şema ve sorgular üretim verisiyle aynı şekli görür,
//   - takma adın girdisi, gerçek bir sistemde ne olacaksa ona benzer kalır —
//     tasarımın savunulabilirliği artar.
//
// # Tuz
//
// HMAC anahtarı `.env`'deki `HMAC_SALT` değerinden gelir; config dosyasında
// **değildir** (plan BÖLÜM L). Aynı tuzla aynı ajan her koşuda aynı takma adı
// alır — K10 için gereklidir. Tuz değişirse tüm takma adlar değişir, bu
// beklenen davranıştır.
package event

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// SaltEnvVar, HMAC tuzunun okunacağı ortam değişkenidir (plan BÖLÜM L).
const SaltEnvVar = "HMAC_SALT"

// minSaltLen, kabul edilen en kısa tuzdur.
//
// 32 bayt, HMAC-SHA256'nın blok boyutunun altındaki anahtarların sıfırla
// doldurulduğu aralığın dışında kalır; kısa bir tuz takma adı tahmin
// edilebilir kılardı.
const minSaltLen = 32

// msisdnCountryPrefix, üretilen sahte numaraların ülke ve operatör önekidir.
//
// 90 (TR) + 5 (mobil) + 3 haneli operatör kodu yerine sabit "50" kullanılır:
// gerçek bir operatöre işaret etmemesi kasıtlıdır.
const msisdnCountryPrefix = "9050"

// Pseudonymizer, ajan kimliklerinden takma ad üretir.
//
// Değişmezdir; eşzamanlı kullanımda güvenlidir.
type Pseudonymizer struct {
	salt []byte
}

// NewPseudonymizer, verilen tuzla bir üretici kurar.
func NewPseudonymizer(salt []byte) (*Pseudonymizer, error) {
	if len(salt) < minSaltLen {
		return nil, fmt.Errorf("takma ad: tuz en az %d bayt olmalı (%d bayt verildi) — "+
			"%s ortam değişkenini ayarlayın", minSaltLen, len(salt), SaltEnvVar)
	}
	return &Pseudonymizer{salt: append([]byte(nil), salt...)}, nil
}

// NewPseudonymizerFromEnv, tuzu ortam değişkeninden okuyarak üretici kurar.
func NewPseudonymizerFromEnv() (*Pseudonymizer, error) {
	salt := os.Getenv(SaltEnvVar)
	if salt == "" {
		return nil, fmt.Errorf("takma ad: %s ortam değişkeni boş — .env dosyasını kontrol edin",
			SaltEnvVar)
	}
	return NewPseudonymizer([]byte(salt))
}

// MSISDN, ajan kimliğinden deterministik sahte telefon numarası üretir.
//
// Biçim: 9050 + 8 hane (ajan kimliğinden). 1000 ajanlık koşuda çakışma yoktur;
// sıra numarası doğrudan kullanıldığı için tersine çevrilebilir — gizlilik
// takma adın kendisinden değil, HMAC'ten gelir.
func MSISDN(agentID int) string {
	return fmt.Sprintf("%s%08d", msisdnCountryPrefix, agentID)
}

// IMEI, ajan kimliğinden deterministik sahte cihaz kimliği üretir.
//
// 15 hanelidir; enjeksiyon kural 5 (sahtecilik) cihaz değişimini bu alan
// üzerinden modeller, bu yüzden NULL bırakılmaz.
func IMEI(agentID int) string {
	return fmt.Sprintf("35%013d", agentID)
}

// PseudoMSISDN, abone takma adını döndürür (hts_records.pseudo_msisdn).
//
// Kafka'da `hts.records` anahtarı da budur: aynı abonenin tüm kayıtları aynı
// partition'a düşer, böylece S4'ün hız/yörünge kuralları sıra görebilir.
func (p *Pseudonymizer) PseudoMSISDN(agentID int) string {
	return p.digest(MSISDN(agentID))
}

// PseudoIMEI, cihaz takma adını döndürür (hts_records.pseudo_imei).
func (p *Pseudonymizer) PseudoIMEI(agentID int) string {
	return p.digest(IMEI(agentID))
}

// PseudoIMEIFor, verilen cihaz kimliğinin takma adını döndürür.
//
// Enjeksiyon kural 5, ajanın kendi cihazı yerine başka bir cihaz kimliği
// yazar; bu yüzden ham değeri alan bir giriş noktası gerekir.
func (p *Pseudonymizer) PseudoIMEIFor(imei string) string {
	return p.digest(imei)
}

// digest, HMAC-SHA256 alıp onaltılık gösterime çevirir.
//
// Çıktı 64 karakterdir ve şemadaki VARCHAR(64) sınırına tam oturur.
func (p *Pseudonymizer) digest(value string) string {
	mac := hmac.New(sha256.New, p.salt)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
