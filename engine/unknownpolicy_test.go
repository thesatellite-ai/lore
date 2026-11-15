package engine

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
)

// TestUnknownPolicyKeyIsOneNote (devco:V-16): a policy row a newer lore
// wrote (branches.isolated before this build knew branch layers) made
// every read and write fail E_CONFIG_INVALID. The resolve now ignores it
// and the engine says so once per key and process: the key, the row, that
// it is ignored, and `lore upgrade`.
func TestUnknownPolicyKeyIsOneNote(t *testing.T) {
	t.Parallel()
	var said []string
	e := &Engine{notice: func(s string) { said = append(said, s) }}
	set, err := policy.Resolve([]policy.Entry{{Level: policy.LevelFolder, Key: "branches.from_the_future", Value: txt("x"), Source: polID(t)}})
	if err != nil {
		t.Fatalf("an unknown key failed the resolve: %v", err)
	}
	e.noteUnknownPolicy(set.Unknown())
	e.noteUnknownPolicy(set.Unknown()) // a second resolve in the same command
	if len(said) != 1 {
		t.Fatalf("notes = %q, want exactly one", said)
	}
	for _, want := range []string{"branches.from_the_future", polID(t).String(), "ignored", "lore upgrade"} {
		if !strings.Contains(said[0], want) {
			t.Errorf("note %q lacks %q", said[0], want)
		}
	}
	(&Engine{}).noteUnknownPolicy(set.Unknown()) // no notice set: silent, no panic
}
