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
type nonneg struct{}

func (nonneg) Name() string { return "INV-NONNEG" }

func (nonneg) Check(o assay.Observation) []Finding {
	var out []Finding
	for _, b := range o.After.Sorted() {
		if b.Balance >= 0 {
			continue
		}
		out = append(out, Finding{
			Invariant: "INV-NONNEG",
			Detail:    fmt.Sprintf("account %s ended at %d", b.Account, b.Balance),
			Subjects:  subjects(b.Account),
		})
	}
	return out
}
