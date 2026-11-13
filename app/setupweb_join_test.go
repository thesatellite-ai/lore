package app

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestJoinPageNeedsTerminal (B-10): `lore setup join --invite <url>` run
// with no terminal (a script, ssh without a tty, an agent) gets the
// `lore setup apply` lines and exits, never a page waiting 30 idle
// minutes; a terminal or --web still opens the page.
func TestJoinPageNeedsTerminal(t *testing.T) {
	e := op.Define(op.Spec{Name: opssetup.OpJoin, Summary: "x", Permission: op.PermRead, Access: op.AccessRead, Local: true},
		func(context.Context, struct{}) (opssetup.JoinOut, error) {
			return opssetup.JoinOut{Web: opssetup.WebStart{Setup: "acme", Invite: "git@git.acme.example:acme/team-store.git"}}, nil
		})
	res, err := e.Invoke(context.Background(), mustDecode(t, e))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		tty  bool
		want bool
	}{
		{"join in a script", []string{"setup", "join", "--invite", "x"}, false, false},
		{"join on a terminal", []string{"setup", "join", "--invite", "x"}, true, true},
		{"join --web in a script", []string{"setup", "join", "--invite", "x", "--web"}, false, true},
	}
	for _, c := range cases {
		r := pageRunner{env: pageEnv{args: c.args, stdoutTTY: func() bool { return c.tty }}}
		if _, _, got := r.webStart(opssetup.OpJoin, res); got != c.want {
			t.Errorf("%s: open=%v, want %v", c.name, got, c.want)
		}
	}
}
