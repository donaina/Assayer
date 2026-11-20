// Package invariant holds the properties every correct money system must have,
// expressed as pure functions of one Observation.
//
// Each checker answers a question with a single right answer, so a disagreement
// is a defect in the system under test rather than a matter of taste. That is
// the whole point of the package: metal, not opinion.
//
// Checkers are pure. A checker that reaches for the network cannot be trusted
// to explain its own failures, and a checker that needs the driver cannot be
// replayed from a history file.
package invariant

import (
	"fmt"
	"sort"

	"github.com/donaina/assayer/pkg/assay"
)

// Finding is one broken property.
type Finding struct {
	// Invariant is the identifier from docs/INVARIANTS.md, e.g. "INV-CONSERVE".
	Invariant string
	// Detail says what failed, in one sentence a reader can act on.
	Detail string
	// Subjects are the accounts, transactions or keys the failure concerns, so
	// a finding points somewhere instead of saying "somewhere in here".
	Subjects []string
}

func (f Finding) String() string {
	s := f.Invariant + ": " + f.Detail
	if len(f.Subjects) > 0 {
		s += fmt.Sprintf(" [%v]", f.Subjects)
	}
	return s
}

// Checker is one named property.
type Checker interface {
	// Name is the invariant identifier, and must match the Finding it emits.
	Name() string
	// Check returns one Finding per distinct violation, or nil.
	Check(assay.Observation) []Finding
}

// All is every checker assayer ships, in report order.
//
// Conservation comes first because when money is missing every other finding is
// downstream of it.
func All() []Checker {
	return []Checker{
		conserve{},
		doublePost{},
		nonneg{},
	}
}

// Run applies every checker and returns the findings in a stable order.
func Run(o assay.Observation) []Finding {
	var out []Finding
	for _, c := range All() {
		out = append(out, c.Check(o)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Invariant != out[j].Invariant {
			return out[i].Invariant < out[j].Invariant
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

// Unsettled returns the idempotency keys the system never gave a final answer
// for, in first-seen order.
//
// A run with unsettled keys is not a passing run, it is an unfinished one: the
// checks that compare against the model simply have less to compare. Reporting
// the count keeps "nothing was wrong" from being confused with "nothing was
// decided".
func Unsettled(o assay.Observation) []string {
	last := make(map[string]assay.Outcome, len(o.Attempts))
	var order []string
	for _, a := range o.Attempts {
		if _, seen := last[a.Intent.Idem]; !seen {
			order = append(order, a.Intent.Idem)
		}
		last[a.Intent.Idem] = a.Result.Outcome
	}
	var out []string
	for _, k := range order {
		switch last[k] {
		case assay.Pending, assay.Unknown:
			out = append(out, k)
		}
	}
	return out
}

// accepted returns the intents the system said it took, once per key.
//
// Retries reuse the idempotency key, so the key — not the attempt — is the unit
// of accounting. Counting attempts would make a retried deposit look like two
// deposits and blame the system for the harness's own repetition.
func accepted(o assay.Observation) []assay.Intent {
	seen := make(map[string]bool, len(o.Attempts))
	var out []assay.Intent
	for _, a := range o.Attempts {
		if a.Result.Outcome != assay.Accepted || seen[a.Intent.Idem] {
			continue
		}
		seen[a.Intent.Idem] = true
		out = append(out, a.Intent)
	}
	return out
}

// acceptedByIDem groups the accepted attempts by idempotency key.
//
// More than one attempt under a key means the system took the same intent more
// than once, which is the failure INV-DOUBLE exists to name.
func acceptedByIDem(o assay.Observation) map[string][]assay.Attempt {
	out := make(map[string][]assay.Attempt)
	for _, a := range o.Attempts {
		if a.Result.Outcome == assay.Accepted {
			out[a.Intent.Idem] = append(out[a.Intent.Idem], a)
		}
	}
	return out
}

// subjects renders account ids for a Finding.
func subjects(accounts ...assay.AccountID) []string {
	out := make([]string, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, string(a))
	}
	return out
}

// idemSubject renders an idempotency key for a Finding.
func idemSubject(idem string) []string { return []string{"idem:" + idem} }
