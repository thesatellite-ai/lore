package wire

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestEveryRosterReadTakesStore pins devco D-499 for the whole class:
// every read of the org roster (a direct profile op that only reads)
// takes --store, so no roster read is left that cannot name its org from
// a plain folder (before: team list, team show, member list … said
// "unknown flag: --store").
func TestEveryRosterReadTakesStore(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	n := 0
	for _, e := range a.Registry().Entries() {
		if !rosterRead(e.Spec()) {
			continue
		}
		n++
		if !slices.ContainsFunc(e.InputFields(), func(f op.FieldInfo) bool { return f.Name == readStoreField }) {
			t.Errorf("%s takes no --store", e.Spec().Name)
		}
	}
	if n < 8 {
		t.Fatalf("only %d roster reads found; the registry check is not looking at them", n)
	}
}

// TestRosterReadNamesItsOrg pins devco D-499: from a plain folder no org
// applies to, `team list --store <org>` reads that org's roster; with no
// flag the refusal names --store <org> and --setup <setup>, never the
// repo open's git-init hint; a --store naming no org says so.
func TestRosterReadNamesItsOrg(t *testing.T) {
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
	open := func(org bool) *App {
		opts := Options{RepoDir: t.TempDir(), Home: home, FlagActor: testEmail, OrgStores: hook, Wall: time.Now}
		if org {
			opts.Org = func(context.Context) (*identity.Org, error) {
				return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail}}, nil
			}
		}
		a, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	admin := open(true) // a folder of the org's setup
	add, _ := admin.Registry().Lookup("team.add")
	if _, err := admin.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"handle":"@web"}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("team add: %v", err)
	}
	away := open(false) // a folder no org applies to
	list, _ := away.Registry().Lookup("team.list")
	res, err := away.Run(ctx, list, op.JSONDecoder{Body: []byte(`{"store":"agency"}`)}, op.Caller{})
	if err != nil {
		t.Fatalf("team list --store agency: %v", err)
	}
	if out, jerr := res.JSON(); jerr != nil || !strings.Contains(string(out), `"web"`) {
		t.Fatalf("team list --store agency does not list @web: %s %v", out, jerr)
	}
	_, err = open(false).Run(ctx, list, op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{})
	env := errs.EnvelopeOf(err).Error
	if !errs.Has(err, errs.NotAStore) || !strings.Contains(env.Hint, "--store <org>") || !strings.Contains(env.Hint, "--setup <setup>") || strings.Contains(env.Hint, "git init") {
		t.Fatalf("team list with no org: %v (hint %q), want E_NOT_A_STORE naming --store <org> and --setup <setup>", err, env.Hint)
	}
	_, err = open(false).Run(ctx, list, op.JSONDecoder{Body: []byte(`{"store":"nosuch"}`)}, op.Caller{})
	if env = errs.EnvelopeOf(err).Error; !errs.Has(err, errs.NotFound) || !strings.Contains(env.Message, "names no org") {
		t.Fatalf("team list --store nosuch: %v, want E_NOT_FOUND saying it names no org", err)
	}
	// devco D-523: a roster change with no org here names --setup (it
	// writes through the org's setup) and says --store is the reads'
	_, err = open(false).Run(ctx, add, op.JSONDecoder{Body: []byte(`{"handle":"@ops"}`)}, op.Caller{Yes: true})
	if env = errs.EnvelopeOf(err).Error; !errs.Has(err, errs.NotAStore) || !strings.Contains(env.Hint, "--setup <setup>") || !strings.Contains(env.Hint, "--store <org>") || !strings.Contains(env.Message, "no roster to change") {
		t.Fatalf("team add with no org: %v (hint %q), want E_NOT_A_STORE naming --setup <setup> and --store <org>", err, env.Hint)
	}
}
