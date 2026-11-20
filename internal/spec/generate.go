package spec

import (
	"fmt"

	"github.com/donaina/assayer/internal/detrand"
	"github.com/donaina/assayer/pkg/assay"
)

const (
	// retryDepth is how many recent intents a retry may re-submit. A retry is
	// a repeat of an idempotency key, so it has to point at something the
	// system has plausibly seen; eight is enough to reach back past an
	// intervening transfer and still be a repeat rather than a resurrection
	// of ancient history.
	retryDepth = 8

	// minAmountCeiling is the amount range used when every account opens at
	// zero, where there is no balance to scale against.
	minAmountCeiling = 100
)

// Generator produces the intent stream for one scenario.
//
// It is a separate type from Spec because a Spec is a description a human
// writes, while the stream is derived: same spec and same seed must give the
// same intents, on every machine, forever. Nothing here reads the clock or the
// environment.
type Generator struct {
	spec      Spec
	rng       *detrand.Source
	weights   Weights
	ceiling   assay.Money
	emitted   int
	recent    []assay.Intent
	nextIdent int
}

// NewGenerator returns a generator for s.
//
// The seed is passed in rather than read from the spec so that a caller can
// replay one scenario across a sweep of seeds without editing the spec.
func NewGenerator(s Spec, seed uint64) (*Generator, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	g := &Generator{
		spec:    s,
		rng:     detrand.New(seed),
		weights: s.Weights.Resolved(),
	}
	var largest assay.Money
	for _, a := range s.Accounts {
		if a.Opening > largest {
			largest = a.Opening
		}
	}
	// Amounts are drawn up to the largest opening balance, and that is not an
	// accident: a transfer that can exhaust an account is exactly how a
	// non-negative-balance invariant gets exercised. A ceiling that kept every
	// amount comfortably affordable would make half the invariant suite
	// unreachable.
	g.ceiling = largest
	if g.ceiling < minAmountCeiling {
		g.ceiling = minAmountCeiling
	}
	return g, nil
}

// Next returns the next intent, or false when the stream is exhausted.
func (g *Generator) Next() (assay.Intent, bool) {
	if g.emitted >= g.spec.Ops {
		return assay.Intent{}, false
	}
	g.emitted++

	in := g.pick()
	g.recent = append(g.recent, in)
	if len(g.recent) > retryDepth {
		g.recent = g.recent[len(g.recent)-retryDepth:]
	}
	return in, true
}

// Remaining reports how many intents are left in the stream.
func (g *Generator) Remaining() int { return g.spec.Ops - g.emitted }

// pick chooses one intent according to the resolved weights.
func (g *Generator) pick() assay.Intent {
	switch g.roll() {
	case rollRetry:
		if in, ok := g.retry(); ok {
			return in
		}
		return g.transfer() // nothing to retry yet
	case rollDeposit:
		return g.deposit()
	default:
		return g.transfer()
	}
}

type roll int

const (
	rollTransfer roll = iota
	rollDeposit
	rollRetry
)

// roll maps the source onto the weight mix.
func (g *Generator) roll() roll {
	total := g.weights.Total()
	if total <= 0 {
		return rollTransfer
	}
	n := g.rng.Intn(total)
	if n < g.weights.Transfer {
		return rollTransfer
	}
	n -= g.weights.Transfer
	if n < g.weights.Deposit {
		return rollDeposit
	}
	return rollRetry
}

// retry re-issues a recent intent under a fresh client ID and the same
// idempotency key.
//
// The ID is fresh because the system should treat it as a new request that
// happens to duplicate an old one — which is exactly what a client that timed
// out and did not know it would send. Reusing the ID would let a system pass
// by deduplicating on the wrong field.
func (g *Generator) retry() (assay.Intent, bool) {
	if len(g.recent) == 0 {
		return assay.Intent{}, false
	}
	src := g.recent[g.rng.Intn(len(g.recent))]
	src.ID = g.ident("retry")
	return src, true
}

func (g *Generator) transfer() assay.Intent {
	from, to := g.pair()
	return assay.Intent{
		ID:     g.ident("tx"),
		Op:     assay.OpTransfer,
		From:   from,
		To:     to,
		Amount: g.amount(),
		Idem:   g.idem("tx"),
	}
}

func (g *Generator) deposit() assay.Intent {
	to := g.account()
	return assay.Intent{
		ID:     g.ident("dep"),
		Op:     assay.OpDeposit,
		To:     to,
		Amount: g.amount(),
		Idem:   g.idem("dep"),
	}
}

// pair returns two distinct accounts.
func (g *Generator) pair() (assay.AccountID, assay.AccountID) {
	n := len(g.spec.Accounts)
	i := g.rng.Intn(n)
	j := g.rng.Intn(n - 1)
	if j >= i {
		j++
	}
	return g.spec.Accounts[i].ID, g.spec.Accounts[j].ID
}

func (g *Generator) account() assay.AccountID {
	return g.spec.Accounts[g.rng.Intn(len(g.spec.Accounts))].ID
}

// amount returns a positive amount, with a bias towards the small end so that
// a run drains slowly enough to keep generating successful transfers rather
// than a stream of insufficient-funds rejections.
func (g *Generator) amount() assay.Money {
	ceil := int64(g.ceiling)
	if ceil <= 0 {
		return 1
	}
	// Squaring a uniform draw in [0,1) by taking the smaller of two draws
	// gives a triangular distribution centred low, which is the shape of real
	// payment traffic and keeps dust amounts common without making them the
	// whole run.
	a := g.rng.Int64n(ceil)
	b := g.rng.Int64n(ceil)
	if b < a {
		a = b
	}
	return assay.Money(a + 1)
}

// ident returns a new client-side identifier decorated with kind.
func (g *Generator) ident(kind string) string {
	g.nextIdent++
	return fmt.Sprintf("%s-%06d", kind, g.nextIdent)
}

// idem returns a fresh idempotency key. Retries copy an existing key rather
// than calling this, which is the whole point of a retry.
func (g *Generator) idem(kind string) string {
	return fmt.Sprintf("%s-key-%06d", kind, g.nextIdent)
}
