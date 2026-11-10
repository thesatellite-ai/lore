package app

import (
	"context"
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/op"
)

// TestSetupFlagReachesShortcuts: the global --setup reaches the operation
// whichever spelling runs it (ONBOARDING_DESIGN 3.3 step 1). A bare-noun
// shortcut (`lore whoami` → whoami show) parses no flags itself, so the
// root's PersistentPreRunE runs before --setup is read; forward carries it
// into the context. Before that fix `lore whoami --setup nosuch` ran
// under the folder's setup and printed it as if the flag were not there.
func TestSetupFlagReachesShortcuts(t *testing.T) {
	for _, args := range [][]string{
		{"w", "show", "--setup", "acme-hub"},
		{"w", "--setup", "acme-hub"},
		{"--setup", "acme-hub", "w"},
		{"w", "--setup=acme-hub"},
	} {
		got := "unset"
		root := &cobra.Command{Use: "lore"}
		root.SetContext(context.Background())
		g := &cobra.Command{Use: "w"}
		g.AddCommand(&cobra.Command{Use: "show", RunE: func(c *cobra.Command, _ []string) error {
			got = op.SetupOf(c.Context())
			return nil
		}})
		root.AddCommand(g)
		addRootFlags(root, &rootFlags{})
		if defaultVerb(root, "w", "show") == nil {
			t.Fatal("defaultVerb found no show verb")
		}
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got != "acme-hub" {
			t.Errorf("%v: setup in context = %q, want acme-hub", args, got)
		}
	}
}
