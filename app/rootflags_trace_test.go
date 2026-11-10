package app

import (
	"os"
	"testing"

	"github.com/spf13/cobra"

	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// TestTraceFlagSetsEnv: --trace sets LORE_TRACE=1 for the process once the
// flags are parsed (the signals trace reads the variable), keeps any
// PersistentPreRunE that was there, and without the flag leaves the
// variable alone.
func TestTraceFlagSetsEnv(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"x", "--trace"}, envOn},
		{[]string{"x"}, ""},
	} {
		t.Setenv(hosthooks.EnvTrace, "")
		prevRan := false
		root := &cobra.Command{Use: "lore", PersistentPreRunE: func(*cobra.Command, []string) error { prevRan = true; return nil }}
		root.AddCommand(&cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }})
		addRootFlags(root, &rootFlags{})
		root.SetArgs(c.args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(hosthooks.EnvTrace); got != c.want {
			t.Errorf("%v: LORE_TRACE=%q, want %q", c.args, got, c.want)
		}
		if !prevRan {
			t.Errorf("%v: the earlier PersistentPreRunE did not run", c.args)
		}
	}
}
