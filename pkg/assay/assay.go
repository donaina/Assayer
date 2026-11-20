// Package assay is the contract between the harness and a system under test.
//
// It imports nothing from assayer's internals, so a third party can implement
// Driver for their own payment system without vendoring the harness. That
// constraint is the reason this package exists separately from everything
// else: the moment a Driver needs an internal type, the tool stops being
// something other people can point at their own code.
package assay

import (
	"context"
	"sort"
)

// AccountID identifies an account inside the system under test.
type AccountID string

// Op is the kind of intent a client submits.
type Op string

const (
	// OpTransfer moves money between two accounts inside the SUT.
	OpTransfer Op = "transfer"

	// OpDeposit is a declared external inflow. It is the only way money
	// enters the system, which is precisely what makes INV-CONSERVE
	// checkable: every unit of growth must be attributable to a deposit
	// the harness itself asked for.
	OpDeposit Op = "deposit"
)

// Intent is one request a client makes of the system under test.
type Intent struct {
	ID     string    // client-side identifier, unique within a run
	Op     Op        //
	From   AccountID // empty for OpDeposit
	To     AccountID //
	Amount Money     //
	Idem   string    // idempotency key; a retry reuses it
}

// Outcome is what the SUT said.
//
// Pending and Unknown are not failures. They are the two answers that make a
// client ask again, and a payment system that cannot produce them is not
// being honest about the network it lives on.
type Outcome string

const (
	Accepted Outcome = "accepted"
	Rejected Outcome = "rejected"
	Pending  Outcome = "pending"
	Unknown  Outcome = "unknown"
)

// Result is the SUT's answer to one intent.
type Result struct {
	Outcome Outcome
	TxnID   string
	Reason  string
}

// Posting is one side of one committed transaction.
type Posting struct {
	TxnID   string
	Intent  string
	Account AccountID
	Delta   Money
}

// State is a snapshot of balances.
type State struct {
	Accounts map[AccountID]Money
}

// Balance returns the balance of a, or zero if a is absent.
func (s State) Balance(a AccountID) Money { return s.Accounts[a] }

// Total returns the sum of every balance in the snapshot.
func (s State) Total() Money {
	var sum Money
	for _, v := range s.Accounts {
		sum += v
	}
	return sum
}

// AccountBalance is one account's balance, for ordered serialisation.
type AccountBalance struct {
	Account AccountID `json:"account"`
	Balance Money     `json:"balance"`
}

// Sorted returns the snapshot ordered by account ID.
//
// Map iteration order in Go is deliberately random, so anything that reaches
// a history file or a report must go through here first. A history that
// differs between two runs of the same seed is not a history.
func (s State) Sorted() []AccountBalance {
	out := make([]AccountBalance, 0, len(s.Accounts))
	for a, b := range s.Accounts {
		out = append(out, AccountBalance{Account: a, Balance: b})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}

// Via names the operation that produced a result.
//
// A payment client has two ways to learn an outcome — submit, or ask about an
// idempotency key it already submitted — and the harness records both as
// attempts because both can carry the resolution of an intent. Collapsing them
// would make "how often did we have to ask again" unanswerable, and that
// number is the honest measure of how well a system handles ambiguity.
type Via string

const (
	ViaApply  Via = "apply"
	ViaLookup Via = "lookup"
)

// Attempt pairs an intent with the answer it got.
type Attempt struct {
	Seq    int    // position in the run, from zero
	Intent Intent //
	Result Result //
	Retry  int    // 0 for the first attempt, 1 for the first retry, ...
	Via    Via    //
}

// Observation is everything one run learned, handed to the invariant checkers.
//
// Checkers are pure functions of this value. A checker that reaches for the
// network cannot be trusted to explain its own failures, and a checker that
// needs the driver cannot be replayed from a history file.
type Observation struct {
	Seed     uint64
	Scenario string
	Before   State
	After    State
	Attempts []Attempt
	Postings []Posting

	// States is every balance reading taken during the run, in order, with
	// Before as the first element and After as the last.
	//
	// It exists because an invariant that says "never" cannot be checked at
	// the end. An account that went negative and was then covered by an
	// incoming credit ends the run looking healthy, and that is precisely the
	// incident shape that costs money: the bank was exposed for a window and
	// the closing balance hides it. A checker that only reads After is
	// checking a postcondition, not an invariant.
	//
	// The cost is one extra read per attempt. That is free for an in-process
	// driver and one request per operation for an HTTP one, which is the right
	// trade for a correctness run. Load runs measure throughput and do not
	// check invariants per operation, so they do not pay it.
	States []State
}

// Readings returns the states to check a "never" invariant against.
//
// Hand-built observations in tests often carry only After, so an empty States
// falls back to it rather than silently checking nothing — a checker that
// passes because it had no input is worse than one that fails.
func (o Observation) Readings() []State {
	if len(o.States) > 0 {
		return o.States
	}
	if o.After.Accounts == nil {
		return nil
	}
	return []State{o.After}
}

// Accepted returns the intents the SUT said it accepted, in order.
func (o Observation) Accepted() []Intent {
	var out []Intent
	for _, a := range o.Attempts {
		if a.Result.Outcome == Accepted {
			out = append(out, a.Intent)
		}
	}
	return out
}

// Driver is the system under test.
//
// The four methods are deliberately the minimum an HTTP service can offer:
// submit, look up, read balances, read the journal. A driver that needs a
// fifth method is usually hiding a leaky abstraction, and a leaky abstraction
// is usually where the bug lives.
type Driver interface {
	// Name identifies the driver in reports.
	Name() string

	// Apply submits one intent. A returned error means the harness could
	// not talk to the SUT; a Result with Outcome Unknown means the SUT
	// could not tell us what happened. Those are different things and the
	// harness treats them differently.
	Apply(ctx context.Context, in Intent) (Result, error)

	// Lookup resolves an idempotency key to the result it produced, which
	// is how a client settles a Pending or Unknown answer.
	Lookup(ctx context.Context, idem string) (Result, error)

	// Snapshot reads every balance.
	Snapshot(ctx context.Context) (State, error)

	// Postings reads the journal: every committed transaction, both sides.
	Postings(ctx context.Context) ([]Posting, error)
}
