package native_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/donaina/assayer/internal/driver/native"
	"github.com/donaina/assayer/internal/invariant"
	"github.com/donaina/assayer/internal/run"
	"github.com/donaina/assayer/internal/spec"
	"github.com/donaina/assayer/pkg/assay"
)

// corpusSeeds is the seed range every corpus entry is judged over.
//
// A defect that only fires on one seed is not caught by a harness, it is caught
// by luck, and the difference decides whether the tool is worth running in CI.
// The range is small on purpose: it is wide enough that a generator mistake
// shows up as a miss, and narrow enough that the suite stays under a second.
var corpusSeeds = []uint64{1, 2, 3, 4, 5}

// corpus is every way this ledger can be wrong, and the invariant that must
// name it.
//
// The defects live in the ledger itself (NewBroken) rather than in a mock here,
// because a bug injected by the test proves the detector works against the
// test's idea of the bug. The two would drift: the ledger would grow a real
// deviation, the test would keep passing, and the corpus would quietly stop
// covering the thing it claims to cover.
var corpus = []struct {
	name     string
	breakage native.Breakage
	want     string // the invariant that must fire
	why      string // what a reader should expect to see
	spec     spec.Spec
}{
	{
		name:     "duplicate-posting",
		breakage: native.BreakIdempotency,
		want:     "INV-DOUBLE",
		why:      "a retried deposit is posted a second time under the same idempotency key",
		spec: spec.Spec{
			Name: "corpus-duplicate-posting",
			Ops:  40,
			Accounts: []spec.Account{
				{ID: "alice", Opening: 1_000_000_00},
				{ID: "bob", Opening: 1_000_000_00},
			},
			// Retries must actually happen, or the defect has nothing to fire
			// on. A duplicate-posting test that never retries tests nothing.
			Weights: spec.Weights{Transfer: 40, Deposit: 40, Retry: 20},
			Timeout: 5 * time.Second,
		},
	},
	{
		name:     "overdraft",
		breakage: native.BreakOverdraft,
		want:     "INV-NONNEG",
		why:      "a transfer larger than the balance is accepted, leaving the account negative",
		spec: spec.Spec{
			Name: "corpus-overdraft",
			Ops:  60,
			Accounts: []spec.Account{
				// Small balances against the generator's amounts, so the
				// insufficient-funds path is entered often rather than rarely.
				{ID: "alice", Opening: 1_000},
				{ID: "bob", Opening: 1_000},
			},
			Weights: spec.Weights{Transfer: 90, Deposit: 5, Retry: 5},
			Timeout: 5 * time.Second,
		},
	},
	{
		name:     "phantom-credit",
		breakage: native.BreakDoubleCredit,
		want:     "INV-CONSERVE",
		why:      "a deposit credits the balance twice while writing one journal entry",
		spec: spec.Spec{
			Name: "corpus-phantom-credit",
			Ops:  40,
			Accounts: []spec.Account{
				{ID: "alice", Opening: 500_000_00},
				{ID: "bob", Opening: 500_000_00},
			},
			Weights: spec.Weights{Transfer: 40, Deposit: 50, Retry: 10},
			Timeout: 5 * time.Second,
		},
	},
}

// TestEveryKnownDefectIsCaughtAndNamed is the corpus: each entry must be found,
// and found by the invariant that describes it.
//
// Detection alone is not enough. A harness that reports "something is wrong"
// on every run is indistinguishable from one that reports it at random, so the
// assertion is not that findings exist but that the *right* invariant fired.
// The wrong invariant naming a real defect is a worse failure than silence: it
// sends whoever reads the report to the wrong line of code.
func TestEveryKnownDefectIsCaughtAndNamed(t *testing.T) {
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			for _, seed := range corpusSeeds {
				o := drive(t, native.NewBroken(openings(c.spec), c.breakage), c.spec, seed)
				findings := invariant.Run(o)
				if !names(findings, c.want) {
					t.Errorf("seed %d: %s, and %s did not fire; findings were %s",
						seed, c.why, c.want, render(findings))
				}
			}
		})
	}
}

// TestTheKnownGoodLedgerPassesEverySeed is the other half of the corpus, and
// the one that keeps the tool switched on.
//
// A detector nobody trusts is a detector nobody runs. The correct ledger and
// the broken ones share every line except the deviation, so if this test fails
// alongside the corpus test the cause is the checker, not the defect — and if
// only this one fails, the checker has started inventing violations on a system
// that is doing nothing wrong.
func TestTheKnownGoodLedgerPassesEverySeed(t *testing.T) {
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			for _, seed := range corpusSeeds {
				o := drive(t, native.New(openings(c.spec)), c.spec, seed)
				if findings := invariant.Run(o); len(findings) > 0 {
					t.Fatalf("seed %d: the correct ledger was reported as faulty: %s",
						seed, render(findings))
				}
				if unsettled := invariant.Unsettled(o); len(unsettled) > 0 {
					t.Fatalf("seed %d: the correct ledger left %d intents unsettled: %v",
						seed, len(unsettled), unsettled)
				}
			}
		})
	}
}

// TestTheCorpusCoversEveryBreakage stops the corpus from silently shrinking.
//
// NewBroken can be taught a deviation that no entry here exercises — the
// compiler will not say so, and the defect will simply never be caught. This is
// the cheapest possible version of that guard: every named breakage must appear
// in the table above.
func TestTheCorpusCoversEveryBreakage(t *testing.T) {
	covered := make(map[native.Breakage]bool, len(corpus))
	for _, c := range corpus {
		covered[c.breakage] = true
	}
	for _, b := range native.AllBreakages {
		if b == native.BreakNone {
			continue
		}
		if !covered[b] {
			t.Errorf("breakage %q has no corpus entry, so nothing would ever catch it", b)
		}
	}
}

// drive runs one scenario against one ledger at one seed.
func drive(t *testing.T, d assay.Driver, s spec.Spec, seed uint64) assay.Observation {
	t.Helper()
	o, err := run.New(d).Run(context.Background(), s, seed)
	if err != nil {
		t.Fatalf("seed %d: %v", seed, err)
	}
	return o
}

// openings turns a spec's accounts into a native ledger's opening balances.
func openings(s spec.Spec) map[assay.AccountID]assay.Money {
	out := make(map[assay.AccountID]assay.Money, len(s.Accounts))
	for _, a := range s.Accounts {
		out[a.ID] = a.Opening
	}
	return out
}

// names reports whether findings contains inv.
func names(findings []invariant.Finding, inv string) bool {
	for _, f := range findings {
		if f.Invariant == inv {
			return true
		}
	}
	return false
}

// render writes findings as one line for the failure message.
func render(findings []invariant.Finding) string {
	if len(findings) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, f.String())
	}
	return strings.Join(parts, "; ")
}
