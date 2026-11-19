package infra

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// Spellings of one repo in the D-576 tests (made-up owner and alias).
const (
	d576Alias    = "git@work-gh:clientco/portal.git"
	d576Resolved = "git@github.com:clientco/portal.git"
)

// aliasDirFiles is the one RepoFiles method adoptRow reaches when no
// origin matches.
type aliasDirFiles struct{ RepoFiles }

func (aliasDirFiles) RepoDirName(context.Context) (string, error) { return "elsewhere", nil }

// TestRepoRemoteReadsBothSpellingsD576 pins devco D-576: a clone's remote
// reaches the identity resolved (URL) with its alias spelling kept
// (Spelled), so a history or origin_url recorded before the fix under the
// alias spelling is still this clone's address: no new unconfirmed entry,
// the row is adopted, and a fork check sees the confirmed address.
func TestRepoRemoteReadsBothSpellingsD576(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clean, _ := cleanRemotes([]Remote{{Name: "origin", URL: d576Resolved, Spelled: d576Alias}})
	if len(clean) != 1 || clean[0].Spelled != d576Alias {
		t.Fatalf("cleanRemotes dropped the written spelling: %+v", clean)
	}
	r := clean[0]
	if got := r.keys(); len(got) != 2 || got[0] != "github.com/clientco/portal" || got[1] != "work-gh/clientco/portal" {
		t.Fatalf("keys = %v, want the resolved key then the alias spelling's", got)
	}

	// a history recorded before D-576 holds the alias spelling only
	old := repourl.History{{URL: d576Alias, Kind: repourl.KindPrimary, How: repourl.HowInit, From: "2026-01-01"}}
	if !knownSpelling(old, r) {
		t.Fatal("an alias-spelled history is not this clone's address")
	}
	if knownSpelling(old, Remote{Name: "origin", URL: "git@github.com:clientco/other.git"}) {
		t.Fatal("another repo's remote counted as known")
	}
	if n := notices(append(old, repourl.Entry{URL: d576Alias, Kind: repourl.KindUnconfirmed, How: repourl.HowSeen, From: "2026-01-02"}), clean, nil); len(n) != 1 || n[0].Remote != "origin" {
		t.Fatalf("an alias-spelled unconfirmed entry is not named by its remote: %+v", n)
	}

	// a v0.1.17 repos row registered through the alias is adopted
	id, err := ids.New[ids.Repo]()
	if err != nil {
		t.Fatal(err)
	}
	origin, err := value.TextOf(d576Alias)
	if err != nil {
		t.Fatal(err)
	}
	rows := []row.Row{{ID: id.Any(), Fields: map[string]value.Value{fieldOrigin: origin}}}
	if got, ok := adoptRow(ctx, nil, aliasDirFiles{}, rows, clean); !ok || got.ID != id.Any() {
		t.Fatalf("the alias-registered row was not adopted (ok=%v)", ok)
	}
	if _, ok := adoptRow(ctx, nil, aliasDirFiles{}, rows, []Remote{{Name: "origin", URL: "git@github.com:clientco/other.git"}}); ok {
		t.Fatal("another repo's clone adopted the row")
	}
}
