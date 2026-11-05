package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

type noteIn struct {
	Body string `arg:"0" json:"body" desc:"text"`
}

type blockIn struct {
	ID     string `arg:"0" json:"id" desc:"the task"`
	Reason string `flag:"reason" json:"reason,omitempty" desc:"why it is blocked"`
}

type echoOut struct {
	Echo string `json:"echo"`
}

// secretFake is newFake plus two write ops whose write path may ask about
// a secret (Confirm.MayAsk): one plain, one with its own --reason flag.
func secretFake(t *testing.T) *fake {
	t.Helper()
	f := newFake(t)
	note := op.Define(op.Spec{Name: "note.add", Summary: "Add a note", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.SecretDetected}, Confirm: op.ConfirmRule{MayAsk: true}},
		func(_ context.Context, in noteIn) (echoOut, error) { return echoOut{Echo: in.Body}, nil })
	block := op.Define(op.Spec{Name: "task.block", Summary: "Block a task", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.SecretDetected}, Confirm: op.ConfirmRule{MayAsk: true}},
		func(_ context.Context, in blockIn) (echoOut, error) { return echoOut{Echo: in.Reason}, nil })
	entries := []op.Entry{note, block}
	for _, name := range []string{"memory.add", "memory.list", "trash.purge", opRuleAdd, "merge.driver"} {
		e, ok := f.reg.Lookup(name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		entries = append(entries, e)
	}
	reg, err := op.NewRegistry(entries...)
	if err != nil {
		t.Fatal(err)
	}
	f.reg = reg
	return f
}

// TestSecretFlags (SECRETS 4, P2-CTX-3): --not-a-secret, --reason,
// --secret-ref (repeatable) and --secret-unstored exist only on operations
// whose write path may ask about a secret (MayAsk) and fill op.Caller.Secret
// as given; an op with its own --reason shares it with --not-a-secret (the
// op still receives it); an op that cannot ask refuses the flags as
// unknown. The CLI does not validate the combination: the engine does.
func TestSecretFlags(t *testing.T) {
	cases := []struct {
		args []string
		exit int
		want op.SecretChoice
	}{
		{args: []string{"note", "add", "x", "--not-a-secret", "--reason", "doc sample"}, want: op.SecretChoice{NotASecret: true, Reason: "doc sample"}},
		{args: []string{"note", "add", "x", "--secret-ref", "gopass://a", "--secret-ref", "env://B"}, want: op.SecretChoice{Refs: []string{"gopass://a", "env://B"}}},
		{args: []string{"note", "add", "x", "--secret-unstored"}, want: op.SecretChoice{Unstored: true}},
		{args: []string{"note", "add", "x", "--reason", "only a reason"}, want: op.SecretChoice{Reason: "only a reason"}},
		{args: []string{"note", "add", "x"}, want: op.SecretChoice{}},
		{args: []string{"note", "add", "x", "--not-a-secret", "--secret-unstored"}, want: op.SecretChoice{NotASecret: true, Unstored: true}},
		{args: []string{"task", "block", "t1", "--reason", "waiting on ü", "--not-a-secret"}, want: op.SecretChoice{NotASecret: true, Reason: "waiting on ü"}},
		{args: []string{"task", "block", "t1", "--not-a-secret"}, want: op.SecretChoice{NotASecret: true}},
		{args: []string{"memory", "add", "x", "--not-a-secret"}, exit: ExitUsage},
		{args: []string{"memory", "add", "x", "--secret-unstored"}, exit: ExitUsage},
		{args: []string{"memory", "add", "x", "--secret-ref", "env://A"}, exit: ExitUsage},
		{args: []string{"memory", "list", "--reason", "x"}, exit: ExitUsage},
	}
	for _, c := range cases {
		f := secretFake(t)
		r := execute(Build(f.reg, f), "", append(c.args, "--json")...)
		if r.exit != c.exit {
			t.Fatalf("%v: exit %d want %d (stderr %q)", c.args, r.exit, c.exit, r.stderr)
		}
		if c.exit != 0 {
			continue
		}
		got := f.lastCaller().Secret
		if got.NotASecret != c.want.NotASecret || got.Unstored != c.want.Unstored || got.Reason != c.want.Reason || !slices.Equal(got.Refs, c.want.Refs) {
			t.Errorf("%v: caller secret %+v, want %+v", c.args, got, c.want)
		}
		if c.args[0] == "task" && c.want.Reason != "" {
			if echo := echoField(t, r); echo != c.want.Reason {
				t.Errorf("%v: the op lost its own --reason: %q", c.args, echo)
			}
		}
	}
}

// echoField reads "echo" from a JSON output.
func echoField(t *testing.T, r result) string {
	t.Helper()
	return echoOf(t, r)
}
