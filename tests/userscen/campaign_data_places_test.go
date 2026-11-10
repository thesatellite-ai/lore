//go:build userscen

package userscen

// campaign_data_places_test.go — commands that move definitions between
// places: a resource kind defined in the repo and adopted into a store
// (ids kept) and usable there, a same-place adopt refused,
// and a holder handed over by secrets move to an act_ id.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCampaignData_ResourceAdopt: a kind defined in the repo is adopted
// into a store with its fields (ids kept, as docs/operations/resources.md
// step 16 shows), the repo resource made before the move still matches
// it and a new one can use it. A kind cannot start in the personal store
// (`resource kind add --to me` is refused, TestCampaignDataFollowups_
// KindToMeRefused), so the documented route is repo → store.
func TestCampaignData_ResourceAdopt(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "laptop")
	c.MustLore("resource", "field", "add", "laptop", "owner", "--type", "text")
	c.MustLore("resource", "add", "laptop", "mbp1", "--set", "owner=alice")
	// a plain folder store (a file:// url is always one)
	opsDir := filepath.Join(c.A.Home, "ops")
	if err := os.MkdirAll(opsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	c.MustLore("store", "init", "ops", "file://"+opsDir, "--yes")
	kindID := func() (id, place string) {
		for _, l := range strings.Split(c.MustLore("resource", "kinds").Stdout, "\n") {
			if f := strings.Fields(l); len(f) > 2 && strings.HasPrefix(f[0], "knd_") && f[1] == "laptop" {
				id, place = f[0], f[2]
			}
		}
		return id, place
	}
	before, place := kindID()
	if before == "" || place != "repo" {
		t.Fatalf("resource kinds lists no repo laptop kind: %q %q", before, place)
	}
	cdRefused(t, c.Lore("resource", "adopt", "--from", "repo", "--to", "ops", "--json"), "E_NEEDS_CONFIRM")
	c.MustLore("resource", "adopt", "--from", "repo", "--to", "ops", "--yes")
	if after, place := kindID(); after != before || place != "ops" {
		t.Errorf("after adopt: kind %s in %q, want %s in ops (ids kept)", after, place, before)
	}
	if got := resShow(t, c, "laptop/mbp1"); got.Values["owner"] != "alice" {
		t.Errorf("the resource made before adopt: %+v", got)
	}
	c.MustLore("resource", "add", "laptop", "mbp2", "--set", "owner=bob")
	if got := resShow(t, c, "laptop/mbp2"); got.Values["owner"] != "bob" {
		t.Errorf("a resource of the adopted kind: %+v", got)
	}
	cdRefused(t, c.Lore("resource", "adopt", "--from", "repo", "--to", "repo", "--yes", "--json"), "E_INVALID_INPUT")
}

// TestCampaignData_SecretsMove: references held by one holder move to an
// actor id in one batch; a second run is refused (nothing held any more).
func TestCampaignData_SecretsMove(t *testing.T) {
	c := resRepo(t)
	c.MustLore("memory", "add", "--body", "db ${secret:gopass://acme/db/main?holder=act_0000000000000000000000000000aaaa}")
	who := c.MustLore("whoami", "--json").Stdout
	i := strings.Index(who, "act_")
	if i < 0 {
		t.Fatalf("whoami --json names no act_ id: %s", who)
	}
	me := who[i : i+len("act_")+32]
	c.MustLore("secrets", "move", "--from", "act_0000000000000000000000000000aaaa", "--to", me, "--yes").Says(t, "1")
	l := c.MustLore("secrets", "list", "--json").Stdout
	if !strings.Contains(l, me) || strings.Contains(l, "act_0000000000000000000000000000aaaa") {
		t.Errorf("after move: %s", l)
	}
	// run twice: nothing is held by the old holder any more, a clean refusal
	cdRefused(t, c.Lore("secrets", "move", "--from", "act_0000000000000000000000000000aaaa", "--to", me, "--yes", "--json"), "E_NOT_FOUND")
}
