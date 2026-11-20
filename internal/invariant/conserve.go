package invariant

import (
	"fmt"

	"github.com/donaina/assayer/internal/model"
	"github.com/donaina/assayer/pkg/assay"
)

// conserve checks INV-CONSERVE: money is neither created nor destroyed.
//
// The expected total comes from the model, which believes each idempotency key
// once. The observed total comes from the system under test. They differ
// exactly when the system moved money that no accepted intent accounts for —
// a double-post, a lost credit, a posting from an operation that was reported
// as rejected, or a transaction that survived a crash on one side only.
type conserve struct{}

func (conserve) Name() string { return "INV-CONSERVE" }

func (conserve) Check(o assay.Observation) []Finding {
	m := model.New(o.Before)
	for _, a := range o.Attempts {
		m.Apply(a.Intent, a.Result)
	}

	want := m.Total()
	got := o.After.Total()
	if want == got {
		return nil
	}

	return []Finding{{
		Invariant: "INV-CONSERVE",
		Detail: fmt.Sprintf(
			"expected a total of %d after %d accepted intent(s), found %d (difference %+d)",
			want, len(accepted(o)), got, got-want),
		Subjects: divergentAccounts(m, o.After),
	}}
}

// divergentAccounts names each account whose balance disagrees with the model,
// so a finding points somewhere instead of saying "somewhere in here".
//
// The account set is the union of what the system reported and what the model
// expected, because the two most expensive ways to lose money look opposite from
// each side: an account with the wrong balance appears only in the system's
// snapshot, and an account that disappeared takes its money with it and appears
// only in the model. Walking one side would report a total mismatch with no
// account attached to it, which is exactly the finding that gets dismissed.
func divergentAccounts(m *model.Model, after assay.State) []string {
	var out []string
	reported := make(map[assay.AccountID]bool, len(after.Accounts))
	for _, b := range after.Sorted() {
		reported[b.Account] = true
		if !m.Knows(b.Account) {
			out = append(out, fmt.Sprintf("%s: unknown to the harness, holds %d", b.Account, b.Balance))
			continue
		}
		if want := m.Balance(b.Account); want != b.Balance {
			out = append(out, fmt.Sprintf("%s: expected %d, got %d", b.Account, want, b.Balance))
		}
	}

	// Only accounts the model holds a non-zero balance in: a zero-balance
	// account is indistinguishable from one the system does not bother to
	// report, and most systems do not report the latter.
	for _, b := range m.Snapshot().Sorted() {
		if reported[b.Account] || b.Balance == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%s: expected %d, missing from the system", b.Account, b.Balance))
	}
	return out
}
