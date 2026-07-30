// Olay talep defteri (ADR-28/2, ADR-28/6).

package detector

import (
	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// Claims, hangi olayın hangi kural tarafından talep edildiğini tutar.
//
// # Neden defter gerekiyor
//
// ADR-28/2: bir olay için en çok bir kanonik bulgu yazılır. Bir kaydın nasıl
// bozulduğunun tek bir en iyi açıklaması vardır; kaydırılmış bir damganın yol
// açtığı hız anomalisi ayrı bir manipülasyon değil, aynı manipülasyonun
// sonucudur.
//
// # Fazlar arasında devir
//
// Toplu faz defteri `integrity_findings`'ten yükler (ADR-28/6). Bellekte
// taşınsaydı toplu fazı tek başına yeniden koşmak öncelik bilgisini
// kaybederdi.
//
// # Bellek sınırı
//
// Boyut **bulgu** sayısıyla büyür, olay sayısıyla değil: koşu başına ~4.000
// girdi (~160 KB). 300.000 olaylık koşu ile 3.000.000 olaylık koşu aynı
// belleği kullanır.
//
// Eşzamanlı kullanım için güvenli **değildir**; motor tek goroutine'den
// çağırır (akış fazı tek tüketici, toplu faz tek tarama).
type Claims struct {
	byEvent map[uuid.UUID]integrityrule.ID
}

// NewClaims, boş bir defter açar.
func NewClaims() *Claims {
	return &Claims{byEvent: make(map[uuid.UUID]integrityrule.ID)}
}

// NewClaimsFrom, önceden yüklenmiş taleplerden defter kurar (faz devri).
func NewClaimsFrom(prior map[uuid.UUID]integrityrule.ID) *Claims {
	c := NewClaims()
	for eventID, rule := range prior {
		c.byEvent[eventID] = rule
	}
	return c
}

// Of, olayı talep eden kuralı döndürür.
func (c *Claims) Of(eventID uuid.UUID) (integrityrule.ID, bool) {
	rule, ok := c.byEvent[eventID]
	return rule, ok
}

// Claim, olayı verilen kural adına talep eder.
//
// Zaten talep edilmiş bir olay **yeniden talep edilmez**: ilk talep kazanır
// (ADR-28/7). Dönüş değeri talebin kabul edilip edilmediğidir.
func (c *Claims) Claim(eventID uuid.UUID, rule integrityrule.ID) bool {
	if _, taken := c.byEvent[eventID]; taken {
		return false
	}
	c.byEvent[eventID] = rule
	return true
}

// Len, defterdeki talep sayısıdır.
func (c *Claims) Len() int { return len(c.byEvent) }
