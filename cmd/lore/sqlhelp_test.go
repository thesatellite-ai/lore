package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	opsql "github.com/thesatellite-ai/lore/ops/sql"
)

// TestSQLBareHelp (B11 port of saas/cmd/cli sql_shortcut_test.go
// TestSQLShortcut, last step): a bare `lore sql` prints the help with the
// shortcut example and exits 0; `lore sql <query>` still forwards.
func TestSQLBareHelp(t *testing.T) {
	t.Parallel()
	var forwarded []string
	root := &cobra.Command{Use: "lore"}
	g := &cobra.Command{Use: opsql.Noun, Args: cobra.ArbitraryArgs, DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error { forwarded = args; return nil }}
	g.AddCommand(&cobra.Command{Use: sqlRunVerb, RunE: func(*cobra.Command, []string) error { return nil }})
	root.AddCommand(g)
	mountSQLHelp(root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{opsql.Noun})
	if err := root.Execute(); err != nil {
		t.Fatalf("bare lore sql: %v", err)
	}
	if !strings.Contains(out.String(), `lore sql "SELECT name, ip FROM res_server"`) || !strings.Contains(out.String(), "lore sql keeps both names") || forwarded != nil {
		t.Fatalf("bare lore sql must print the help with the example, not forward:\n%s", out.String())
	}
	if run := find(g, sqlRunVerb); !strings.Contains(run.Long, "each row twice") {
		t.Fatalf("lore sql run help lacks the sources note:\n%s", run.Long)
	}
	root.SetArgs([]string{opsql.Noun, "SELECT 1"})
	if err := root.Execute(); err != nil || len(forwarded) != 1 || forwarded[0] != "SELECT 1" {
		t.Fatalf("a query must still forward: %v %v", forwarded, err)
	}
}
