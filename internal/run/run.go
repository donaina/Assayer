// Package run drives a system under test with a generated intent stream and
// records what it saw.
//
// The runner is the only part of assayer that talks to a system under test.
// Everything downstream — invariants, reports, the shrinker — works from the
// Observation this package produces, which is why an invariant can never
// accidentally depend on a live driver and why a failure can be replayed
// without one.
package run

import (
	"context"
	"fmt"

	"github.com/donaina/assayer/internal/spec"
	"github.com/donaina/assayer/pkg/assay"
)

// DefaultLookups is how many times the runner asks about an unresolved intent
// before giving up and leaving it unsettled.
//
// One is enough for a system that settles a status query immediately, and it
// is deliberately not more: the harness should not paper over a system that
// takes three queries to tell the truth. The number of queries needed is
// recorded, and a high count is a finding in its own right.
const DefaultLookups = 1

// Runner drives one system under test.
type Runner struct {
	driver  assay.Driver
	lookups int
}

// New returns a runner for d.
func New(d assay.Driver) *Runner {
	return &Runner{driver: d, lookups: DefaultLookups}
}

// WithLookups sets how many status queries may be spent resolving one
// Pending or Unknown answer. A value below one is ignored.
func (r *Runner) WithLookups(n int) *Runner {
	if n >= 1 {
		r.lookups = n
	}
	return r
}

// Run generates the intent stream for s at seed and drives the SUT with it.
//
// Before and After are both snapshotted, because an invariant is a statement
// about a change, not about a level. Conserving money means the total moved by
// exactly the amount deposited, and that sentence needs two snapshots to mean
// anything.
func (r *Runner) Run(ctx context.Context, s spec.Spec, seed uint64) (assay.Observation, error) {
	gen, err := spec.NewGenerator(s, seed)
	if err != nil {
		return assay.Observation{}, err
	}

	before, err := r.driver.Snapshot(ctx)
	if err != nil {
		return assay.Observation{}, fmt.Errorf("run: snapshot before: %w", err)
	}

	obs := assay.Observation{
		Seed:     seed,
		Scenario: s.Name,
		Before:   before,
	}
	for {
		in, ok := gen.Next()
		if !ok {
			break
		}
		if err := r.attempt(ctx, &obs, in); err != nil {
			return obs, err
		}
	}

	after, err := r.driver.Snapshot(ctx)
	if err != nil {
		return obs, fmt.Errorf("run: snapshot after: %w", err)
	}
	obs.After = after

	postings, err := r.driver.Postings(ctx)
	if err != nil {
		return obs, fmt.Errorf("run: read journal: %w", err)
	}
	obs.Postings = postings
	return obs, nil
}

// attempt submits one intent and, if the answer is ambiguous, asks again.
//
// Every answer is appended as its own Attempt. That means an intent can appear
// in the observation more than once, with a different Via each time, and the
// invariant checkers are written to expect exactly that: an intent's effect is
// established by the last answer that settled it, not by the first.
func (r *Runner) attempt(ctx context.Context, obs *assay.Observation, in assay.Intent) error {
	res, err := r.driver.Apply(ctx, in)
	if err != nil {
		// A transport failure is not a result, so the harness cannot claim
		// one. Recording it as Unknown keeps the intent open, which is what
		// it is, and lets the status query settle it.
		res = assay.Result{Outcome: assay.Unknown, Reason: "transport: " + err.Error()}
	}
	obs.Attempts = append(obs.Attempts, assay.Attempt{
		Seq:    len(obs.Attempts),
		Intent: in,
		Result: res,
		Retry:  0,
		Via:    assay.ViaApply,
	})

	for i := 0; i < r.lookups && !settled(res.Outcome); i++ {
		got, err := r.driver.Lookup(ctx, in.Idem)
		if err != nil {
			// The lookup itself failed: nothing new is known, so the intent
			// stays unresolved and we stop asking rather than spinning.
			obs.Attempts = append(obs.Attempts, assay.Attempt{
				Seq:    len(obs.Attempts),
				Intent: in,
				Result: assay.Result{Outcome: assay.Unknown, Reason: "lookup transport: " + err.Error()},
				Retry:  i + 1,
				Via:    assay.ViaLookup,
			})
			return nil
		}
		res = got
		obs.Attempts = append(obs.Attempts, assay.Attempt{
			Seq:    len(obs.Attempts),
			Intent: in,
			Result: res,
			Retry:  i + 1,
			Via:    assay.ViaLookup,
		})
	}
	return nil
}

// settled reports whether an outcome ends the matter.
//
// Pending and Unknown are both open. Pending sounds more encouraging than
// Unknown and means exactly the same thing to a client: keep asking.
func settled(o assay.Outcome) bool {
	return o == assay.Accepted || o == assay.Rejected
}
