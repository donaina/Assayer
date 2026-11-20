package main

import (
	"sort"
	"time"

	"github.com/donaina/assayer/internal/spec"
	"github.com/donaina/assayer/pkg/assay"
)

// scenario is a named spec plus the sentence the help text shows for it.
type scenario struct {
	Desc string
	Spec spec.Spec
}

// scenarios are the runs worth having a name for.
//
// They are not a menu of features; they are three different ways to be wrong.
// A ledger can conserve money and still let an account go negative, or keep
// every account non-negative and still lose a naira to a rounding path that
// only fires under contention. Each scenario leans on one of those.
var scenarios = map[string]scenario{
	"smoke": {
		Desc: "two accounts, a short mixed stream — the two-minute check",
		Spec: spec.Spec{
			Name: "smoke",
			Seed: 1,
			Ops:  50,
			Accounts: []spec.Account{
				{ID: "alice", Opening: 100_000_00},
				{ID: "bob", Opening: 50_000_00},
			},
			Timeout: 5 * time.Second,
		},
	},

	"conservation": {
		Desc: "deposit-heavy, three accounts — money entering must be accounted for",
		Spec: spec.Spec{
			Name: "conservation",
			Seed: 2,
			Ops:  500,
			Accounts: []spec.Account{
				{ID: "alice", Opening: 1_000_000_00},
				{ID: "bob", Opening: 250_000_00},
				{ID: "carol", Opening: 0},
			},
			// Deposits dominate so the total is free to move. A conservation
			// check on a closed system is a tautology: nothing can change, so
			// nothing can be caught changing.
			Weights: spec.Weights{Transfer: 50, Deposit: 40, Retry: 10},
			Timeout: 10 * time.Second,
		},
	},

	"contention": {
		Desc: "transfer-heavy between one pair — where double-spend and overdraft live",
		Spec: spec.Spec{
			Name: "contention",
			Seed: 3,
			Ops:  200,
			Accounts: []spec.Account{
				{ID: "alice", Opening: 10_000_00},
				{ID: "bob", Opening: 10_000_00},
			},
			// Almost every intent moves money between the same two accounts,
			// and retries are frequent, so the same intent is likely to be
			// submitted twice. That is the shape that produces a double
			// charge in a system whose idempotency key is per-process.
			Weights: spec.Weights{Transfer: 90, Deposit: 5, Retry: 5},
			Timeout: 10 * time.Second,
		},
	},
}

// scenarioNames returns the scenario names in a stable order, so that help
// output and error messages do not shuffle between runs.
func scenarioNames() []string {
	names := make([]string, 0, len(scenarios))
	for n := range scenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// compile-time proof that the account ids used above are the type the spec
// wants, so a rename in pkg/assay fails here rather than in a scenario table.
var _ = map[assay.AccountID]assay.Money{}
