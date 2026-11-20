package invariant_test

import (
	"strings"
	"testing"

	"github.com/donaina/assayer/internal/invariant"
	"github.com/donaina/assayer/pkg/assay"
)

// TestAConserveFindingNamesTheAccountThatVanished pins the shape of the finding
// rather than only its existence.
//
// "expected 300, found 100" with no account attached is the finding a reader
// dismisses, because there is nowhere to look next. The account is derivable —
// it is in the model's snapshot — so the checker has to say so.
func TestAConserveFindingNamesTheAccountThatVanished(t *testing.T) {
	o := assay.Observation{
		Seed:     1,
		Scenario: "vanishing-account",
		Before:   assay.State{Accounts: map[assay.AccountID]assay.Money{"A": 100, "B": 200}},
		// B disappeared, and its money with it.
		After: assay.State{Accounts: map[assay.AccountID]assay.Money{"A": 100}},
	}

	var conserve *invariant.Finding
	for _, f := range invariant.Run(o) {
		if f.Invariant == "INV-CONSERVE" {
			f := f
			conserve = &f
			break
		}
	}
	if conserve == nil {
		t.Fatal("an account vanished with its balance and INV-CONSERVE said nothing")
	}
	if len(conserve.Subjects) == 0 {
		t.Fatalf("INV-CONSERVE fired with no account attached: %s", conserve)
	}
	if !strings.Contains(conserve.Subjects[0], "B") {
		t.Errorf("the finding names %v, which does not point at the account that vanished (B)", conserve.Subjects)
	}
}

// TestAnUnreportedZeroBalanceAccountIsNotAFinding is the other half of the rule
// above, and the reason for the "non-zero" condition in divergentAccounts.
//
// Plenty of systems decline to return accounts that hold nothing. Nothing is
// missing from a total of zero, so treating that silence as a disappearance
// would manufacture a violation on almost every real integration — and a
// harness that cries wolf gets switched off.
func TestAnUnreportedZeroBalanceAccountIsNotAFinding(t *testing.T) {
	o := assay.Observation{
		Seed:     2,
		Scenario: "quiet-zero-balance",
		Before:   assay.State{Accounts: map[assay.AccountID]assay.Money{"A": 100, "C": 0}},
		// C holds nothing and the system does not bother reporting it.
		After: assay.State{Accounts: map[assay.AccountID]assay.Money{"A": 100}},
	}

	if findings := invariant.Run(o); len(findings) != 0 {
		t.Fatalf("a system that omits zero-balance accounts was accused of %d violation(s): %v", len(findings), findings)
	}
}
