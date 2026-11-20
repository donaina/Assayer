// Package model is the reference implementation: a ledger so simple it is
// obviously correct, written independently of the driver under test.
//
// Differential testing needs a second opinion that is *not* the code under
// test. The model is that opinion. It is deliberately naive — no locking, no
// network, no database — because its only job is to say what should have
// happened, not to be the kind of thing that has bugs of its own.
package model

import (
	"sort"

	"github.com/donaina/assayer/pkg/assay"
)

// Model tracks the balances and the results a correct implementation would
// have produced for the intents it has been told about.
//
// The rule for *when* the model learns about an intent is the whole trick, and
// it is documented as a decision rather than assumed:
//
//   - Accepted: the model applies the postings and remembers the result.
//   - Rejected: the model remembers the result but moves no money.
//   - Pending or Unknown: the model learns nothing. The intent is not resolved
//     by the answer itself, only by a follow-up Lookup. If no lookup ever
//     resolves it, AGREE simply does not compare it — an unresolved intent is
//     not evidence of a bug, it is evidence of an incomplete conversation.
type Model struct {
	balances map[assay.AccountID]assay.Money
	results  map[string]assay.Result
	postings []assay.Posting
}

// New builds a model at the given starting state.
func New(initial assay.State) *Model {
	m := &Model{
		balances: make(map[assay.AccountID]assay.Money, len(initial.Accounts)),
		results:  make(map[string]assay.Result),
	}
	for a, b := range initial.Accounts {
		m.balances[a] = b
	}
	return m
}

// Apply tells the model what one attempt produced, and whether the model
// should believe it.
//
// The first resolution of an idempotency key is the model's opinion, and later
// answers for the same key do not change it. That asymmetry is the point: if a
// system accepts the same deposit twice, the model still says one deposit
// happened, INV-CONSERVE sees more money than that, and the bug shows up as
// money rather than as a shrug. Making the model as forgetful as the bug would
// make the two agree perfectly and prove nothing.
func (m *Model) Apply(in assay.Intent, res assay.Result) {
	if _, settled := m.results[in.Idem]; settled {
		return
	}
	switch res.Outcome {
	case assay.Accepted:
		m.post(in, res.TxnID)
		m.results[in.Idem] = res
	case assay.Rejected:
		m.results[in.Idem] = res
	default:
		// Pending and Unknown teach the model nothing. See the type comment.
	}
}

// post writes the postings a correct implementation would have written.
func (m *Model) post(in assay.Intent, txnID string) {
	switch in.Op {
	case assay.OpTransfer:
		m.balances[in.From] -= in.Amount
		m.balances[in.To] += in.Amount
		m.postings = append(m.postings,
			assay.Posting{TxnID: txnID, Intent: in.ID, Account: in.From, Delta: -in.Amount},
			assay.Posting{TxnID: txnID, Intent: in.ID, Account: in.To, Delta: in.Amount},
		)
	case assay.OpDeposit:
		m.balances[in.To] += in.Amount
		m.postings = append(m.postings,
			assay.Posting{TxnID: txnID, Intent: in.ID, Account: in.To, Delta: in.Amount},
		)
	}
}

// Result returns what the model believes an idempotency key resolved to.
func (m *Model) Result(idem string) (assay.Result, bool) {
	r, ok := m.results[idem]
	return r, ok
}

// Resolved reports whether the model knows the answer for idem.
func (m *Model) Resolved(idem string) bool {
	_, ok := m.results[idem]
	return ok
}

// Snapshot returns the model's balances.
func (m *Model) Snapshot() assay.State {
	out := assay.State{Accounts: make(map[assay.AccountID]assay.Money, len(m.balances))}
	for a, b := range m.balances {
		out.Accounts[a] = b
	}
	return out
}

// Balance returns one account's balance.
func (m *Model) Balance(a assay.AccountID) assay.Money { return m.balances[a] }

// Knows reports whether the harness opened this account. An account the system
// reports but the model never heard of means a posting invented one, which is a
// different bug from a wrong balance and deserves its own sentence.
func (m *Model) Knows(a assay.AccountID) bool {
	_, ok := m.balances[a]
	return ok
}

// Postings returns the model's journal, ordered by account then transaction.
func (m *Model) Postings() []assay.Posting {
	out := append([]assay.Posting(nil), m.postings...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TxnID != out[j].TxnID {
			return out[i].TxnID < out[j].TxnID
		}
		return out[i].Account < out[j].Account
	})
	return out
}

// Total returns the sum of every balance.
func (m *Model) Total() assay.Money {
	var sum assay.Money
	for _, v := range m.balances {
		sum += v
	}
	return sum
}
