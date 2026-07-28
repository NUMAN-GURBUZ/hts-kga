// Package event, tick başına ajan gözlemlerini platformun dayandığı iki kayıt
// akışına dönüştürür: HTS kayıtları (bir operatörün tutmuş olacağı kayıt) ve
// ground truth (gerçekte olan).
//
// Sprint 2 kapsamı:
//   - T-E02-14  Deterministik UUIDv5 olay kimliği (ADR-01)
//   - T-E02-13  Poisson olay üreteci (saatlik λ)
//   - T-E02-15  HTS kaydı + HMAC takma ad + mesafeden türetilen TA
//   - T-E02-16  Ground truth + kapsama bayrağı + olay bazlı 80/20 bölümleme
//   - T-E02-17  Enjeksiyon aşaması (ADR-09)
//   - T-E02-18  Kafka publisher (iki topic)
//
// Paketin koşu tohumu dışında kendine ait rastgeleliği yoktur: her değer ajan
// kimliği ve tick indeksinden türetilir. Aynı tohumla yapılan ikinci koşu
// kayıtları bit düzeyinde yeniden üretir (K10).
package event

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// T-E02-14 — Deterministik olay kimliği (ADR-01).
//
// hts_records, ground_truth ve estimates arasındaki referansiyel bütünlüğü
// tek başına olay kimliği taşır: üç tabloyu üç ayrı servis, ayrı zamanlarda,
// paylaşılan bir sayaç olmadan yazar. Rastgele bir kimlik bu bağı sonradan
// yeniden kurulamaz hâle getirirdi; bu yüzden kimlik, olayı üreten şeyin saf
// bir fonksiyonudur:
//
//	event_id = UUIDv5(namespace, run_id ‖ agent_id ‖ tick_index ‖ event_seq)
//
// Çıplak bir özet yerine UUIDv5 (SHA-1 tabanlı, RFC 4122 §4.3) kullanılır;
// böylece değer, şemanın UUID sütunları için geçerli bir UUID olur. UUIDv3
// tercih edilmez: takma adları da imzalayan bir sistemde MD5'in yeri yoktur.
//
// # Adın sabit genişlikli ikili olmasının nedeni
//
// Dört bileşen sabit genişliklerde kodlanır ve ayırıcı olmadan birleştirilir.
// Değişken genişlikli herhangi bir kodlama — örneğin ondalık metin — iki ayrı
// anahtarın aynı adı üretmesine izin verirdi: tick 0'daki ajan 1 ile tick
// 10'daki ajan 0, ikisi de "1" ardından "0" okunur. Sabit genişlik, kodlamayı
// yapısal olarak birebir yapar; kimliğin teklik garantisi buna dayanır.
//
// Kodlama dondurulmuştur. Bir genişliğin, bayt sırasının veya namespace'in
// değişmesi, platformun ürettiği tüm kimlikleri değiştirir ve önceki koşulara
// karşı K10 tekrarlanabilirliğini sessizce bozar; TestEventIDGoldenVector
// bunu bekler.

// namespaceLiteral, HTS-KGA olay kimlikleri için sabit UUID namespace'idir.
//
// Bir kez (rastgele v4 olarak) üretilmiş ve ADR-01 ekine kaydedilmiştir.
// Platformun sabitidir, yapılandırma değeri değildir: senaryoya, ortama veya
// koşuya göre değişmez.
const namespaceLiteral = "29b15779-62b7-4b8d-889e-417227237e61"

// namespace, namespaceLiteral'ın çözümlenmiş hâlidir.
//
// Dışa açılmaz, erişim Namespace üzerindendir: çalışma anında yeniden
// atanabilseydi, değişmiş bir namespace koşu boyunca kimliği bozardı.
var namespace = uuid.MustParse(namespaceLiteral)

// Namespace, olay kimlikleri için kullanılan UUIDv5 namespace'ini döndürür.
func Namespace() uuid.UUID { return namespace }

// Kodlanmış adın bileşen genişlikleri (bayt).
const (
	runIDLen   = 16 // UUID, ham baytlar
	agentIDLen = 4  // uint32 big endian, ground_truth.agent_id INTEGER ile eşleşir
	tickLen    = 8  // uint64 big endian, her koşu uzunluğu için fazlasıyla yeterli
	seqLen     = 4  // uint32 big endian, tek tick içindeki olaylar

	nameLen = runIDLen + agentIDLen + tickLen + seqLen

	// Bileşenlerin kodlanmış ad içindeki bayt konumları.
	agentIDOffset = runIDLen
	tickOffset    = agentIDOffset + agentIDLen
	seqOffset     = tickOffset + tickLen
)

// Key, tek bir olayın kaynağını koşu içinde benzersiz olarak belirler.
//
// Değer tipidir ve saat okuması taşımaz: zamanın tek karşılığı tick indeksidir
// — kimliği tekrarlanabilir kılan da budur.
type Key struct {
	// RunID, olayın ait olduğu koşudur (run_config.run_id, ADR-05).
	RunID uuid.UUID
	// AgentID, ajanın koşu içindeki 0 tabanlı sıra numarasıdır;
	// agent.State.ID ve ground_truth.agent_id ile eşleşir.
	AgentID int
	// Tick, olayın üretildiği 0 tabanlı simülasyon tick indeksidir.
	Tick int
	// Seq, aynı ajanın aynı tick içinde ürettiği olayları ayırır. Poisson
	// çekilişi birden fazla olay verebilir; ilki 0'dır.
	Seq int
}

// maxSeq, kodlamanın taşıyabileceği en büyük olay sıra numarasıdır.
const maxSeq = math.MaxUint32

// Validate, anahtarın bilgi kaybetmeden kodlanıp kodlanamayacağını denetler.
//
// Sınırlar keyfi değildir; kodlamanın ve veritabanı sütunlarının sınırlarıdır:
// int32'yi aşan bir ajan kimliği ground_truth.agent_id'ye sığmaz, negatif bir
// değer ise işaretsize dönüşümde sarmalanıp büyük bir pozitifle çakışır.
func (k Key) Validate() error {
	if k.RunID == uuid.Nil {
		return fmt.Errorf("olay anahtarı: run_id boş olamaz")
	}
	if k.AgentID < 0 || k.AgentID > math.MaxInt32 {
		return fmt.Errorf("olay anahtarı: ajan kimliği [0,%d] aralığında olmalı (%d)",
			math.MaxInt32, k.AgentID)
	}
	if k.Tick < 0 {
		return fmt.Errorf("olay anahtarı: tick indeksi negatif olamaz (%d)", k.Tick)
	}
	if k.Seq < 0 || int64(k.Seq) > maxSeq {
		return fmt.Errorf("olay anahtarı: sıra numarası [0,%d] aralığında olmalı (%d)",
			int64(maxSeq), k.Seq)
	}
	return nil
}

// encodeName, anahtarı UUID'ye özetlenecek sabit genişlikli ada serer.
//
// Dilim yerine dizi döndürür: sıcak yolda yığın ayırma yapılmaz.
func (k Key) encodeName() [nameLen]byte {
	var name [nameLen]byte
	copy(name[:runIDLen], k.RunID[:])
	binary.BigEndian.PutUint32(name[agentIDOffset:tickOffset], uint32(k.AgentID))
	binary.BigEndian.PutUint64(name[tickOffset:seqOffset], uint64(k.Tick))
	binary.BigEndian.PutUint32(name[seqOffset:], uint32(k.Seq))
	return name
}

// NewID, verilen anahtarın olay kimliğini türetir.
//
// Sonuç yalnızca anahtara ve dondurulmuş namespace'e bağlıdır: aynı anahtar,
// hangi makinede ve hangi koşuda olursa olsun aynı kimliği verir.
func NewID(k Key) (uuid.UUID, error) {
	if err := k.Validate(); err != nil {
		return uuid.Nil, err
	}
	name := k.encodeName()
	return uuid.NewSHA1(namespace, name[:]), nil
}
