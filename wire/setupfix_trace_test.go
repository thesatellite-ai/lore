package wire

// setupfix_trace_test.go — a "Run it" fix shows up in the request's probe
// trace, labelled with its check id and redacted (before, the FixRunner
// ran its own exec and the Details panel and debug log never saw it).

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

func TestRunFixIsTraced(t *testing.T) {
	t.Parallel()
	ctx, tr := probe.WithTrace(context.Background())
	ctx = opssetup.WithFixCheck(ctx, onboard.CheckStorePrivate)
	secret := "ghp_" + strings.Repeat("Z", 36)
	probe.Secret(ctx, secret)
	run, err := setupFixRunner{}.RunFixIn(ctx, t.TempDir(), []string{FixProgramGit, "config", "--get", "x.y" + secret})
	if err != nil {
		t.Fatal(err)
	}
	calls := tr.Calls()
	if len(calls) != 1 {
		t.Fatalf("trace has %d calls, want the fix", len(calls))
	}
	c := calls[0]
	if c.Label != string(onboard.CheckStorePrivate) || c.Kind != probe.CallExec || c.Exit != run.Exit {
		t.Fatalf("call = %+v, want label %s, exec, exit %d", c, onboard.CheckStorePrivate, run.Exit)
	}
	if !strings.HasPrefix(c.Command, "git config --get ") || strings.Contains(c.Command, secret) {
		t.Fatalf("command %q: want the argv as typed, secret redacted", c.Command)
	}
	// No check in ctx: still traced, as "fix".
	ctx2, tr2 := probe.WithTrace(context.Background())
	if _, err := (setupFixRunner{}).RunFixIn(ctx2, "", []string{FixProgramGit, "--version"}); err != nil {
		t.Fatal(err)
	}
	if cs := tr2.Calls(); len(cs) != 1 || cs[0].Label != fixCallLabel {
		t.Fatalf("calls = %+v, want one labelled %q", cs, fixCallLabel)
	}
	// Without a trace nothing is recorded and the fix still runs.
	if _, err := (setupFixRunner{}).RunFixIn(context.Background(), "", []string{FixProgramGit, "--version"}); err != nil {
		t.Fatal(err)
	}
}

func TestShellWord(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"acme/team": "acme/team", "a b": "'a b'", "": "''", "it's": `'it'\''s'`} {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}
