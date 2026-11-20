package assay

import (
	"errors"
	"fmt"
)

// Money is an amount in the currency's minor unit (kobo, cents).
//
// It is an integer because a ledger that uses floats produces errors
// indistinguishable from fraud. 0.1+0.2 != 0.3 is survivable in a game
// scoreboard and is not survivable in a bank, so the type makes the mistake
// unrepresentable rather than merely discouraged.
type Money int64

// ErrOverflow reports an addition that left the int64 range. It is returned
// rather than wrapped around, because a silently wrapped balance is a balance
// that has become a lie.
var ErrOverflow = errors.New("assay: money overflow")

// Add returns m+n, or ErrOverflow.
func (m Money) Add(n Money) (Money, error) {
	sum := m + n
	if (n > 0 && sum < m) || (n < 0 && sum > m) {
		return 0, ErrOverflow
	}
	return sum, nil
}

// Neg returns -m.
func (m Money) Neg() Money { return -m }

// Sub returns m-n, or ErrOverflow.
func (m Money) Sub(n Money) (Money, error) { return m.Add(n.Neg()) }

// Sign reports -1, 0 or 1 according to the sign of m.
func (m Money) Sign() int {
	switch {
	case m < 0:
		return -1
	case m > 0:
		return 1
	}
	return 0
}

// String renders the amount with two decimal places, e.g. "-12.34". It exists
// so that history files and violation messages are readable by a human being
// debugging a payment at 3am.
func (m Money) String() string {
	neg := m < 0
	u := uint64(m)
	if neg {
		u = uint64(^m) + 1 // two's-complement negate; safe for the minimum int64
	}
	if neg {
		return fmt.Sprintf("-%d.%02d", u/100, u%100)
	}
	return fmt.Sprintf("%d.%02d", u/100, u%100)
}
