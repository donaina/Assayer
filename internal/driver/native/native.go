// Package native is the reference ledger.
//
// It is the system under test that assayer is most certain about: an in-memory
// double-entry ledger small enough to reason about by hand. Its job is not to
// be impressive. Its job is to be correct, because every invariant in the
// suite is calibrated against it — if the reference ledger fails an invariant,
// the invariant is wrong, not the ledger. That makes it the anchor for the
// false-positive suite, and the reason a defect injected into it is a fair
// test of the detectors.
package native

import (
	"context"
	"fmt"
	"sync"

	"github.com/donaina/assayer/pkg/assay"
)

// Ledger is a correct, in-memory, double-entry ledger.
//
// All state sits behind one mutex. That is deliberate: concurrency is not what
// this driver is for. It exists so that a run of the harness can be replayed
// without a database, a network, or a clock, which is what makes a failure
// reproducible on a laptop months later.
type Ledger struct {
	mu       sync.Mutex
	balances map[assay.AccountID]assay.Money
	journal  []assay.Posting
	idem     map[string]assay.Result
	nextTxn  int
}

// New returns a ledger whose accounts open at the given balances.
//
// Opening balances arrive through this constructor rather than as deposits
// because they are part of the before-state: money the system already held
// when the run began. Counting them as deposits would make every run look like
// it had conserved nothing.
func New(openings map[assay.AccountID]assay.Money) *Ledger {
	balances := make(map[assay.AccountID]assay.Money, len(openings))
	for a, b := range openings {
		balances[a] = b
	}
	return &Ledger{
		balances: balances,
		idem:     make(map[string]assay.Result),
	}
}

// Name implements assay.Driver.
func (l *Ledger) Name() string { return "native" }

// Apply implements assay.Driver.
//
// The order of checks matters and is not arbitrary. Idempotency comes first,
// because a repeated key must return the original answer whether or not that
// answer was a rejection — a client that retried a failed transfer and got
// success the second time has been lied to. Validation comes next, and the
// debit is only posted after every check has passed, so there is no window in
// which a half-transfer exists for an invariant to find.
func (l *Ledger) Apply(_ context.Context, in assay.Intent) (assay.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if prev, ok := l.idem[in.Idem]; ok {
		return prev, nil
	}

	res := l.check(in)
	if res.Outcome == assay.Accepted {
		res.TxnID = fmt.Sprintf("txn-%06d", l.nextTxn)
		l.nextTxn++
		l.post(res.TxnID, in)
	}
	// Every outcome is remembered under the key, including rejections, so a
	// retry is answered from the record rather than re-decided.
	l.idem[in.Idem] = res
	return res, nil
}

// check decides whether in is legal against current balances.
func (l *Ledger) check(in assay.Intent) assay.Result {
	if in.Amount <= 0 {
		return assay.Result{Outcome: assay.Rejected, Reason: "amount must be positive"}
	}
	switch in.Op {
	case assay.OpTransfer:
		if in.From == in.To {
			return assay.Result{Outcome: assay.Rejected, Reason: "self transfer"}
		}
		if _, ok := l.balances[in.From]; !ok {
			return assay.Result{Outcome: assay.Rejected, Reason: "unknown source account"}
		}
		if _, ok := l.balances[in.To]; !ok {
			return assay.Result{Outcome: assay.Rejected, Reason: "unknown destination account"}
		}
		if l.balances[in.From] < in.Amount {
			return assay.Result{Outcome: assay.Rejected, Reason: "insufficient funds"}
		}
	case assay.OpDeposit:
		if _, ok := l.balances[in.To]; !ok {
			return assay.Result{Outcome: assay.Rejected, Reason: "unknown destination account"}
		}
	default:
		return assay.Result{Outcome: assay.Rejected, Reason: "unknown operation"}
	}
	return assay.Result{Outcome: assay.Accepted}
}

// post writes both sides of a committed transaction.
func (l *Ledger) post(txnID string, in assay.Intent) {
	switch in.Op {
	case assay.OpTransfer:
		l.balances[in.From] -= in.Amount
		l.balances[in.To] += in.Amount
		l.journal = append(l.journal,
			assay.Posting{TxnID: txnID, Intent: in.ID, Account: in.From, Delta: -in.Amount},
			assay.Posting{TxnID: txnID, Intent: in.ID, Account: in.To, Delta: in.Amount},
		)
	case assay.OpDeposit:
		l.balances[in.To] += in.Amount
		l.journal = append(l.journal,
			assay.Posting{TxnID: txnID, Intent: in.ID, Account: in.To, Delta: in.Amount},
		)
	}
}

// Lookup implements assay.Driver.
//
// A key with no record answers Rejected rather than Unknown. The distinction
// is the whole of the transactional-status-query rule: "unknown" means the
// system is unsure, while "rejected" means it is sure the intent had no
// effect. A system that cannot find a key has not applied it, so the honest
// answer is that it did not happen, and telling a client otherwise would leave
// the intent permanently unsettled.
func (l *Ledger) Lookup(_ context.Context, idem string) (assay.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if res, ok := l.idem[idem]; ok {
		return res, nil
	}
	return assay.Result{Outcome: assay.Rejected, Reason: "not found"}, nil
}

// Snapshot implements assay.Driver.
func (l *Ledger) Snapshot(_ context.Context) (assay.State, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make(map[assay.AccountID]assay.Money, len(l.balances))
	for a, b := range l.balances {
		out[a] = b
	}
	return assay.State{Accounts: out}, nil
}

// Postings implements assay.Driver.
func (l *Ledger) Postings(_ context.Context) ([]assay.Posting, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]assay.Posting, len(l.journal))
	copy(out, l.journal)
	return out, nil
}

// BreakTrialBalance flips a random credit to a debit, producing a ledger whose
// accounts no longer justify its journal. It exists so the defect corpus can
// be tested against a ledger that is wrong in exactly one way.
func (l *Ledger) BreakTrialBalance() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.journal) == 0 {
		return
	}
	// Reverse the sign of the last posting. The balances stay as they were, so
	// the sum of the journal stops matching the sum of the balances: precisely
	// the failure a trial balance is supposed to catch.
	l.journal[len(l.journal)-1].Delta = -l.journal[len(l.journal)-1].Delta
}
