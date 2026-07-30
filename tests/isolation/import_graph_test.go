// Kör testin dördüncü katmanı: import grafiği (ADR-20).
//
// İlk üç katman veriye erişimi engeller:
//
//  1. Kafka ACL      : analysis principal → hts.groundtruth → DENY
//  2. PostgreSQL rol : analysis rolü      → ground_truth    → SELECT yok
//  3. Enjeksiyon etiketi: injected_rule ground_truth'ta, S4 göremez
//
// Dördüncüsü **koda** erişimi engeller. Analiz motoru simülatörün gölgeleme
// alanını import edebilseydi, aynı tohumdan aynı alanı yeniden üretip
// gerçekleşmiş gölgelemeyi öğrenebilirdi. O noktada ADR-03'ün bütün iddiası —
// "analiz gölgelemenin gerçekleşmiş değerini bilmez" — veri katmanı ne kadar
// sıkı olursa olsun geçersiz olurdu.
//
// Kısıt insan denetimine bırakılmaz: bu test bağımlılık kapanışını okur.
package isolation

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	modulePath   = "github.com/NUMAN-GURBUZ/hts-kga"
	analysisTree = modulePath + "/internal/analysis/"
	forbidden    = modulePath + "/internal/simulator/"
)

// TestAnalysisDoesNotDependOnSimulator, analiz ağacındaki hiçbir paketin
// simülatör ağacına — doğrudan veya dolaylı — bağımlı olmadığını sınar.
func TestAnalysisDoesNotDependOnSimulator(t *testing.T) {
	packages := listPackages(t, analysisTree+"...")
	if len(packages) == 0 {
		t.Skip("internal/analysis altında henüz paket yok")
	}
	t.Logf("denetlenen analiz paketi: %d", len(packages))

	for _, pkg := range packages {
		for _, dep := range listDeps(t, pkg) {
			if strings.HasPrefix(dep, forbidden) {
				t.Errorf("%s → %s\n"+
					"analiz katmanı simülatöre bağımlı olamaz (ADR-20). Deterministik RF "+
					"hesapları internal/rf paketindedir; gölgeleme alanı simülatörde kalır.",
					pkg, dep)
			}
		}
	}
}

// TestDeterministicRFIsFreeOfShadowing, internal/rf paketinin gölgeleme
// alanına bağımlı olmadığını sınar.
//
// Ayrımın yönü budur: radio → rf olur, rf → radio asla. Ters bağımlılık,
// taşınan kodun gölgelemeyi geri çağırdığı anlamına gelirdi ve analiz onu
// dolaylı olarak elde ederdi.
func TestDeterministicRFIsFreeOfShadowing(t *testing.T) {
	for _, dep := range listDeps(t, modulePath+"/internal/rf") {
		if strings.HasPrefix(dep, forbidden) {
			t.Errorf("internal/rf → %s\ndeterministik RF paketi simülatöre bağımlı olamaz (ADR-20)", dep)
		}
	}
}

// TestSharedContractsAreDependencyFree, ikiz sözleşme paketlerinin
// (pkg/ta, pkg/split, pkg/htswire) iç bağımlılık taşımadığını sınar.
//
// Bu paketler hem simülatör hem analiz tarafından çağrılır. İçlerinden birine
// bir internal/ bağımlılığı sızarsa, iki taraftan biri onu import edemez hâle
// gelir ve tanım yeniden ikizlenir — paketlerin var oluş nedeni ortadan kalkar.
func TestSharedContractsAreDependencyFree(t *testing.T) {
	for _, pkg := range []string{
		modulePath + "/pkg/ta",
		modulePath + "/pkg/split",
		modulePath + "/pkg/htswire",
		modulePath + "/pkg/integrityrule",
	} {
		for _, dep := range listDeps(t, pkg) {
			if strings.HasPrefix(dep, modulePath+"/internal/") {
				t.Errorf("%s → %s\nortak sözleşme paketleri internal/ bağımlılığı taşıyamaz", pkg, dep)
			}
		}
	}
}

// TestIntegrityDoesNotDependOnSimulator, bütünlük ağacının simülatöre — ve
// özellikle **enjektöre** — bağımlı olmadığını sınar (ADR-27, T-E05-00).
//
// Enjektör hangi kaydın bozulduğunu bilir: `Apply` etiketi oraya koyar. S4 o
// paketi import edebilseydi, aynı tohumdan enjeksiyon çekilişini yeniden
// üretip hangi olayların enjekte edildiğini **hesaplayabilirdi**. O noktada
// K7'nin bütün iddiası — "bütünlük tespiti kör testtir" — veritabanı rolleri ne
// kadar sıkı olursa olsun geçersiz olurdu.
//
// Ortak kural kimlikleri bu yüzden `pkg/integrityrule`'dadır: kimlik paylaşmak
// kuralın *hangi olayı* vurduğunu söylemez.
func TestIntegrityDoesNotDependOnSimulator(t *testing.T) {
	packages := listPackages(t, modulePath+"/internal/integrity/...")
	if len(packages) == 0 {
		t.Skip("internal/integrity altında henüz paket yok")
	}
	t.Logf("denetlenen bütünlük paketi: %d", len(packages))

	for _, pkg := range packages {
		for _, dep := range listDeps(t, pkg) {
			if strings.HasPrefix(dep, forbidden) {
				t.Errorf("%s → %s\n"+
					"bütünlük katmanı simülatöre bağımlı olamaz (ADR-27). Ortak kural "+
					"kimlikleri pkg/integrityrule'dadır; enjeksiyon mantığı simülatörde kalır.",
					pkg, dep)
			}
		}
	}
}

// TestIntegrityDoesNotDependOnAnalysis, bütünlük ağacının analiz ağacına
// bağımlı olmadığını sınar.
//
// Ayrım kör testle ilgili değil, sorumlulukla ilgilidir: S4'ün ihtiyacı olan
// envanter görünümü (hücre kimliği + site konumu) analiz motorunun ihtiyacından
// çok küçüktür. `internal/analysis/params` import edilseydi bütünlük servisi
// ızgara, hüzme deseni ve komşu seçimi kodunu da taşırdı — ve analiz
// katmanındaki bir değişiklik bütünlük ölçümünü sessizce etkileyebilirdi.
func TestIntegrityDoesNotDependOnAnalysis(t *testing.T) {
	packages := listPackages(t, modulePath+"/internal/integrity/...")
	if len(packages) == 0 {
		t.Skip("internal/integrity altında henüz paket yok")
	}

	for _, pkg := range packages {
		for _, dep := range listDeps(t, pkg) {
			if strings.HasPrefix(dep, modulePath+"/internal/analysis/") {
				t.Errorf("%s → %s\nbütünlük katmanı analiz katmanına bağımlı olamaz", pkg, dep)
			}
		}
	}
}

// TestDetectorSeesKnownDependency, denetleyicinin kendisinin çalıştığını
// doğrular.
//
// Yukarıdaki üç test "bağımlılık yok" der. Bu tür testlerin sessiz başarısızlık
// biçimi, bağımlılıkları hiç göremiyor olmaktır — o zaman da hep geçerler.
// Bilinen bir bağımlılık (radio → rf, ADR-20 taşımasının kendisi) üzerinde
// pozitif kontrol yapılır.
func TestDetectorSeesKnownDependency(t *testing.T) {
	deps := listDeps(t, modulePath+"/internal/simulator/radio")

	found := false
	for _, dep := range deps {
		if dep == modulePath+"/internal/rf" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("denetleyici bilinen bağımlılığı (radio → rf) görmüyor; "+
			"diğer testlerin geçmesi anlamsız (%d bağımlılık tarandı)", len(deps))
	}
}

// listPackages, verilen desene uyan paketleri döndürür.
func listPackages(t *testing.T, pattern string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", pattern).Output()
	if err != nil {
		// Desene uyan paket yoksa go list hata döndürür; bu bir başarısızlık
		// değildir (henüz yazılmamış olabilir).
		return nil
	}
	return nonEmptyLines(string(out))
}

// listDeps, paketin bağımlılık kapanışını döndürür (dolaylılar dâhil).
func listDeps(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	return nonEmptyLines(string(out))
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
