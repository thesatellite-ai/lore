package wire

import (
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
		got := malformedIDErr(e, op.JSONDecoder{Body: []byte(`{"id":"` + tc.id + `"}`)})
		switch {
		case tc.bad && !errs.Has(got, errs.InvalidID):
			t.Errorf("%s --id %s: %v, want E_INVALID_ID", tc.op, tc.id, got)
		case tc.bad && !strings.Contains(got.Error(), "is not a valid lore id") && !strings.Contains(got.Error(), tc.id[:3]+"_<32 hex>"):
			t.Errorf("%s --id %s: %q does not say the id is malformed and its shape", tc.op, tc.id, got)
		case !tc.bad && got != nil:
			t.Errorf("%s --id %s: %v, want no refusal here", tc.op, tc.id, got)
		}
	}
}
