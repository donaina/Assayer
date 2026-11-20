// Command assayer drives a payment system with a generated stream of intents
// and reports every invariant it broke.
//
// The command is deliberately thin. It parses flags, builds a driver, runs a
// scenario and prints what happened; every decision worth arguing about lives
// in a package that can be tested without a terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/donaina/assayer/internal/driver/native"
	"github.com/donaina/assayer/internal/history"
	"github.com/donaina/assayer/internal/invariant"
	"github.com/donaina/assayer/internal/run"
	"github.com/donaina/assayer/internal/spec"
	"github.com/donaina/assayer/pkg/assay"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
//
// It defaults to "dev" so that a binary built by `go run` still answers
// --version with something honest rather than an empty string.
var version = "dev"

// Exit codes.
//
// The split between 1 and 2 is the point. A CI job that treats "the harness
// could not run" the same as "the ledger is wrong" will eventually learn to
// ignore both, and the second is the reason this tool exists.
const (
	exitClean    = 0
	exitFindings = 1
	exitError    = 2
)

// check is the invariant pass. It is a variable rather than a direct call so
// that a test can prove the run command refuses to report a verdict when no
// invariants are registered: a run that checked nothing must never look like a
// clean run.
var check func(assay.Observation) []history.Finding = runInvariants

// runInvariants adapts the invariant package's findings to the history record.
func runInvariants(o assay.Observation) []history.Finding {
	found := invariant.Run(o)
	if len(found) == 0 {
		return nil
	}
	out := make([]history.Finding, len(found))
	for i, f := range found {
		out[i] = history.Finding{
			Invariant: f.Invariant,
			Detail:    f.Detail,
			Subjects:  f.Subjects,
		}
	}
	return out
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(Main(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// Main is main without the process exit, so tests can call it.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitError
	}
	switch args[0] {
	case "run":
		return cmdRun(ctx, args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintf(stdout, "assayer %s\n", version)
		return exitClean
	case "help", "-h", "--help":
		usage(stdout)
		return exitClean
	default:
		fmt.Fprintf(stderr, "assayer: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitError
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `assayer — a correctness harness for payment systems

usage:
  assayer run [flags]     drive a scenario and report what broke
  assayer version         print the version
  assayer help            print this message

run flags:
  -scenario name   scenario to run (default "smoke")
  -seed n          seed for the intent stream (default: the scenario's)
  -ops n           override the scenario's operation count
  -lookups n       status queries allowed per unresolved intent
  -history path    write the run's history as JSON to path
  -quiet           print only the verdict

exit codes:
  0  every invariant held
  1  the system under test broke an invariant
  2  assayer could not run

scenarios:
`)
	for _, n := range scenarioNames() {
		fmt.Fprintf(w, "  %-10s %s\n", n, scenarios[n].Desc)
	}
}

func cmdRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("assayer run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(stderr) }

	var (
		name     = fs.String("scenario", "smoke", "scenario to run")
		seed     = fs.Uint64("seed", 0, "seed for the intent stream")
		ops      = fs.Int("ops", 0, "override the scenario's operation count")
		lookups  = fs.Int("lookups", run.DefaultLookups, "status queries allowed per unresolved intent")
		histPath = fs.String("history", "", "write the run's history as JSON to this path")
		quiet    = fs.Bool("quiet", false, "print only the verdict")
	)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "assayer run: unexpected argument %q\n", fs.Arg(0))
		return exitError
	}

	// Which flags the caller actually set, as opposed to which happen to hold
	// their default. Without this, "-ops 0" would be indistinguishable from
	// omitting -ops and would silently run the scenario's own count.
	given := make(map[string]bool, fs.NFlag())
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	sc, ok := scenarios[*name]
	if !ok {
		fmt.Fprintf(stderr, "assayer run: unknown scenario %q\n", *name)
		fmt.Fprintf(stderr, "known scenarios: %s\n", strings.Join(scenarioNames(), ", "))
		return exitError
	}

	s := sc.Spec
	if given["ops"] {
		if *ops < 1 {
			fmt.Fprintf(stderr, "assayer run: -ops must be at least 1, got %d\n", *ops)
			return exitError
		}
		s.Ops = *ops
	}
	if given["seed"] {
		s.Seed = *seed
	}
	if *lookups < 0 {
		fmt.Fprintf(stderr, "assayer run: -lookups must not be negative, got %d\n", *lookups)
		return exitError
	}
	if err := s.Validate(); err != nil {
		fmt.Fprintf(stderr, "assayer run: %v\n", err)
		return exitError
	}

	openings := make(map[assay.AccountID]assay.Money, len(s.Accounts))
	for _, a := range s.Accounts {
		openings[a.ID] = a.Opening
	}

	obs, err := run.New(native.New(openings)).WithLookups(*lookups).Run(ctx, s, s.Seed)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "assayer run: interrupted")
			return exitError
		}
		fmt.Fprintf(stderr, "assayer run: %v\n", err)
		return exitError
	}

	if check == nil {
		fmt.Fprintln(stderr, "assayer run: no invariants are registered in this build, so this run proves nothing about correctness")
		return exitError
	}

	rec := history.FromObservation(obs)
	rec.Driver = "native"
	rec.Findings = check(obs)

	if *histPath != "" {
		if err := writeHistory(*histPath, rec); err != nil {
			fmt.Fprintf(stderr, "assayer run: %v\n", err)
			return exitError
		}
	}

	return report(stdout, s, obs, rec.Findings, *quiet)
}

// writeHistory writes the record to path through a temporary file in the same
// directory and a rename, so that a run killed mid-write leaves either the old
// history or the new one and never half of either.
func writeHistory(path string, rec history.Record) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".assayer-history-*")
	if err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once the rename below succeeds

	if err := rec.Write(f); err != nil {
		f.Close()
		return fmt.Errorf("write history: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

// tally is the per-intent outcome count. It is per intent rather than per
// attempt because a lookup is not a second intent, and a report that counted
// them as one would overstate how much work the system actually did.
type tally struct {
	accepted, rejected, pending, unknown int
}

func (t tally) total() int { return t.accepted + t.rejected + t.pending + t.unknown }

// tallyOutcomes folds the attempt log down to one outcome per intent: the last
// answer recorded for it. The runner stops querying an intent once it settles,
// so the last answer is either the one that settled it or the final Unknown.
func tallyOutcomes(attempts []assay.Attempt) tally {
	final := make(map[string]assay.Outcome, len(attempts))
	order := make([]string, 0, len(attempts))
	for _, a := range attempts {
		id := string(a.Intent.ID)
		if _, seen := final[id]; !seen {
			order = append(order, id)
		}
		final[id] = a.Result.Outcome
	}

	var t tally
	for _, id := range order {
		switch final[id] {
		case assay.Accepted:
			t.accepted++
		case assay.Rejected:
			t.rejected++
		case assay.Pending:
			t.pending++
		default:
			t.unknown++
		}
	}
	return t
}

func report(w io.Writer, s spec.Spec, obs assay.Observation, findings []history.Finding, quiet bool) int {
	t := tallyOutcomes(obs.Attempts)

	if !quiet {
		fmt.Fprintf(w, "assayer %s — %s, seed %d, %d ops, driver native\n\n", version, s.Name, s.Seed, s.Ops)
		fmt.Fprintf(w, "  intents   %d  (accepted %d, rejected %d, pending %d, unknown %d)\n",
			t.total(), t.accepted, t.rejected, t.pending, t.unknown)
		fmt.Fprintf(w, "  postings  %d\n", len(obs.Postings))
		fmt.Fprintf(w, "  total     %s → %s\n\n", obs.Before.Total(), obs.After.Total())
	}

	if len(findings) == 0 {
		fmt.Fprintf(w, "clean: no invariant violations over %d intents\n", t.total())
		return exitClean
	}

	fmt.Fprintf(w, "%d invariant violation(s):\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(w, "  %s  %s\n", f.Invariant, f.Detail)
	}
	return exitFindings
}
