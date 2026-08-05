// SASL/SCRAM kimlik doğrulama seçenekleri (ADR-32).
//
// # Neden tek yerde
//
// Altı istemci çağrı noktası var: üretici (simülatör), iki kalıcılaştırıcı,
// analiz tüketicisi, bütünlük akış tüketicisi ve izolasyon testi. Kimlik
// seçenekleri her birinde ayrı kurulsaydı biri unutulur ve o servis
// yetkilendirmeyi atlar — kör testin 1. katmanı sessizce delinirdi.
//
// Bu yüzden `ClientOptions` tek kapıdır ve hepsi oradan geçer.

package kafka

import (
	"fmt"
	"os"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// Ortam değişkeni adları (ADR-32/4).
const (
	// EnvBrokers, broker adres listesidir (virgülle ayrılmış).
	EnvBrokers = "KAFKA_BROKERS"
	// EnvSASLUser, servisin SCRAM principal'ıdır (örn. "svc_analysis").
	EnvSASLUser = "KAFKA_SASL_USER"
	// EnvSASLPassword, o principal'ın parolasıdır.
	EnvSASLPassword = "KAFKA_SASL_PASSWORD"
)

// Credentials, bir servisin Kafka kimliğidir.
type Credentials struct {
	// User, SCRAM principal adıdır. Boşsa kimlik doğrulama yapılmaz.
	User string
	// Password, SCRAM parolasıdır.
	Password string
}

// Enabled, kimlik bilgilerinin dolu olup olmadığını bildirir.
func (c Credentials) Enabled() bool { return c.User != "" && c.Password != "" }

// CredentialsFromEnv, servis kimliğini ortamdan okur.
//
// # Boş kimlik neden hata değil
//
// ADR-32/4: mevcut entegrasyon testleri SASL kurulu olmayan bir broker'da da
// koşabilmelidir (CI'da yetkilendirme yapılandırılmamış olabilir). Boş kimlikte
// istemci SASL'sız kurulur ve bu **açıkça günlüğe geçmez** — çünkü kararı
// çağıran verir, kütüphane değil.
//
// Kör testin gerçek reddi, SASL'ın kurulu olduğu ortamda
// `scripts/verify-isolation.sh` tarafından doğrulanır (ADR-32/5).
func CredentialsFromEnv() Credentials {
	return Credentials{
		User:     os.Getenv(EnvSASLUser),
		Password: os.Getenv(EnvSASLPassword),
	}
}

// BrokersFromEnv, broker adreslerini ortamdan okur.
//
// Boşsa `localhost:9092` varsayılanı kullanılır (geliştirme ortamı).
func BrokersFromEnv() []string {
	return splitBrokers(os.Getenv(EnvBrokers))
}

// splitBrokers, virgülle ayrılmış listeyi böler ve boşları atar.
func splitBrokers(raw string) []string {
	if raw == "" {
		return []string{"localhost:9092"}
	}
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			if s := trimSpace(raw[start:i]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if len(out) == 0 {
		return []string{"localhost:9092"}
	}
	return out
}

// trimSpace, baştaki ve sondaki boşlukları atar (strings import etmeden —
// bu paket ADR-20 gereği bağımlılıksız kalır).
func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

// ClientOptions, bir servis için temel kgo seçeneklerini döndürür.
//
// Broker adresleri ve (varsa) SASL/SCRAM kimliği içerir. Çağıran bunun üzerine
// kendi seçeneklerini ekler (tüketici grubu, topic, offset disiplini).
//
// Boş broker listesi hatadır: sessiz bir varsayılan, yanlış broker'a bağlanıp
// "akış boş" diye yorumlanan bir koşuya yol açardı.
func ClientOptions(brokers []string, creds Credentials) ([]kgo.Opt, error) {
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafka istemcisi: en az bir broker adresi gerekli")
	}

	opts := []kgo.Opt{kgo.SeedBrokers(brokers...)}
	if !creds.Enabled() {
		return opts, nil
	}

	// SCRAM-SHA-256 (ADR-32/1). SHA-512 de broker'da açılabilirdi ama tek
	// mekanizma tutmak kurulum betiğini ve hata mesajlarını yalın tutuyor.
	mech := scram.Auth{User: creds.User, Pass: creds.Password}.AsSha256Mechanism()
	return append(opts, kgo.SASL(mech)), nil
}

// ClientOptionsFromEnv, seçenekleri tümüyle ortamdan kurar.
//
// Servislerin çağırdığı biçim budur; testler kimlikleri elle vermek için
// ClientOptions'ı kullanır.
func ClientOptionsFromEnv() ([]kgo.Opt, error) {
	return ClientOptions(BrokersFromEnv(), CredentialsFromEnv())
}
