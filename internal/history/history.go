// Package history records a run in a form that can be compared byte for byte,
// shipped in a bug report, and replayed months later.
//
// The format is line-oriented JSON with sorted keys and no timestamps. That is
// a deliberate, boring choice: a history that changes shape is a history that
// cannot be diffed between two runs, and diffing two runs is most of the point.
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/donaina/assayer/pkg/assay"
)

// Version is the history format version.
const Version = 1

// Record is one complete run.
type Record struct {
	Version  int                    `json:"version"`
	Seed     uint64                 `json:"seed"`
	Scenario string                 `json:"scenario"`
	Driver   string                 `json:"driver"`
	Before   []assay.AccountBalance `json:"before"`
	After    []assay.AccountBalance `json:"after"`
	Attempts []Attempt              `json:"attempts"`
	Postings []Posting              `json:"postings,omitempty"`
	Findings []Finding              `json:"findings,omitempty"`
}

// Attempt is one client request and the answer it got.
type Attempt struct {
	Seq     int    `json:"seq"`
	Retry   int    `json:"retry"`
	ID      string `json:"id"`
	Op      string `json:"op"`
	From    string `json:"from,omitempty"`
	To      string `json:"to"`
	Amount  string `json:"amount"`
	Idem    string `json:"idem"`
	Outcome string `json:"outcome"`
	TxnID   string `json:"txn,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Via     string `json:"via"`
}

// Posting is one side of one committed transaction.
type Posting struct {
	TxnID   string `json:"txn"`
	Intent  string `json:"intent"`
	Account string `json:"account"`
	Delta   string `json:"delta"`
}

// Finding is one invariant violation, as recorded.
type Finding struct {
	Invariant string   `json:"invariant"`
	Detail    string   `json:"detail"`
	Subjects  []string `json:"subjects,omitempty"`
}

// FromObservation converts a run into a record.
//
// Values are rendered as decimal strings rather than JSON numbers: money is a
// 64-bit integer of minor units, and JavaScript's numbers are not. A history
// should be readable in any language without silently losing the last digit,
// which is the one digit that matters.
func FromObservation(o assay.Observation) Record {
	r := Record{
		Version:  Version,
		Seed:     o.Seed,
		Scenario: o.Scenario,
		Before:   nonNil(o.Before.Sorted()),
		After:    nonNil(o.After.Sorted()),
	}
	for _, a := range o.Attempts {
		r.Attempts = append(r.Attempts, Attempt{
			Seq:     a.Seq,
			Retry:   a.Retry,
			ID:      a.Intent.ID,
			Op:      string(a.Intent.Op),
			From:    string(a.Intent.From),
			To:      string(a.Intent.To),
			Amount:  a.Intent.Amount.String(),
			Idem:    a.Intent.Idem,
			Outcome: string(a.Result.Outcome),
			TxnID:   a.Result.TxnID,
			Reason:  a.Result.Reason,
			Via:     string(a.Via),
		})
	}
	for _, p := range o.Postings {
		r.Postings = append(r.Postings, Posting{
			TxnID:   p.TxnID,
			Intent:  p.Intent,
			Account: string(p.Account),
			Delta:   p.Delta.String(),
		})
	}
	sort.SliceStable(r.Postings, func(i, j int) bool {
		if r.Postings[i].TxnID != r.Postings[j].TxnID {
			return r.Postings[i].TxnID < r.Postings[j].TxnID
		}
		return r.Postings[i].Account < r.Postings[j].Account
	})
	return r
}

func nonNil(in []assay.AccountBalance) []assay.AccountBalance {
	if in == nil {
		return []assay.AccountBalance{}
	}
	return in
}

// Write emits the record as line-oriented JSON: one attempt per line, so a
// 10,000-operation history diffs sensibly in a terminal.
func (r Record) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)

	head := r
	head.Attempts = nil
	head.Postings = nil
	head.Findings = nil
	if err := enc.Encode(head); err != nil {
		return fmt.Errorf("history: header: %w", err)
	}
	for _, a := range r.Attempts {
		if err := enc.Encode(struct {
			Attempt
			Kind string `json:"kind"`
		}{a, "attempt"}); err != nil {
			return fmt.Errorf("history: attempt %d: %w", a.Seq, err)
		}
	}
	for _, p := range r.Postings {
		if err := enc.Encode(struct {
			Posting
			Kind string `json:"kind"`
		}{p, "posting"}); err != nil {
			return fmt.Errorf("history: posting %s: %w", p.TxnID, err)
		}
	}
	for _, f := range r.Findings {
		if err := enc.Encode(struct {
			Finding
			Kind string `json:"kind"`
		}{f, "finding"}); err != nil {
			return fmt.Errorf("history: finding %s: %w", f.Invariant, err)
		}
	}
	return bw.Flush()
}

// Bytes returns the canonical serialisation, which is what tests compare.
func (r Record) Bytes() []byte {
	var buf bytes.Buffer
	if err := r.Write(&buf); err != nil {
		// Write only fails on an io.Writer that cannot fail (bytes.Buffer).
		panic("history: unreachable write error: " + err.Error())
	}
	return buf.Bytes()
}
