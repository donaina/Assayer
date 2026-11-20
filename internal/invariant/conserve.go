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
func divergentAccounts(m *model.Model, after assay.State) []string {
	var out []string
	for _, b := range after.Sorted() {
		if !m.Knows(b.Account) {
			out = append(out, fmt.Sprintf("%s: unknown to the harness, holds %d", b.Account, b.Balance))
			continue
		}
		if want := m.Balance(b.Account); want != b.Balance {
			out = append(out, fmt.Sprintf("%s: expected %d, got %d", b.Account, want, b.Balance))
		}
	}
	return out
}
