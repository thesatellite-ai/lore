package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestDroppedTeamStoreSaidOnce (devco D-727): a member taken off a
// store's team loses the store at the refresh that copies lore-org.yaml;
// that was silent. The drop is recorded and the next sync says it once,
// naming the audience, why, the clone left on disk and the command that
// removes it; the sync after says nothing. A store taken out of the file
// altogether, and one the person keeps, are never named.
func TestDroppedTeamStoreSaidOnce(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const team = "git@git.example.org:acme/vault.git"
	const ops = "git@git.example.org:acme/ops.git"
	const gone = "git@git.example.org:acme/old.git"
	main := orgconf.Store{URL: "git@git.example.org:acme/notes.git", OrgFolder: "org"}
	have := orgconf.Shared{Stores: []orgconf.Store{main, {URL: team, Audience: orgconf.TeamAudience("@vault")}, {URL: ops, Audience: orgconf.AudienceAdmins}, {URL: gone}}}
	full := orgconf.Shared{Stores: []orgconf.Store{main, {URL: team, Audience: orgconf.TeamAudience("@vault")}, {URL: ops, Audience: orgconf.AudienceAdmins}}}
	mine := orgconf.Shared{Stores: []orgconf.Store{main}}

	ds := droppedRestricted("acme", have, mine, full)
	if len(ds) != 2 {
		t.Fatalf("dropped = %+v, want the team and the admins store (not the removed one)", ds)
	}
	recordDroppedStores(home, ds)
	notes := droppedStoreNotes(home)
	if len(notes) != 2 {
		t.Fatalf("notes = %q", notes)
	}
	for _, want := range []string{"vault: no longer read: audience team:@vault, you are not on it", "the clone at ", "lore store forget --url " + team + " --yes removes it"} {
		if !strings.Contains(notes[0], want) {
			t.Errorf("note %q lacks %q", notes[0], want)
		}
	}
	if !strings.Contains(notes[1], "ops: no longer read: audience admins, you are not an admin of acme") {
		t.Errorf("admins note %q", notes[1])
	}
	if again := droppedStoreNotes(home); len(again) != 0 {
		t.Errorf("said twice: %q", again)
	}
	if ds := droppedRestricted("acme", mine, mine, full); len(ds) != 0 {
		t.Errorf("a person who never had the store is told it was dropped: %+v", ds)
	}
}
