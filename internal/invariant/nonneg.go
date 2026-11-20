package invariant

import (
	"fmt"

	"github.com/donaina/assayer/pkg/assay"
)

// nonneg checks INV-NONNEG: no account is overdrawn.
//
// Every account in the scenarios means the same thing: a balance that may not
// go below zero. A negative balance is how a system reports that it spent money
// it did not have, and in a payment system that is the single most expensive
// way to be wrong.
//
// The check reads the system's own claims, not the model's. A model can only
// go negative if the system said yes to something it should have refused, and
// the ledger is where that becomes visible.
//
// It reads every balance taken during the run, not just the last one. An
// account that dips below zero and is then covered by an incoming credit ends
// the run looking healthy, and that is the shape of the incident that matters:
// the money was spent before it arrived, and the closing balance is a lie about
// the window in between.
type nonneg struct{}

func (nonneg) Name() string { return "INV-NONNEG" }

func (nonneg) Check(o assay.Observation) []Finding {
	var out []Finding
	// One finding per account, at the first reading that shows it overdrawn.
	// Reporting every reading would bury the first occurrence — the one that
	// says when it started — under the ones that merely continue it.
	seen := make(map[assay.AccountID]bool)
	for i, st := range o.Readings() {
		for _, b := range st.Sorted() {
			if b.Balance >= 0 || seen[b.Account] {
				continue
			}
			seen[b.Account] = true
			out = append(out, Finding{
				Invariant: "INV-NONNEG",
				Detail:    fmt.Sprintf("account %s was %d at reading %d", b.Account, b.Balance, i),
				Subjects:  subjects(b.Account),
			})
		}
	}
	return out
}
