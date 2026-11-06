package settings_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/surface/cli"
)

// runCLI runs one lore command line through the CLI built from the real
// registry and engine (the seam OV27 found broken: an unexported embedded
// flag struct never reached the CLI).
func runCLI(t *testing.T, h *opstest.H, args ...string) (int, string) {
	t.Helper()
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	root := cli.Build(h.App.Registry(), h.App, cli.WithCaller(func(context.Context) (op.Caller, error) { return op.Caller{Actor: actor}, nil }))
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err = root.Execute()
	return cli.ExitCode(err), out.String() + errOut.String()
}

// TestResourceFlagsReachCLI (OV27): the bulk flags (--kind --where
// --dry-run, embedded BulkWhere), the value flags (--set-json, embedded
// ResValueFlags), check --note, field edit --no-internal and --clear are
// real CLI flags that reach the operation; an unknown flag exits 20.
func TestResourceFlagsReachCLI(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"cost","type":"number","internal":true,"max":"9"}`},
	)
	for _, n := range []string{"h1", "h2"} {
		h.MustYes(settings.OpResAdd, `{"kind":"host","name":"`+n+`","set":["provider=linode"]}`)
	}
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"resource", "edit", "--kind", "host", "--where", "provider=linode", "--set", "provider=aws", "--dry-run"}, "would edit 2 host"},
		{[]string{"resource", "edit", "--kind", "host", "--where", "provider=linode", "--set", "provider=aws", "--yes"}, "edit 2 host resources"},
		{[]string{"resource", "check", "--kind", "host", "--where", "provider=aws", "--note", "audited", "--yes"}, "check 2 host resources"},
		{[]string{"resource", "add", "host", "h3", "--set-json", `provider="hetzner"`, "--yes"}, "h3"},
		{[]string{"resource", "field", "edit", "host", "cost", "--no-internal", "--yes"}, ""},
		{[]string{"resource", "field", "edit", "host", "cost", "--clear", "max", "--yes"}, ""},
	}
	for _, s := range steps {
		code, out := runCLI(t, h, s.args...)
		if code != 0 || !strings.Contains(out, s.want) {
			t.Fatalf("lore %s: exit %d %q", strings.Join(s.args, " "), code, out)
		}
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); !strings.Contains(s, "provider = aws") || !strings.Contains(s, "note: audited") {
		t.Errorf("h1 after the CLI bulk edit and check %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h3"}`)); !strings.Contains(s, "provider = hetzner") {
		t.Errorf("--set-json %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpKindShow, `{"id":"host"}`)); strings.Contains(s, "max 9") {
		t.Errorf("--clear max did not reach the op %q", s)
	}
	if code, _ := runCLI(t, h, "resource", "edit", "h1", "--no-such-flag"); code != 20 {
		t.Errorf("unknown flag exit %d, want 20 (bad argument, round 2)", code)
	}
}
