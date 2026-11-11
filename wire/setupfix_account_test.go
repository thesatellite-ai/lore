package wire

// setupfix_account_test.go — a gh fix runs as the setup's gh login
// (realmac item 9): its token goes to the gh child's GH_TOKEN only, a
// login gh does not have stops the fix before it runs, and nothing but gh
// ever gets a token.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/probe"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// fakeGHPath puts a gh on PATH that hands out tok for login mira and
// records, for `repo create`, whether GH_TOKEN carried that token.
func fakeGHPath(t *testing.T, tok string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "gh.log")
	script := `#!/bin/sh
case "$1 $2" in
  "auth token") echo "$6" >> "` + log + `.tok"; [ "$6" = mira ] && { echo "` + tok + `"; exit 0; }; echo "no oauth token found" >&2; exit 1;;
  "repo create") if [ "$GH_TOKEN" = "` + tok + `" ]; then echo "create as mira" >> "` + log + `"; else echo "create as active" >> "` + log + `"; fi; echo "created $3";;
  *) echo "unexpected $*" >&2; exit 2;;
esac
`
	fakeexec.Write(t, filepath.Join(dir, "gh"), script, 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "")
	return log
}

func TestRunFixGHAsAccount(t *testing.T) {
	tok := "fake_tok_" + strings.Repeat("q", 12) // built at run time
	create := []string{FixProgramGH, "repo", "create", "mira/lore-me", "--private"}
	cases := []struct {
		name, account string
		exit          int
		logged        string // the repo create line, "" = gh repo create never ran
		outHas        string
	}{
		{"setup account", "mira", 0, "create as mira", "created mira/lore-me"},
		{"no account: gh's active login", "", 0, "create as active", "created"},
		{"gh has no login for it", "tomas", 1, "", "gh has no login for tomas"},
		{"not a login name", "-x", 1, "", "is not a GitHub login"},
		{"name with a space", "a b", 1, "", "is not a GitHub login"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := fakeGHPath(t, tok)
			ctx, tr := probe.WithTrace(context.Background())
			ctx = opssetup.WithFixGHAccount(ctx, c.account)
			run, err := setupFixRunner{}.RunFixIn(ctx, "", create)
			if err != nil {
				t.Fatal(err)
			}
			if run.Exit != c.exit || !strings.Contains(run.OutputTail, c.outHas) {
				t.Fatalf("run = %+v, want exit %d with %q", run, c.exit, c.outHas)
			}
			b, _ := os.ReadFile(log)
			if got := strings.TrimSpace(string(b)); got != c.logged {
				t.Fatalf("gh repo create ran %q, want %q", got, c.logged)
			}
			if strings.Contains(run.OutputTail, tok) {
				t.Fatal("output shows the token")
			}
			calls := tr.Calls()
			if len(calls) != 1 {
				t.Fatalf("trace has %d calls, want the fix only", len(calls))
			}
			if strings.Contains(calls[0].Command+calls[0].Output, tok) {
				t.Fatalf("trace %+v shows the token", calls[0])
			}
		})
	}
}

// A fix that is not gh never looks up a token, even with an account set.
func TestRunFixNonGHGetsNoToken(t *testing.T) {
	log := fakeGHPath(t, "fake_tok_unused_value")
	ctx := opssetup.WithFixGHAccount(context.Background(), "mira")
	run, err := setupFixRunner{}.RunFixIn(ctx, "", []string{FixProgramGit, "--version"})
	if err != nil || run.Exit != 0 {
		t.Fatalf("git --version: %+v %v", run, err)
	}
	if _, err := os.Stat(log + ".tok"); err == nil {
		t.Fatal("a git fix looked up a gh token")
	}
}
