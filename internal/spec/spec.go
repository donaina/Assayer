// Package spec validates a scenario before it runs.
//
// Every mistake a spec can make — a transfer from an account that does not
// exist, a limit below the opening balance, a weight of zero — surfaces at
// load time with a message naming the field. The alternative, discovering it
// eleven thousand operations into a run, is not a way to spend an afternoon.
package spec

import (
	"errors"
	"fmt"
	"time"

	"github.com/donaina/assayer/pkg/assay"
)

// Spec is one scenario.
type Spec struct {
	Name     string
	Seed     uint64
	Ops      int
	Accounts []Account
	Limits   map[assay.AccountID]assay.Money
	Weights  Weights
	Timeout  time.Duration
	Invariants []string
}

// Account is one opening balance.
type Account struct {
	ID      assay.AccountID
	Opening assay.Money
}

// Weights controls the mix of intents a scenario generates. Zero-valued
// weights fall back to DefaultWeights, because a scenario that only ever
// deposits is not a scenario anyone meant to write.
type Weights struct {
	Transfer int
	Deposit  int
	Retry    int
}

// DefaultWeights is the mix used when a spec leaves Weights zero.
var DefaultWeights = Weights{Transfer: 70, Deposit: 20, Retry: 10}

// Resolved returns the weights in use.
func (w Weights) Resolved() Weights {
	if w == (Weights{}) {
		return DefaultWeights
	}
	return w
}

// Total returns the sum of all weights, for validating the mix.
func (w Weights) Total() int { return w.Transfer + w.Deposit + w.Retry }

// Validate returns the first problem with s, or nil.
//
// It collects nothing: a spec with two problems should be reported twice, so
// the caller runs Validate on each field group. Returning one error at a time
// keeps this function a plain description of what a spec must be.
func (s Spec) Validate() error {
	if s.Name == "" {
		return errors.New("spec: name is required")
	}
	if s.Ops < 1 {
		return fmt.Errorf("spec %q: ops must be at least 1, got %d", s.Name, s.Ops)
	}
	if s.Ops > 10_000_000 {
		return fmt.Errorf("spec %q: ops %d is beyond what a history file can hold usefully", s.Name, s.Ops)
	}
	if len(s.Accounts) == 0 {
		return fmt.Errorf("spec %q: at least one account is required", s.Name)
	}
	seen := make(map[assay.AccountID]bool, len(s.Accounts))
	for i, a := range s.Accounts {
		if a.ID == "" {
			return fmt.Errorf("spec %q: account %d has no id", s.Name, i)
		}
		if seen[a.ID] {
			return fmt.Errorf("spec %q: account %q appears twice", s.Name, a.ID)
		}
		seen[a.ID] = true
		if a.Opening < 0 {
			return fmt.Errorf("spec %q: account %q opens negative (%s)", s.Name, a.ID, a.Opening)
		}
	}
	for id, limit := range s.Limits {
		if !seen[id] {
			return fmt.Errorf("spec %q: limit set for unknown account %q", s.Name, id)
		}
		opening := assay.Money(0)
		for _, a := range s.Accounts {
			if a.ID == id {
				opening = a.Opening
			}
		}
		if limit < opening {
			return fmt.Errorf("spec %q: limit %s for account %q is below its opening balance %s",
				s.Name, limit, id, opening)
		}
		if limit < 0 {
			return fmt.Errorf("spec %q: limit for account %q is negative", s.Name, id)
		}
	}
	w := s.Weights.Resolved()
	if w.Total() <= 0 {
		return fmt.Errorf("spec %q: weights sum to zero, so no intent can be generated", s.Name)
	}
	for _, d := range []struct {
		name string
		v    int
	}{{"transfer", w.Transfer}, {"deposit", w.Deposit}, {"retry", w.Retry}} {
		if d.v < 0 {
			return fmt.Errorf("spec %q: %s weight is negative", s.Name, d.name)
		}
	}
	if w.Transfer > 0 && len(s.Accounts) < 2 {
		return fmt.Errorf("spec %q: transfers need at least two accounts", s.Name)
	}
	if w.Deposit == 0 && !mayCreateMoney(s) {
		return fmt.Errorf("spec %q: with no deposits and no external source, INV-CONSERVE cannot be exercised", s.Name)
	}
	if s.Timeout < 0 {
		return fmt.Errorf("spec %q: timeout is negative", s.Name)
	}
	return nil
}

func mayCreateMoney(s Spec) bool { return len(s.Accounts) > 1 }

// IsLiquid reports whether money can enter the system, which is what makes a
// conservation check meaningful rather than a tautology. A scenario with no
// deposits and no outside account can only move money around, so a leak shows
// up as a change in the total — still worth checking, but only if the total is
// free to move.
func (s Spec) IsLiquid() bool { return s.Weights.Resolved().Deposit > 0 }
