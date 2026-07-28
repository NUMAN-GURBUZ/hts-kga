// Package ta, Timing Advance değerinin mesafeden türetilmesini ve karşılık
// gelen halka geometrisini tek bir yerde tanımlar.
//
// # Bu paketin var oluş nedeni
//
// TA iki kez, iki ayrı serviste kullanılır:
//
//	simülatör (T-E02-15) : gerçek mesafeden ta_value üretir      → FromDistance
//	analiz    (T-E03-04) : ta_value'dan olası mesafe kümesi çıkarır → Ring
//
// Bu ikisi birbirinin tam tersidir. Ayrı ayrı yazılsalardı — farklı yuvarlama,
// farklı aralık ucu, 2B yerine 3B mesafe — kayıt ile çıkarım sessizce
// ayrışırdı. Hata gürültüsüz olmazdı: analiz gerçek konumu halkanın dışında
// bırakır, kapsama@90 düşer, kalibrasyon λ'yı bunu telafi etmek için şişirir
// ve K1/K3 yanlış bir modelle "tutar". Bu yüzden tanım tektir ve her iki taraf
// da buradan çağırır.
//
// # Dondurulmuş tanım
//
//	çözünürlük : LTE 78,12 m · GSM 550 m
//	mesafe     : 2B (yatay). Anten yüksekliği TA'ya girmez.
//	yuvarlama  : ta = floor(d2D / res)
//	halka      : [ta·res, (ta+1)·res)   — alt uç dâhil, üst uç hariç
//
// Yuvarlamanın floor olması PBT #7'yi (imkânsız TA üretilemez) ispatlanabilir
// kılar: ta = floor(d/res) ⟹ ta·res ≤ d. Mesafe r_max'ı aşmıyorsa ta·res de
// aşmaz. En yakına yuvarlama (round) bu garantiyi bozardı — d = r_max
// yakınında ta·res > r_max çıkabilirdi.
//
// # Kapsam dışı
//
// Halkanın ızgara hücreleriyle nasıl ağırlıklandırılacağı (örtüşme oranı,
// ADR-18) burada değildir: o hesap altıgen geometrisi ister ve analiz
// katmanına aittir. Bu paket yalnızca yarıçap eksenini tanımlar; bağımlılığı
// stdlib ile sınırlıdır.
package ta

import (
	"fmt"
	"math"
	"strings"
)

// Technology, TA çözünürlüğünü belirleyen erişim teknolojisidir.
type Technology uint8

const (
	// Unknown, tanımsız teknolojidir (sıfır değer).
	Unknown Technology = iota
	// LTE, 78,12 m çözünürlüklü TA kullanır.
	LTE
	// GSM, 550 m çözünürlüklü TA kullanır.
	GSM
)

// TA çözünürlükleri (metre). Plan BÖLÜM C.1'de sabitlenmiştir.
const (
	// ResolutionLTEM, LTE TA adım uzunluğudur.
	//
	// 16·T_s ışık hızıyla: 16 / 30.720.000 Hz × c / 2 ≈ 78,125 m.
	// Plan 78,12 m yazar; plandaki değer kullanılır.
	ResolutionLTEM = 78.12
	// ResolutionGSMM, GSM TA adım uzunluğudur (yarım bit süresi ≈ 550 m).
	ResolutionGSMM = 550.0
)

// Valid, teknolojinin tanımlı olup olmadığını bildirir.
func (t Technology) Valid() bool { return t == LTE || t == GSM }

// ResolutionM, teknolojinin TA adım uzunluğunu döndürür (metre).
// Tanımsız teknoloji için 0 döner; çağıran önce Valid ile denetlemelidir.
func (t Technology) ResolutionM() float64 {
	switch t {
	case LTE:
		return ResolutionLTEM
	case GSM:
		return ResolutionGSMM
	default:
		return 0
	}
}

// String, teknolojinin config'deki yazımını döndürür.
func (t Technology) String() string {
	switch t {
	case LTE:
		return "LTE"
	case GSM:
		return "GSM"
	default:
		return "UNKNOWN"
	}
}

// Parse, senaryo config'indeki metni teknolojiye çevirir.
//
// config.TimingAdvanceSection.Technology alanının karşılığıdır; bu paket
// internal/config'i tanımaz, dönüşüm çağıran katmanda yapılır.
func Parse(s string) (Technology, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "LTE":
		return LTE, nil
	case "GSM":
		return GSM, nil
	default:
		return Unknown, fmt.Errorf("ta: bilinmeyen teknoloji %q (LTE veya GSM)", s)
	}
}

// FromDistance, yatay mesafeden TA değerini türetir (T-E02-15).
//
// d2DM **yatay** mesafedir: anten yüksekliği kasten dışarıdadır. Gerçek
// şebekede TA sinyalin gidiş-dönüş süresinden ölçülür ve 3B eğim mesafesini
// içerir, ama 30 m'lik bir anten 500 m yatay mesafede eğimi yalnızca ~0,9 m
// uzatır — TA adımının %1'inden azdır. Buna karşılık 2B seçimi, analizin
// halkayı yer düzleminde kurmasını sağlar; 3B kullanılsaydı analiz her ızgara
// hücresi için anten yüksekliğini geri çıkarmak zorunda kalırdı.
func FromDistance(d2DM float64, tech Technology) (int, error) {
	if !tech.Valid() {
		return 0, fmt.Errorf("ta: geçersiz teknoloji (%d)", uint8(tech))
	}
	if math.IsNaN(d2DM) || math.IsInf(d2DM, 0) || d2DM < 0 {
		return 0, fmt.Errorf("ta: mesafe sonlu ve negatif olmamalı (%g m)", d2DM)
	}

	res := tech.ResolutionM()
	n := int(math.Floor(d2DM / res))

	// Sonucu halkanın **kendi** sınırlarıyla hizala.
	//
	// d/res bölmesi en yakın çift sayıya yuvarlanır; adım sınırına bir ULP
	// kalan mesafelerde bölüm tam n'e yuvarlanabilir ve floor bir adım fazla
	// verir (ör. LTE'de 19·78,12'nin bir ULP altı → 19). O mesafe için
	// NewRing(19) = [1484,28 , 1562,40) döner ve mesafeyi **içermez**: tam
	// olarak bu paketin önlemek için var olduğu ayrışma. Aşağıdaki iki
	// düzeltme, sınırları NewRing ile aynı çarpımdan üreterek tur dönüşünü
	// yapısal olarak garanti eder; en fazla bir adım kaydırır.
	if n > 0 && d2DM < float64(n)*res {
		n--
	}
	if d2DM >= float64(n+1)*res {
		n++
	}
	return n, nil
}

// Ring, tek bir TA değerinin ima ettiği halkadır (T-E03-04).
//
// Kayıt "mesafe bu iki yarıçap arasındaydı" der; başka bir şey söylemez.
// Sınırlar keskindir: simülatör TA'yı gürültüsüz türetir, bu yüzden gerçek
// konum **tanım gereği** halkanın içindedir. Yumuşak bir kenar, dayanağı
// olmayan bir serbest parametre (σ_TA) eklemek olurdu.
type Ring struct {
	// InnerM, iç yarıçaptır (dâhil).
	InnerM float64
	// OuterM, dış yarıçaptır (hariç).
	OuterM float64
}

// NewRing, TA değerinden halkayı üretir.
func NewRing(taValue int, tech Technology) (Ring, error) {
	if !tech.Valid() {
		return Ring{}, fmt.Errorf("ta: geçersiz teknoloji (%d)", uint8(tech))
	}
	if taValue < 0 {
		return Ring{}, fmt.Errorf("ta: TA değeri negatif olamaz (%d)", taValue)
	}
	res := tech.ResolutionM()
	return Ring{
		InnerM: float64(taValue) * res,
		OuterM: float64(taValue+1) * res,
	}, nil
}

// Contains, verilen yatay mesafenin halkaya düşüp düşmediğini bildirir.
// Alt uç dâhil, üst uç hariçtir — FromDistance'ın floor tanımıyla birebir.
func (r Ring) Contains(d2DM float64) bool {
	return d2DM >= r.InnerM && d2DM < r.OuterM
}

// WidthM, halkanın kalınlığıdır; teknolojinin çözünürlüğüne eşittir.
func (r Ring) WidthM() float64 { return r.OuterM - r.InnerM }

// MaxValue, verilen kapsama yarıçapında görülebilecek en büyük TA değeridir.
//
// PBT #7'nin üst sınırı budur: d ≤ rMaxM olan her mesafe için
// FromDistance(d) ≤ MaxValue(rMaxM).
func MaxValue(rMaxM float64, tech Technology) (int, error) {
	if !tech.Valid() {
		return 0, fmt.Errorf("ta: geçersiz teknoloji (%d)", uint8(tech))
	}
	if math.IsNaN(rMaxM) || math.IsInf(rMaxM, 0) || rMaxM < 0 {
		return 0, fmt.Errorf("ta: kapsama yarıçapı sonlu ve negatif olmamalı (%g m)", rMaxM)
	}
	return int(math.Floor(rMaxM / tech.ResolutionM())), nil
}
