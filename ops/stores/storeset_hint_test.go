package stores

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreSetBadWordHintNamed (devco:D-760, bug hunt H-15): a word
// store.set cannot parse prints an example that runs as printed, with
// --store and every key=value under its own --settings; it printed
// `lore store set me push=batch …` with positional values, which the
// command does not take.
// The example names the store the person gave (one shell word), else me.
func TestStoreSetBadWordHintNamed(t *testing.T) {
	for _, c := range []struct{ store, want string }{
		{"me", "me"},
		{" acme ", "acme"},
		{"", "me"},
		{"two words", "me"},
		{"x;rm", "me"},
	} {
		_, err := CheckSettings(c.store, []string{"oops"})
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.InvalidInput {
			t.Fatalf("CheckSettings(%q, oops) = %v, want E_INVALID_INPUT", c.store, err)
		}
		want := "lore store set --store " + c.want + " --settings push=batch --settings push_every=10m --settings compact=on --settings compact_after=30d"
		if !strings.Contains(e.Hint, want) {
			t.Fatalf("store %q: hint %q does not give the named example %q", c.store, e.Hint, want)
		}
		if strings.Contains(e.Hint, "store set "+c.want+" ") {
			t.Fatalf("store %q: hint %q still gives the store by position", c.store, e.Hint)
		}
	}
}
