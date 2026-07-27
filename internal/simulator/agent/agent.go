// Package agent, simülasyon ajanlarının durumunu, yerleşimini, günlük
// rutinini ve hareketini yönetir.
//
// Sprint 2 kapsamı:
//   - T-E02-07  AgentState + reddetme örneklemesiyle yerleşim (ADR-08)
//   - T-E02-08  4 fazlı günlük rutin + haftalık periyodisite
//   - T-E02-09  Hız profili + gürültü + çift tampon (double buffer)
//
// Olay üretimi, TA, HMAC ve Kafka bu paketin kapsamı **dışındadır** (Sprint 3).
//
// Tüm rastgelelik ajan kimliği ve tick indeksinden deterministik olarak
// türetilir; hiçbir paylaşılan üreteç yoktur. Bu sayede 1000 goroutine
// eşzamanlı çalışsa da çıktı goroutine zamanlamasından bağımsızdır (K10).
package agent

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Phase, ajanın günlük rutinindeki evresidir (T-E02-08).
type Phase uint8

const (
	// PhaseHome, ajan evdedir (gece ve akşam saatleri, hafta sonları tüm gün).
	PhaseHome Phase = iota
	// PhaseCommuteToWork, ajan evden işe gitmektedir.
	PhaseCommuteToWork
	// PhaseWork, ajan iş yerindedir.
	PhaseWork
	// PhaseCommuteToHome, ajan işten eve dönmektedir.
	PhaseCommuteToHome
)

// phaseNames, Phase değerlerinin okunabilir karşılıklarıdır.
var phaseNames = [...]string{"HOME", "COMMUTE_TO_WORK", "WORK", "COMMUTE_TO_HOME"}

// String, evrenin okunabilir adını döndürür.
func (p Phase) String() string {
	if int(p) >= len(phaseNames) {
		return fmt.Sprintf("Phase(%d)", uint8(p))
	}
	return phaseNames[p]
}

// Valid, evre değerinin tanımlı olup olmadığını bildirir.
func (p Phase) Valid() bool { return int(p) < len(phaseNames) }

// IsCommuting, ajanın yolculuk hâlinde olup olmadığını bildirir.
func (p Phase) IsCommuting() bool {
	return p == PhaseCommuteToWork || p == PhaseCommuteToHome
}

// State, tek bir ajanın anlık durumudur.
//
// **Değer tipidir**: işaretçi veya dilim içermez. Çift tamponlu güncelleme
// (T-E02-09) bu sayede kopyalama ile çalışır; paylaşılan yazılabilir bellek
// olmadığından veri yarışı yapısal olarak imkânsızdır.
type State struct {
	// ID, ajanın koşu içindeki sıra numarasıdır (0 tabanlı).
	// ground_truth.agent_id ile eşleşir (Sprint 3).
	ID int

	// Home, ajanın ev çapasıdır (ENU, metre).
	Home geo.Point
	// Work, ajanın iş çapasıdır (ENU, metre).
	Work geo.Point

	// Pos, ajanın anlık konumudur (ENU, metre).
	Pos geo.Point
	// Phase, ajanın anlık evresidir.
	Phase Phase

	// DepartWorkMin, hafta içi işe çıkış anıdır (gün başından beri dakika).
	DepartWorkMin int
	// DepartHomeMin, hafta içi eve dönüş anıdır (gün başından beri dakika).
	DepartHomeMin int
	// CommuteMin, tek yön yolculuk süresidir (dakika), mesafe ve profil
	// hızından türetilir.
	CommuteMin int
}

// HomeWorkDistanceM, ajanın ev–iş mesafesini döndürür (metre).
func (s State) HomeWorkDistanceM() float64 {
	return geo.Distance(s.Home, s.Work)
}

// Validate, ajan durumunun tutarlı olup olmadığını denetler.
func (s State) Validate() error {
	if s.ID < 0 {
		return fmt.Errorf("ajan: kimlik negatif olamaz (%d)", s.ID)
	}
	if !s.Phase.Valid() {
		return fmt.Errorf("ajan %d: geçersiz evre (%d)", s.ID, uint8(s.Phase))
	}
	if s.CommuteMin <= 0 {
		return fmt.Errorf("ajan %d: yolculuk süresi pozitif olmalı (%d dk)", s.ID, s.CommuteMin)
	}
	if s.DepartWorkMin < 0 || s.DepartWorkMin >= minutesPerDay {
		return fmt.Errorf("ajan %d: işe çıkış anı gün içinde olmalı (%d dk)", s.ID, s.DepartWorkMin)
	}
	if s.DepartHomeMin < 0 || s.DepartHomeMin >= minutesPerDay {
		return fmt.Errorf("ajan %d: eve dönüş anı gün içinde olmalı (%d dk)", s.ID, s.DepartHomeMin)
	}
	if s.DepartHomeMin <= s.DepartWorkMin+s.CommuteMin {
		return fmt.Errorf("ajan %d: eve dönüş, işe varıştan önce olamaz (%d ≤ %d+%d)",
			s.ID, s.DepartHomeMin, s.DepartWorkMin, s.CommuteMin)
	}
	return nil
}
