// Package detrand is a small deterministic pseudo-random source.
//
// math/rand's algorithm is documented as stable, and that promise is worth
// something — but "documented as stable" is weaker than "is ours". A seed must
// produce the same history on every machine and every toolchain forever,
// because histories get pasted into bug reports and compared across years.
// SplitMix64 has no state beyond a uint64 and no version to change.
package detrand

// Source is a SplitMix64 generator.
type Source struct{ state uint64 }

// New returns a source seeded with seed.
func New(seed uint64) *Source { return &Source{state: seed} }

// Uint64 returns the next value.
func (s *Source) Uint64() uint64 {
	s.state += 0x9e3779b97f4a7c15
	z := s.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Intn returns a value in [0,n). n must be positive. The modulo bias is left
// in place deliberately: scenarios are generated from ranges far smaller than
// 2^64, and a biased shuffle is not the kind of thing that costs a payment.
func (s *Source) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(s.Uint64() % uint64(n))
}

// Int64n returns a value in [0,n). n must be positive.
func (s *Source) Int64n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return int64(s.Uint64() % uint64(n))
}
