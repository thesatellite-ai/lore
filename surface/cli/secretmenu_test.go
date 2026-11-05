package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// secretRunner stands in for the engine's on_detect ask decision
// (FB-SEC-1): no choice → E_NEEDS_CONFIRM with masked findings; a
// not-a-secret choice → its own confirm until Yes; any other choice, or a
// confirmed not-a-secret → the write runs (writes counts it).
type secretRunner struct {
	*fake
	findings int
	writes   int
	last     op.SecretChoice
}

func (r *secretRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	r.last = c.Secret
	if !c.Secret.Any() {
		fs := make([]errs.Finding, r.findings)
		for i := range fs {
			fs[i] = errs.Finding{N: i + 1, Rule: "stripe", Field: "body", Line: 1, Masked: "sk_••••FAKE"}
		}
		return op.Result{}, errs.New(errs.NeedsConfirm, "body looks like a secret").WithFindings(fs).
			WithAddArgs([]string{"--secret-unstored"})
	}
	if c.Secret.NotASecret && !c.Yes {
		return op.Result{}, errs.New(errs.NeedsConfirm, "keep it as not a secret ("+c.Secret.Reason+")?")
	}
	r.writes++
	return r.fake.Run(ctx, e, d, c)
}

// TestSecretMenu (SECRETS 2.1, FB-SEC-2): at a terminal an ask finding
// shows the masked menu and proceeds only with a choice that carries its
// content (typed references, unstored, not a secret with a typed reason
// plus its confirm); cancel, an empty answer, junk, end of input, an empty
// reference or an empty reason write nothing, with or without --yes. No
// terminal: the answer comes back unchanged and nothing is read.
func TestSecretMenu(t *testing.T) {
	cases := []struct {
		name     string
		findings int
		tty      bool
		stdin    string
		extra    []string
		writes   int
		want     op.SecretChoice
	}{
		{name: "redact", findings: 1, tty: true, stdin: "r\ngopass://a/b\n", writes: 1, want: op.SecretChoice{Refs: []string{"gopass://a/b"}}},
		{name: "redact two", findings: 2, tty: true, stdin: "R\ngopass://a\nenv://B\n", writes: 1, want: op.SecretChoice{Refs: []string{"gopass://a", "env://B"}}},
		{name: "unstored", findings: 1, tty: true, stdin: "u\n", writes: 1, want: op.SecretChoice{Unstored: true}},
		{name: "not a secret", findings: 1, tty: true, stdin: "n\ndocs sample\ny\n", writes: 1, want: op.SecretChoice{NotASecret: true, Reason: "docs sample"}},
		{name: "not a secret, confirm no", findings: 1, tty: true, stdin: "n\ndocs sample\n\n"},
		{name: "not a secret, empty reason", findings: 1, tty: true, stdin: "n\n\n"},
		{name: "not a secret, eof at reason", findings: 1, tty: true, stdin: "n\n"},
		{name: "redact, empty ref", findings: 1, tty: true, stdin: "r\n\n"},
		{name: "redact two, one ref", findings: 2, tty: true, stdin: "r\ngopass://a\n"},
		{name: "cancel", findings: 1, tty: true, stdin: "c\n"},
		{name: "yes is no choice", findings: 1, tty: true, stdin: "y\ny\ny\n"},
		{name: "enter", findings: 1, tty: true, stdin: "\n\n\n"},
		{name: "eof", findings: 1, tty: true, stdin: ""},
		{name: "junk then unstored", findings: 1, tty: true, stdin: "x\nu\n", writes: 1, want: op.SecretChoice{Unstored: true}},
		{name: "with --yes still a choice", findings: 1, tty: true, stdin: "\n", extra: []string{"--yes"}},
		{name: "--yes and n", findings: 1, tty: true, stdin: "n\nfixture\n", extra: []string{"--yes"}, writes: 1, want: op.SecretChoice{NotASecret: true, Reason: "fixture"}},
		{name: "no terminal", findings: 1, stdin: "u\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &secretRunner{fake: secretFake(t), findings: c.findings}
			root := Build(r.reg, r, WithInteractive(func() bool { return c.tty }))
			res := execute(root, c.stdin, append([]string{"note", "add", "x"}, c.extra...)...)
			if r.writes != c.writes {
				t.Fatalf("writes %d want %d (exit %d, stderr %q)", r.writes, c.writes, res.exit, res.stderr)
			}
			if c.writes == 0 && res.exit != errs.DefOf(errs.NeedsConfirm).Exit {
				t.Fatalf("exit %d, want the E_NEEDS_CONFIRM exit (stderr %q)", res.exit, res.stderr)
			}
			if c.writes > 0 {
				got := r.last
				if got.NotASecret != c.want.NotASecret || got.Unstored != c.want.Unstored || got.Reason != c.want.Reason || !slices.Equal(got.Refs, c.want.Refs) {
					t.Fatalf("choice %+v want %+v", got, c.want)
				}
			}
			if c.tty && c.stdin != "" && !strings.Contains(res.stderr, "sk_••••FAKE") {
				t.Fatalf("the menu does not list the masked finding: %q", res.stderr)
			}
			if !c.tty && strings.Contains(res.stderr, "[r] redact") {
				t.Fatal("a menu without a terminal")
			}
		})
	}
}
