package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaina/assayer/internal/history"
	"github.com/donaina/assayer/internal/spec"
	"github.com/donaina/assayer/pkg/assay"
)

// runMain invokes the CLI the way the shell would and returns what a caller
// would see: the exit code and both streams.
func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Main(t.Context(), args, &out, &errb)
	return code, out.String(), errb.String()
}

// swapCheck replaces the invariant pass for the duration of a test and returns
// a function that puts the real one back.
func swapCheck(fn func(assay.Observation) []history.Finding) (restore func()) {
	prev := check
	check = fn
	return func() { check = prev }
}

func TestVersionDefaultsToDev(t *testing.T) {
	// The release build sets this with -ldflags -X. A build that forgets to
	// must not claim a version it is not.
	if version != "dev" {
		t.Fatalf("version = %q, want %q", version, "dev")
	}
}

func TestVersionCommandPrintsTheVersion(t *testing.T) {
	code, stdout, stderr := runMain(t, "--version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "assayer dev"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestVersionIsAnsweredBeforeAnyOtherWork(t *testing.T) {
	// --version must not depend on a scenario, a driver, or a flag that only
	// makes sense for a run. Asking for the version is never a request to
	// start one.
	code, stdout, _ := runMain(t, "--version", "run", "-scenario", "nope")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "assayer dev") {
		t.Fatalf("stdout = %q, want it to contain the version", stdout)
	}
}

func TestUnknownCommandIsAnError(t *testing.T) {
	code, _, stderr := runMain(t, "frobnicate")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("stderr = %q, want it to name the unknown command", stderr)
	}
}

func TestNoCommandPrintsUsage(t *testing.T) {
	code, _, stderr := runMain(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("stderr = %q, want usage", stderr)
	}
}

func TestRunRefusesToClaimACleanRunWithNoInvariants(t *testing.T) {
	// This is the single most important behaviour in the binary. A harness
	// that checks nothing and exits zero is worse than no harness: it is a
	// green tick that means nothing, and someone will trust it.
	restore := swapCheck(nil)
	defer restore()

	code, _, stderr := runMain(t, "run", "-scenario", "smoke")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "proves nothing") {
		t.Fatalf("stderr = %q, want it to say the run proves nothing", stderr)
	}
}

func TestRunRejectsAnUnknownScenario(t *testing.T) {
	code, _, stderr := runMain(t, "run", "-scenario", "nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown scenario") {
		t.Fatalf("stderr = %q, want it to name the unknown scenario", stderr)
	}
}

func TestRunRejectsZeroOps(t *testing.T) {
	// -ops 0 is a request to run nothing. Silently falling back to the
	// scenario's own count would make a typo look like a full run.
	code, _, stderr := runMain(t, "run", "-ops", "0")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "-ops") {
		t.Fatalf("stderr = %q, want it to name -ops", stderr)
	}
}

func TestRunRejectsNegativeLookups(t *testing.T) {
	code, _, stderr := runMain(t, "run", "-lookups", "-1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "-lookups") {
		t.Fatalf("stderr = %q, want it to name -lookups", stderr)
	}
}

func TestTallyCountsOneOutcomePerIntent(t *testing.T) {
	// A retry is the same intent asked twice, not two intents. Counting
	// attempts would report twice the work the system was actually asked to
	// do, and would make a retried rejection look like two rejections.
	attempts := []assay.Attempt{
		{Seq: 0, Intent: assay.Intent{ID: "i1"}, Result: assay.Result{Outcome: assay.Rejected}},
		{Seq: 1, Intent: assay.Intent{ID: "i1"}, Result: assay.Result{Outcome: assay.Accepted}, Retry: 1},
		{Seq: 2, Intent: assay.Intent{ID: "i2"}, Result: assay.Result{Outcome: assay.Accepted}},
	}
	got := tallyOutcomes(attempts)
	if got.accepted != 2 || got.rejected != 0 {
		t.Fatalf("tally = %+v, want 2 accepted and 0 rejected", got)
	}
	if got.total() != 2 {
		t.Fatalf("total = %d, want 2 intents", got.total())
	}
}

func TestTallyKeepsTheLastAnswerForAnIntent(t *testing.T) {
	// Pending is not an answer, it is a promise of one. Once a lookup settles
	// the intent, the settled answer is the one that counts.
	attempts := []assay.Attempt{
		{Seq: 0, Intent: assay.Intent{ID: "i1"}, Result: assay.Result{Outcome: assay.Pending}},
		{Seq: 1, Intent: assay.Intent{ID: "i1"}, Result: assay.Result{Outcome: assay.Accepted}, Retry: 1},
	}
	got := tallyOutcomes(attempts)
	if got.accepted != 1 || got.pending != 0 {
		t.Fatalf("tally = %+v, want 1 accepted and 0 pending", got)
	}
}

func TestReportExitsOneWhenThereAreFindings(t *testing.T) {
	// Exit 1 is reserved for "the system under test is wrong". It must not be
	// reachable from a harness bug, and it must not be swallowed.
	var out bytes.Buffer
	code := report(&out, spec.Spec{Name: "smoke"}, cleanObservation(), []history.Finding{
		{Invariant: "INV-CONSERVE", Detail: "total moved by 500 without a deposit"},
	}, false)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "INV-CONSERVE") {
		t.Fatalf("stdout = %q, want it to name the violated invariant", out.String())
	}
}

func TestReportExitsZeroWhenClean(t *testing.T) {
	var out bytes.Buffer
	code := report(&out, spec.Spec{Name: "smoke"}, cleanObservation(), nil, false)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "clean") {
		t.Fatalf("stdout = %q, want it to say the run was clean", out.String())
	}
}

func TestWriteHistoryProducesReadableJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	rec := history.FromObservation(cleanObservation())
	rec.Scenario = "smoke"
	rec.Driver = "native"
	if err := writeHistory(path, rec); err != nil {
		t.Fatalf("writeHistory: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	head := readHeader(t, raw)
	if head.Scenario != "smoke" {
		t.Fatalf("scenario = %q, want %q", head.Scenario, "smoke")
	}
	if head.Seed != rec.Seed {
		t.Fatalf("seed = %d, want %d", head.Seed, rec.Seed)
	}
	if head.Driver != "native" {
		t.Fatalf("driver = %q, want %q", head.Driver, "native")
	}
}

func TestWriteHistoryWritesOneLinePerRecord(t *testing.T) {
	// The history is JSONL: a header, then one line per attempt, posting and
	// finding. A reader that streams it must be able to stop at any line, so
	// the count has to be exact.
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	rec := history.FromObservation(cleanObservation())
	rec.Findings = []history.Finding{{Invariant: "INV-CONSERVE", Detail: "x"}}
	if err := writeHistory(path, rec); err != nil {
		t.Fatalf("writeHistory: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	lines := nonEmptyLines(raw)
	want := 1 + len(rec.Attempts) + len(rec.Postings) + len(rec.Findings)
	if len(lines) != want {
		t.Fatalf("history has %d lines, want %d", len(lines), want)
	}
	for i, line := range lines {
		var probe map[string]any
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
	}
}

// readHeader decodes the first line of a history file.
func readHeader(t *testing.T, raw []byte) history.Record {
	t.Helper()
	lines := nonEmptyLines(raw)
	if len(lines) == 0 {
		t.Fatal("history file is empty")
	}
	var head history.Record
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		t.Fatalf("header is not valid JSON: %v", err)
	}
	return head
}

func nonEmptyLines(raw []byte) []string {
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestWriteHistoryReplacesAnExistingFile(t *testing.T) {
	// The write goes through a rename, so a second run must replace the first
	// rather than append to it or fail on it.
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	first := history.FromObservation(cleanObservation())
	first.Scenario = "first"
	if err := writeHistory(path, first); err != nil {
		t.Fatalf("first write: %v", err)
	}

	second := history.FromObservation(cleanObservation())
	second.Scenario = "second"
	if err := writeHistory(path, second); err != nil {
		t.Fatalf("second write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := readHeader(t, raw).Scenario; got != "second" {
		t.Fatalf("scenario = %q, want the second write to win", got)
	}
}

func TestWriteHistoryLeavesNoTemporaryFilesBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	if err := writeHistory(path, history.FromObservation(cleanObservation())); err != nil {
		t.Fatalf("writeHistory: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory holds %v, want only the history file", names)
	}
}

// cleanObservation is a minimal run that no invariant can object to: one
// deposit, one account, and a total that grew by exactly the deposit.
func cleanObservation() assay.Observation {
	return assay.Observation{
		Seed:     1,
		Scenario: "smoke",
		Before:   assay.State{Accounts: map[assay.AccountID]assay.Money{"alice": 100}},
		After:    assay.State{Accounts: map[assay.AccountID]assay.Money{"alice": 150}},
		Attempts: []assay.Attempt{
			{
				Seq:    0,
				Intent: assay.Intent{ID: "i1", Op: assay.OpDeposit, To: "alice", Amount: 50, Idem: "k1"},
				Result: assay.Result{Outcome: assay.Accepted, TxnID: "t1"},
			},
		},
		Postings: []assay.Posting{
			{TxnID: "t1", Intent: "i1", Account: "alice", Delta: 50},
		},
	}
}
