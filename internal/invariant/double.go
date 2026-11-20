package invariant

import (
	"fmt"
	"sort"
	"strings"

	"github.com/donaina/assayer/pkg/assay"
)

// doublePost checks INV-DOUBLE: one intent, one effect.
//
// The idempotency key is the client's promise that a retry is the same intent.
// If the system accepts that key twice it has charged twice, and no amount of
// consistency checking will make the customer whole. This check reports the key
// and every transaction that came out of it, because the second transaction is
// the one somebody has to reverse.
type doublePost struct{}

func (doublePost) Name() string { return "INV-DOUBLE" }

func (doublePost) Check(o assay.Observation) []Finding {
	byIdem := acceptedByIDem(o)

	keys := make([]string, 0, len(byIdem))
	for k, attempts := range byIdem {
		if len(attempts) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	out := make([]Finding, 0, len(keys))
	for _, k := range keys {
		attempts := byIdem[k]
		txns := make([]string, 0, len(attempts))
		for _, a := range attempts {
			txns = append(txns, a.Result.TxnID)
		}
		out = append(out, Finding{
			Invariant: "INV-DOUBLE",
			Detail: fmt.Sprintf("idempotency key %q was accepted %d times: %s",
				k, len(attempts), strings.Join(txns, ", ")),
			Subjects: idemSubject(k),
		})
	}
	return out
}
