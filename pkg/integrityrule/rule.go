// Package integrityrule, bütünlük kurallarının kimlik ve sınıf sözleşmesidir.
//
// # Bu paketin var oluş nedeni
//
// Kural kimlikleri iki yerde kullanılır ve iki taraf birbirini import
// **edemez**:
//
//	simülatör (T-E02-17) : ground_truth.injected_rule sütununu yazar
//	bütünlük  (T-E05-*)  : integrity_findings.rule_id sütununu yazar
//
// Aralarındaki bağ F.5'in precision hesabıdır:
//
//	count(*) FILTER (WHERE g.injected_rule = f.rule_id) / count(*)
//
// Bu eşitlik bir **sözleşmedir**, tesadüf değil. Kimlikler iki yerde ayrı
// tanımlansaydı bir numaralandırma değişikliği derleme hatası vermez, sessizce
// precision'ı sıfırlardı: dedektör 2 derken üreteç 3 demiş olur ve her bulgu
// yanlış pozitif sayılır. Ölçüm "kural 2 çalışmıyor" der; oysa kural çalışıyor,
// numaralandırma kaymıştır.
//
// `pkg/ta`, `pkg/split` ve `pkg/htswire` ile aynı ikiz tuzağı, aynı çözüm.
//
// # Kör test delinmiyor
//
// S4'ün hangi kuralı **uyguladığını** bilmesi zorunludur; hangi kaydın enjekte
// edildiğini bilmesi yasaktır. Bu paket yalnızca birincisini verir: kimlik, ad
// ve sınıf. Enjeksiyon etiketi `ground_truth`'ta kalır ve S4 o tabloya erişemez
// (ADR-09 katman 2), o topic'e abone olmaz (katman 1), simülatör kodunu import
// edemez (katman 4).
//
// # Bağımsızlık kısıtı
//
// Paket yalnızca stdlib kullanır. Bir `internal/` bağımlılığı sızarsa iki
// taraftan biri onu import edemez hâle gelir ve tanım yeniden ikizlenir —
// paketin var oluş nedeni ortadan kalkar. Kısıt
// `tests/isolation/import_graph_test.go` tarafından denetlenir.
package integrityrule

import "fmt"

// ID, bütünlük kuralının kimliğidir.
//
// Değerler `ground_truth.injected_rule` ve `integrity_findings.rule_id`
// sütunlarıyla birebir aynıdır; ikisinde de CHECK kısıtı 1..5'tir.
type ID int

const (
	// Inventory (1), envanterde bulunmayan bir hücre kimliğidir.
	//
	// Enjeksiyon: kayda sahte cell_id yazılır.
	// Tespit: cell_id koşunun hücre envanterinde yok.
	Inventory ID = 1

	// Velocity (2), kinematik olarak imkânsız bir geçiştir.
	//
	// Enjeksiyon: kayıt envanterin en uzak hücresine taşınır.
	// Tespit: ardışık iki kayıt arasındaki ima edilen hız eşiği aşar.
	Velocity ID = 2

	// TimeOrder (3), geriye kaydırılmış bir zaman damgasıdır.
	//
	// Enjeksiyon: damga 2 saat geriye alınır.
	// Tespit: varış sırası ilerlerken olay zamanı geriye gider.
	TimeOrder ID = 3

	// Trajectory (4), yörüngedeki eksik halkadır.
	//
	// Enjeksiyon: olay silinir; yalnızca ground truth kalır.
	// Tespit: ADR-30 — olay çıpalı tespit **imkânsızdır**; koşu düzeyi
	// gösterge olarak ele alınır.
	Trajectory ID = 4

	// Activity (5), abonenin cihaz kimliğindeki tutarsızlıktır.
	//
	// Enjeksiyon: pseudo_imei başka bir ajanın cihazıyla değiştirilir.
	// Tespit: abonenin modal IMEI'sinden sapan kayıt.
	Activity ID = 5
)

// Class, kuralın hangi girdi şeklinden beslendiğini belirler (ADR-27).
//
// Sınıf bir yorum değildir: kural motoru kurulum sırasında sınıfı denetler ve
// bir toplu kuralı akış fazına almaz. Yanlış faz bir çalışma zamanı hatası
// bile olmaz — kurulum başarısız olur.
//
// Ayrım ölçümden doğdu, tercihten değil: kural 3'ün kanıtı varış sırasındadır
// ve toplu modda yok olur (precision %8); kural 5'in kanıtı kapalı
// popülasyondadır ve akışta ilk-kayıt tuzağı precision'ı %50'ye düşürür.
type Class string

const (
	// ClassStream, varış sırasında tek kayıt gören kurallardır.
	// Durum abone başına sabittir.
	ClassStream Class = "stream"

	// ClassBatch, bir abonenin olay-zamanı sıralı dizisini gören kurallardır.
	ClassBatch Class = "batch"

	// ClassAggregate, koşu düzeyinde gösterge üreten kurallardır.
	// integrity_findings'e olay bazlı satır YAZMAZ (ADR-30).
	ClassAggregate Class = "aggregate"
)

// Valid, sınıfın tanımlı olup olmadığını bildirir.
func (c Class) Valid() bool {
	switch c {
	case ClassStream, ClassBatch, ClassAggregate:
		return true
	default:
		return false
	}
}

// spec, bir kuralın değişmez tanımıdır.
type spec struct {
	name  string
	class Class
}

// specs, beş kuralın dondurulmuş tanımıdır.
//
// Adlar `integrity_findings.rule_name VARCHAR(50)` sütununa yazılır; uzunluk
// kısıtı testle denetlenir. Türkçe adlar seçildi çünkü sütun adli bir kayıttır
// ve raporun dili Türkçedir; kimlik (rule_id) makine tarafı, ad insan tarafıdır.
var specs = map[ID]spec{
	Inventory:  {name: "envanter tutarsızlığı", class: ClassStream},
	Velocity:   {name: "kinematik tutarsızlık", class: ClassBatch},
	TimeOrder:  {name: "zaman tutarsızlığı", class: ClassStream},
	Trajectory: {name: "yörünge boşluğu", class: ClassAggregate},
	Activity:   {name: "cihaz aktivitesi tutarsızlığı", class: ClassBatch},
}

// All, tanımlı kuralların **öncelik sırasına göre** listesidir (ADR-28).
//
// Sıra kanıt gücüne göredir, kimlik sırasına göre değil:
//
//	1 envanter  — küme üyeliği, eşik yok, atıf belirsizliği yok
//	5 aktivite  — kapalı popülasyonda çoğunluk, eşik yok
//	3 zaman     — varış sırası ↔ olay zamanı çelişkisi, eşik yok
//	2 hız       — türetilmiş oran, eşik VAR, atıf belirsizliği VAR
//	4 yörünge   — toplulaştırılmış; olay talebine katılmaz
//
// Eşiği ve atıf belirsizliği olmayan kanıt, olanın önündedir. Sıralamanın
// ölçülen precision'a göre seçilmesi aşırı uydurma olurdu; kanıt tipinden
// türetildi ve ADR-28'de dondurulmuştur.
var All = []ID{Inventory, Activity, TimeOrder, Velocity, Trajectory}

// ByID, tanımlı kuralların **kimlik sırasına göre** listesidir.
//
// All ile arasındaki fark önemlidir ve iki listenin ayrı durması bilinçlidir:
//
//	All  → öncelik sırası; kural motoru bastırma yarışında kullanır (ADR-28)
//	ByID → kimlik sırası;  enjektör ağırlık tablosunu bu sırada kurar (ADR-09)
//
// Enjektörün sırası determinizmin parçasıdır: kümülatif ağırlık tablosu bu
// sırada kurulur ve aynı tekdüze sayı aynı kuralı seçer. Sıra değişirse aynı
// tohum farklı enjeksiyonlar üretir ve K10 (tekrarlanabilirlik) ile Sprint 5'in
// dört koşusu geçersiz olur. İki liste tek listeye indirgenmemelidir.
var ByID = []ID{Inventory, Velocity, TimeOrder, Trajectory, Activity}

// precedence, kimliğin öncelik sırasındaki konumudur (küçük = güçlü).
var precedence = func() map[ID]int {
	m := make(map[ID]int, len(All))
	for i, id := range All {
		m[id] = i
	}
	return m
}()

// Valid, kimliğin tanımlı olup olmadığını bildirir.
func (id ID) Valid() bool {
	_, ok := specs[id]
	return ok
}

// Name, kuralın kanonik adıdır (integrity_findings.rule_name).
func (id ID) Name() string {
	if s, ok := specs[id]; ok {
		return s.name
	}
	return fmt.Sprintf("bilinmeyen kural(%d)", int(id))
}

// Class, kuralın çalışma sınıfıdır (ADR-27).
//
// Tanımsız kimlik için ClassAggregate döner: bilinmeyen bir kuralın akış veya
// toplu faza alınmaması, sessizce alınmasından iyidir.
func (id ID) Class() Class {
	if s, ok := specs[id]; ok {
		return s.class
	}
	return ClassAggregate
}

// EventAnchored, kuralın olay bazlı bulgu yazıp yazmadığını bildirir.
//
// ADR-30: kural 4 yazmaz. Silinen olayın event_id'si
// UUIDv5(run_id ‖ agent_id ‖ tick ‖ seq) ile üretilir ve S4 bu girdilerden
// ikisini (agent_id, tick) hiç görmez — çıpa üretilemez.
func (id ID) EventAnchored() bool { return id.Class() != ClassAggregate }

// String, kimliğin okunabilir gösterimidir.
func (id ID) String() string {
	if !id.Valid() {
		return fmt.Sprintf("ID(%d)", int(id))
	}
	return fmt.Sprintf("%d:%s", int(id), id.Name())
}

// StrongerThan, ADR-28 öncelik sırasında id'nin other'dan güçlü olup olmadığını
// bildirir.
//
// Tanımsız kimlik daima zayıftır: bilinmeyen bir kural tanımlı bir kuralı
// bastıramaz.
func (id ID) StrongerThan(other ID) bool {
	a, okA := precedence[id]
	b, okB := precedence[other]
	switch {
	case !okA:
		return false
	case !okB:
		return true
	default:
		return a < b
	}
}

// Precedence, kuralın öncelik sırasındaki konumudur (0 = en güçlü).
//
// Tanımsız kimlik için len(All) döner — tanımlı her kuraldan zayıf.
func (id ID) Precedence() int {
	if p, ok := precedence[id]; ok {
		return p
	}
	return len(All)
}

// InClass, verilen sınıftaki kuralları öncelik sırasında döndürür.
func InClass(c Class) []ID {
	var out []ID
	for _, id := range All {
		if id.Class() == c {
			out = append(out, id)
		}
	}
	return out
}

// Parse, sayısal kimliği doğrulayarak ID'ye çevirir.
//
// Veritabanından veya ground_truth.injected_rule'dan okunan değerler bu
// kapıdan geçer: CHECK kısıtı 1..5 diyor ama şema dışı bir yazıcı olabilir ve
// sessiz kabul, karışıklık matrisinde açıklanamayan bir satır üretirdi.
func Parse(v int) (ID, error) {
	id := ID(v)
	if !id.Valid() {
		return 0, fmt.Errorf("bütünlük kuralı: kimlik 1..5 olmalı (%d)", v)
	}
	return id, nil
}
