package wire

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestOrgReadWorksInAnyFolder pins devco D-471 and D-472: a read naming
// an org store (`rule list --store agency/org`) lists its rows from a git
// repo under the setup's folders that lore is not set up in, as it does
// from a plain folder (before: refused, the repo attaches nothing); a read
// naming a folder the store does not have is E_NOT_FOUND saying so and
// naming `store folder list --store`, never the write's E_NEEDS_CONFIRM
// "nothing written … --create". The whole-org read and folders no setup
// covers need a git-hosted org store: the store-reads journey has them.
func TestOrgReadWorksInAnyFolder(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{
			{Name: "agency", URL: orgURL, Folder: rowfs.FolderRoot},
			{Name: rosterStore, URL: orgURL, Folder: "org"},
		}, nil
	}
	home := t.TempDir()
	app := func(dir string) *App {
		a, err := New(Options{RepoDir: dir, Home: home, FlagActor: testEmail, OrgStores: hook, Wall: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	plain := t.TempDir()
	a := app(plain)
	add, _ := a.Registry().Lookup("rule.add")
	const body = "every estimate is reviewed by two people"
	if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"` + body + `","to":"` + string(rosterStore) + `"}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("rule add --to %s: %v", rosterStore, err)
	}
	list, _ := a.Registry().Lookup("rule.list")
	repo := t.TempDir() // a git repo with no .lore/
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for _, place := range []struct{ name, dir string }{{"a plain folder", plain}, {"a repo lore is not set up in", repo}} {
		res, err := app(place.dir).Run(ctx, list, op.JSONDecoder{Body: []byte(`{"store":"` + string(rosterStore) + `"}`)}, op.Caller{})
		if err != nil {
			t.Fatalf("rule list --store %s in %s: %v", rosterStore, place.name, err)
		}
		if out, jerr := res.JSON(); jerr != nil || !strings.Contains(string(out), body) {
			t.Fatalf("rule list --store %s in %s does not list the rule: %s %v", rosterStore, place.name, out, jerr)
		}
		_, err = app(place.dir).Run(ctx, list, op.JSONDecoder{Body: []byte(`{"store":"agency/nofolder"}`)}, op.Caller{})
		env := errs.EnvelopeOf(err).Error
		if !errs.Has(err, errs.NotFound) || !strings.Contains(env.Message, "has no folder nofolder yet") || strings.Contains(env.Message, "nothing written") ||
			!strings.Contains(env.Hint, "store folder list --store agency") || strings.Contains(env.Hint, "--create") {
			t.Fatalf("rule list --store agency/nofolder in %s: %v (hint %q), want E_NOT_FOUND naming the missing folder and store folder list, no write wording", place.name, err, env.Hint)
		}
	}
}
