package invariant_test

import (
	"context"
	"testing"

	"github.com/donaina/assayer/internal/driver/native"
	"github.com/donaina/assayer/internal/invariant"
	"github.com/donaina/assayer/internal/run"
	"github.com/donaina/assayer/internal/spec"
	"github.com/donaina/assayer/pkg/assay"
)

// TestKnownGoodLedgerPassesEveryInvariantAtEverySeed is the suite that keeps
// the harness honest.
//
// A correctness harness has one failure mode worse than missing a bug, and it
// is crying wolf: a tool that reports a violation against a system that is
// fine gets switched off, and then it catches nothing. So before assayer is
// allowed to accuse anybody, its own reference ledger — which is too simple to
// be wrong — has to come out clean under every scenario mix, at every seed,
// with every invariant switched on.
//
// This test found real false positives when it was first written, which is the
// reason it exists rather than a note asking people to be careful.
func TestKnownGoodLedgerPassesEveryInvariantAtEverySeed(t *testing.T) {
	scenarios := []struct {
		name    string
		weights spec.Weights
	}{
		// Transfers dominate, so most operations depend on two accounts
		// staying in step.
		{"mostly-transfers", spec.Weights{Transfer: 90, Deposit: 10}},
		// Deposits dominate, so the run is liquid and refusals are rare:
		// the other end of the space from mostly-transfers.
		{"mostly-deposits", spec.Weights{Transfer: 10, Deposit: 90}},
		// Retries dominate, so a large share of attempts are deliberately
		// ambiguous and the checks lean on follow-up lookups.
		{"retry-heavy", spec.Weights{Transfer: 50, Deposit: 20, Retry: 30}},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			for seed := uint64(1); seed <= 12; seed++ {
				o := mustRun(t, sc.weights, seed)
				for _, f := range invariant.Run(o) {
					t.Errorf("seed %d: %s", seed, f)
				}
			}
		})
	}
}

// TestKnownGoodLedgerAgreesWithItselfAcrossSeeds pins the other half of a
// false-positive claim: it is not enough that some seeds pass, because a
// checker that only works on even seeds would still look green above.
func TestKnownGoodLedgerAgreesWithItselfAcrossSeeds(t *testing.T) {
	weights := spec.DefaultWeights
	for seed := uint64(1); seed <= 24; seed++ {
		o := mustRun(t, weights, seed)
		if findings := invariant.Run(o); len(findings) != 0 {
			t.Fatalf("seed %d: %d findings, first: %s", seed, len(findings), findings[0])
		}
		if len(o.Attempts) == 0 {
			t.Fatalf("seed %d: run produced no attempts", seed)
		}
	}
}

// mustRun runs one scenario against a fresh reference ledger.
//
// A fresh driver per seed, not a reused one: a shared ledger would let an
// earlier seed's state leak into the next and turn a clean run into a pass for
// the wrong reason.
func mustRun(t *testing.T, w spec.Weights, seed uint64) assay.Observation {
	t.Helper()

	opening := map[assay.AccountID]assay.Money{
		"A": 1_000_000,
		"B": 250_000,
		"C": 0,
	}
	limits := map[assay.AccountID]assay.Money{
		"A": 5_000_000,
		"B": 5_000_000,
		"C": 5_000_000,
	}

	sp := spec.Spec{
		Name:       "known-good",
		Seed:       seed,
		Ops:        120,
		Accounts:   []spec.Account{{ID: "A", Opening: 1_000_000}, {ID: "B", Opening: 250_000}, {ID: "C", Opening: 0}},
		Limits:     limits,
		Weights:    w,
		Invariants: []string{"INV-CONSERVE", "INV-DOUBLE", "INV-NONNEG"},
	}
	if err := sp.Validate(); err != nil {
		t.Fatalf("the scenario this test runs is not a valid spec: %v", err)
	}

	o, err := run.New(native.New(opening)).WithLookups(2).Run(context.Background(), sp, seed)
	if err != nil {
		t.Fatalf("seed %d: reference run failed: %v", seed, err)
	}
	return o
}
