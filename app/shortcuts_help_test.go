package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestShowFlags_GroupJSONPlaceholder: a group that forwards to its default
// verb (`lore doctor` → doctor run) lists the verb's visible flags in its
// --help, --json included, although the group already holds a hidden
// --json placeholder of its own (surface/cli acceptJSON). A flag the verb
// hides stays hidden, and a flag the group shows is left as it is.
func TestShowFlags_GroupJSONPlaceholder(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		groupHas, groupHidden bool
		verbHidden            bool
		wantListed            bool
	}{
		{name: "group has no such flag", wantListed: true},
		{name: "group hides a placeholder", groupHas: true, groupHidden: true, wantListed: true},
		{name: "verb hides it, group placeholder", groupHas: true, groupHidden: true, verbHidden: true},
		{name: "verb hides it, group has none", verbHidden: true},
		{name: "group shows its own", groupHas: true, wantListed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "lore"}
			g := &cobra.Command{Use: "w"}
			if tc.groupHas {
				_ = g.Flags().Bool("json", false, "group usage")
				if tc.groupHidden {
					_ = g.Flags().MarkHidden("json")
				}
			}
			run := &cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }}
			_ = run.Flags().Bool("json", false, "print the result (or the error) as JSON")
			_ = run.Flags().Bool("fresh", false, "probe again")
			if tc.verbHidden {
				_ = run.Flags().MarkHidden("json")
			}
			g.AddCommand(run)
			root.AddCommand(g)
			if defaultVerb(root, "w", "run") == nil {
				t.Fatal("defaultVerb found no run verb")
			}
			var out bytes.Buffer
			g.SetOut(&out)
			if err := g.Help(); err != nil {
				t.Fatal(err)
			}
			h := out.String()
			if !strings.Contains(h, "--fresh") {
				t.Fatalf("help lacks the verb's --fresh:\n%s", h)
			}
			if got := strings.Contains(h, "--json"); got != tc.wantListed {
				t.Fatalf("--json listed = %v, want %v:\n%s", got, tc.wantListed, h)
			}
			if tc.wantListed && tc.groupHidden && !strings.Contains(h, "print the result (or the error) as JSON") {
				t.Errorf("the placeholder kept its own usage, not the verb's:\n%s", h)
			}
		})
	}
}
