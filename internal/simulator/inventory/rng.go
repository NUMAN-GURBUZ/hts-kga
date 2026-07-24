// Package inventory, şebeke envanterini (site, sektör, hücre) üretir.
//
// Sprint 1 kapsamı: T-E02-03 (hex ızgara + site yerleşimi), T-E02-04
// (3 sektör üretimi), T-E02-05 (r_max), T-E02-06 (DB + Redis yükleme).
//
// Tüm üretim deterministiktir (K10): aynı run.seed aynı envanteri verir.
package inventory

import (
	"math/rand/v2"
)

// Rastgelelik akışları (stream).
//
// Aynı tohumdan farklı amaçlar için bağımsız diziler türetilir. Böylece
// T-E02-04'te frekans ataması değişse bile site yerleşimi aynı kalır —
// akışlar birbirini kaydırmaz.
const (
	streamSitePlacement uint64 = 1
	streamFrequency     uint64 = 2
)

// newRNG, koşu tohumundan verilen akış için deterministik bir üreteç türetir.
//
// PCG iki 64-bit tohum alır: koşu tohumu ile akış kimliği birlikte verilerek
// akışlar arası bağımsızlık sağlanır.
func newRNG(seed int64, stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), stream))
}
