package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// leftoverKinds is the kinds Leftovers reports for home's config.yaml.
func leftoverKinds(t *testing.T, home string) []onboard.LeftoverKind {
	t.Helper()
	c, err := ReadSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	out := []onboard.LeftoverKind{}
	for _, l := range onboard.Leftovers(c) {
		out = append(out, l.Kind)
	}
	return out
}

// TestLeftoversAfterPurge: while setups use the identity and the store
// nothing is left over; a purge without the clears leaves both; one with
// the clears leaves none; ClearLeftover removes one, keeps the rest of the
// file and its header comment, and a second clear finds nothing.
func TestLeftoversAfterPurge(t *testing.T) {
	t.Parallel()
	purgeAll := func(t *testing.T, home string, o PurgeOptions) {
		t.Helper()
		for i, name := range []string{"solo-a", "acme"} {
			if _, err := TrashSetup(home, name, lifeNow.Add(time.Duration(i)*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := PurgeSetup(home, name, o); err != nil {
				t.Fatal(err)
			}
		}
	}

	home := writeSetupsConfig(t, lifeConfig)
	if got := leftoverKinds(t, home); len(got) != 0 {
		t.Fatalf("live setups use both, got leftovers %v", got)
	}
	// Trashed, not purged: restorable, so nothing is left over yet (a
	// leftover is only what a purge or the migration left).
	if _, err := TrashSetup(home, "solo-a", lifeNow); err != nil {
		t.Fatal(err)
	}
	if got := leftoverKinds(t, home); len(got) != 0 {
		t.Fatalf("solo-a trashed: %v, want none (nothing purged)", got)
	}

	home = writeSetupsConfig(t, lifeConfig)
	purgeAll(t, home, PurgeOptions{})
	if got := leftoverKinds(t, home); len(got) != 2 {
		t.Fatalf("purged without clears: %v, want identity and personal_store", got)
	}
	found, err := ClearLeftover(home, onboard.LeftoverIdentity)
	if err != nil || !found {
		t.Fatalf("clear identity: %v %v", found, err)
	}
	raw := readSetupsFile(t, home)
	// (acme's orgs[] entry, and the identity in it, went with the purge of
	// its last setup: purgeAll purged acme.)
	if strings.Contains(raw, "Ada Example") || !strings.Contains(raw, "# keep this comment") || !strings.Contains(raw, "lore-me.git") {
		t.Fatalf("clear identity removed the wrong things:\n%s", raw)
	}
	if found, err = ClearLeftover(home, onboard.LeftoverIdentity); err != nil || found {
		t.Fatalf("second clear: %v %v, want not found", found, err)
	}
	if found, err = ClearLeftover(home, onboard.LeftoverPersonalStore); err != nil || !found {
		t.Fatalf("clear store: %v %v", found, err)
	}
	if raw = readSetupsFile(t, home); strings.Contains(raw, "personal") {
		t.Fatalf("an empty personal: stayed:\n%s", raw)
	}
	if got := leftoverKinds(t, home); len(got) != 0 {
		t.Fatalf("after both clears: %v", got)
	}

	home = writeSetupsConfig(t, lifeConfig)
	purgeAll(t, home, PurgeOptions{ClearIdentity: true, ClearPersonalStore: true})
	if got := leftoverKinds(t, home); len(got) != 0 {
		t.Fatalf("purged with clears: %v", got)
	}
}

// TestConfigWriteKeepsIndentAndEmpties: a setup write keeps the file's own
// indent (4 stays 4, 2 stays 2) and clearing the last keys leaves the
// file's header comment, or an empty file, never `{}`.
func TestConfigWriteKeepsIndentAndEmpties(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, raw, want string }{
		{"4 spaces stay 4", "# mine\nidentity:\n    key: human:a@example.test\npersonal:\n    store: file:///tmp/me\nlive:\n    autopush: true\n", "# mine\nlive:\n    autopush: true\n"},
		{"2 spaces stay 2", "identity:\n  key: human:a@example.test\npersonal:\n  store: file:///tmp/me\nlive:\n  autopush: true\n", "live:\n  autopush: true\n"},
		{"emptied: header only", "# my machine\nidentity:\n  key: human:a@example.test\npersonal:\n  store: file:///tmp/me\n", "# my machine\n"},
		{"emptied: empty file", "identity:\n  key: human:a@example.test\npersonal:\n  store: file:///tmp/me\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := writeSetupsConfig(t, c.raw)
			for _, k := range onboard.LeftoverKinds() {
				if _, err := ClearLeftover(home, k); err != nil {
					t.Fatal(err)
				}
			}
			if got := readSetupsFile(t, home); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
