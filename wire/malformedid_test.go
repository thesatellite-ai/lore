package wire

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestMalformedIDRefusedFirstD634: a value shaped like a lore id (a
// registered prefix and "_") that is not one is refused as a bad id
// before any store or git lookup; from the home folder `show --id
// mem_bogus` said "not inside a git work tree". A valid id, a value that
// is no id, and an id field that takes names too pass through.
func TestMalformedIDRefusedFirstD634(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	for _, tc := range []struct {
		op, id string
		bad    bool
	}{
		{"memory.show", "mem_bogus", true},
		{"row.show", "mem_bogus", true},
		{"memory.show", "mem_0123456789abcdefg123456789abcdef", true},  // not hex
		{"memory.show", "mem_01a1b4a47d1e75c1aa1a0b15f16438", true},    // cut short
		{"memory.show", "mem_00000000000000000000000000000000", false}, // the id's shape: its op says it is no id lore made
		{"memory.show", "mem_01a1b4a47d1e75c1aa1a0b15f1643889", false},
		{"memory.show", "bogus", false},         // no id shape: the lookup answers
		{"resource.show", "act_runner", false},  // a resource is named or res_ id
		{"machine.show", "mch_0cd630f0", false}, // a machine id prefix
	} {
		e, ok := a.Registry().Lookup(tc.op)
		if !ok {
			t.Fatalf("%s is not in the registry", tc.op)
		}
		got := malformedIDErr(context.Background(), a.opts.RepoDir, e, op.JSONDecoder{Body: []byte(`{"id":"` + tc.id + `"}`)})
		switch {
		case tc.bad && !errs.Has(got, errs.InvalidID):
			t.Errorf("%s --id %s: %v, want E_INVALID_ID", tc.op, tc.id, got)
		case tc.bad && !strings.Contains(got.Error(), tc.id[:3]+"_<32 hex>"):
			t.Errorf("%s --id %s: %q does not name the id's shape", tc.op, tc.id, got)
		case !tc.bad && got != nil:
			t.Errorf("%s --id %s: %v, want no refusal here", tc.op, tc.id, got)
		}
	}
}

// TestMalformedIDKindWordsD676: the early refusal speaks the op's own
// kind-specific words (`"mem_nope" is not a memory id`), and inside a git
// work tree it stays out of the way so the op refuses in its own order and
// code (actor delete is E_REFUSED whatever the id; batch show says "batch").
func TestMalformedIDKindWordsD676(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	a, err := New(Options{RepoDir: t.TempDir(), Home: home, FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	ctx := context.Background()
	for _, tc := range []struct{ op, id, says string }{
		{"memory.show", "mem_nope", `memory: "mem_nope" is not a memory id (mem_…): a memory id is mem_ and 32 hex digits`},
		{"batch.show", "bat_nope", `"bat_nope" is not a batch id (bat_…)`},
		{"actor.delete", "act_0123", `"act_0123" is not an actor id (act_…): an actor id is act_`},
		{"row.show", "mem_nope", `"mem_nope" is not a valid lore id`},
	} {
		e, ok := a.Registry().Lookup(tc.op)
		if !ok {
			t.Fatalf("%s is not in the registry", tc.op)
		}
		d := op.JSONDecoder{Body: []byte(`{"id":"` + tc.id + `"}`)}
		if got := malformedIDErr(ctx, a.opts.RepoDir, e, d); got == nil || !strings.Contains(got.Error(), tc.says) {
			t.Errorf("outside a work tree: %s --id %s: %v, want %q", tc.op, tc.id, got, tc.says)
		}
		if got := malformedIDErr(ctx, repo, e, d); got != nil {
			t.Errorf("inside a work tree: %s --id %s: %v, want the op's own refusal", tc.op, tc.id, got)
		}
	}
}
