package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/schema"
)

// TestURLPushHoldOfARemovedMember (D-85, gate7b): a clone pushed without
// an attached open (`lore store push`, the org-store pass of `lore sync`)
// gets the same left hold as an attached store's sync: the org that lists
// the url holds the removed person's commits made after left_at. Before,
// that pass pushed them (TestStoryFPeople saw the team store's main move).
func TestURLPushHoldOfARemovedMember(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1") // the person, not the agent running the test
	ctx := context.Background()
	const team, notes = "git@github.com:fabrikam-co/lore-team", "git@github.com:fabrikam-co/lore-notes"
	home := writeSetupsConfig(t, `orgs:
  - name: fabrikam
    identity: {key: "human:lee@fabrikam.example"}
    stores:
      - url: `+team+`
        org_folder: org
      - url: `+notes+`
`)
	plain := t.TempDir()
	opts, _ := StandaloneOptionsOrg(plain, home, plain, "test")
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })

	if h := app.urlPushHold(ctx, "git@github.com:someone-else/notes"); h != nil {
		t.Fatal("a url no org lists got a push hold")
	}
	hold := app.urlPushHold(ctx, notes)
	if hold == nil {
		t.Fatal("no push hold for a store of the org")
	}
	if _, held := hold(ctx); held {
		t.Fatal("held with no roster row: a member in the org pushes")
	}

	r, err := gitbackend.RemoteOfRaw(team)
	if err != nil {
		t.Fatal(err)
	}
	dir, _, err := gitbackend.CloneDirFor(home, r, "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.ActorID("human:lee@fabrikam.example")
	if err != nil {
		t.Fatal(err)
	}
	actors := filepath.Join(dir, "org", schema.TableActors)
	if err = os.MkdirAll(actors, 0o700); err != nil {
		t.Fatal(err)
	}
	row := `{"status":"left","left_at":"2026-03-02T09:00:00Z"}`
	if err = os.WriteFile(filepath.Join(actors, actor.String()+".json"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{notes, team} {
		got, held := app.urlPushHold(ctx, url)(ctx)
		if !held || !got.After.Equal(time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)) || !strings.Contains(got.Reason, "fabrikam") {
			t.Fatalf("%s after the removal: hold %+v held %v, want held after left_at naming fabrikam", url, got, held)
		}
	}
}
