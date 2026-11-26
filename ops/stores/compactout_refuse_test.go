package stores

import (
	"strings"
	"testing"
)

// devco:D-1010: a dry run below the org floor prints its plan and the
// refusal the real run would meet, with the command that clears it.
func TestCompactOutDryRunSaysTheRealRunWouldBeRefused(t *testing.T) {
	o := CompactOut{Store: "acme_notes", DryRun: true, Before: "2026-10-03", Squashed: 5, Kept: 3, Rows: 5, ToEpoch: 1,
		WouldRefuse: "org acme's min_lore_version is not set; a compaction needs 0.1.57 or later",
		Raise:       "lore org config set --org acme --key min_lore_version --value 0.1.57 --push --yes"}
	got := o.Text()
	for _, want := range []string{"would squash 5 commit(s)", "the real run would be refused now: org acme's min_lore_version is not set", "first: lore org config set --org acme"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dry run text lacks %q:\n%s", want, got)
		}
	}
	o.WouldRefuse, o.Raise = "", ""
	if got := o.Text(); strings.Contains(got, "would be refused") {
		t.Fatalf("a dry run that would run says no refusal:\n%s", got)
	}
}
