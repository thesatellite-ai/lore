package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// testEmail is the identity key of the actor-projection tests (a stable
// key that must never reach a client repo's files).
const testEmail = "human:alice@example.com"

// folderStoreAt makes an internal folder store in a fresh dir and returns
// its file:// url.
func folderStoreAt(t *testing.T, id string, vis rowfs.Visibility) string {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store](id)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: vis}); err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.ToSlash(dir)
}

// writeStoresYAML writes repo's .lore/stores.yaml.
func writeStoresYAML(t *testing.T, repo, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// openWith builds an App over repo with opts' hooks, the identity chain
// running from --actor testEmail.
func openWith(t *testing.T, repo string, opts Options) *App {
	t.Helper()
	opts.RepoDir, opts.FlagActor = repo, testEmail
	if opts.Home == "" {
		opts.Home = t.TempDir()
	}
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestRepoIsClientHook: Options.RepoIsClient decides the repo store's
// class. nil, an error and true make it client-class (fail closed: F15
// refuses repo → internal pointers, the actor row is the F18 projection
// without the email) with a note for nil and error; false makes it
// internal (no note). The repo actor row carries the email only in an
// org's own repo (identity resolved in an org); a no-org repo keeps the
// projection, as v0.1.17 did.
func TestRepoIsClientHook(t *testing.T) {
	boom := errors.New("org store unreachable")
	cases := []struct {
		name     string
		hook     func(context.Context) (bool, error)
		client   bool
		noteHas  string
		emailRow bool
	}{
		{"no hook", nil, true, "no client-repo check is wired", false},
		{"hook error", func(context.Context) (bool, error) { return false, boom }, true, boom.Error(), false},
		{"client", func(context.Context) (bool, error) { return true, nil }, true, "", false},
		// not a client repo but no org: still the projection (v0.1.17
		// wrote {id, display_name, kind} in a no-org repo)
		{"internal, no org", func(context.Context) (bool, error) { return false, nil }, false, "", false},
		{"org own repo", func(context.Context) (bool, error) { return false, nil }, false, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			opts := Options{RepoIsClient: c.hook}
			if c.name == "org own repo" {
				opts.Org = func(context.Context) (*identity.Org, error) {
					return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail, Handle: "alice"}}, nil
				}
			}
			a := openWith(t, repo, opts)
			ctx := background()
			class, err := a.StoreClass(ctx, store.SourceRepo)
			if err != nil {
				t.Fatal(err)
			}
			want := direction.Internal
			if c.client {
				want = direction.Client
			}
			if class != want {
				t.Fatalf("class = %s, want %s", class, want)
			}
			if def, _ := a.StoreClass(ctx, ""); def != want {
				t.Fatalf("default target class = %s", def)
			}
			note, err := a.RepoClassNote(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if (c.noteHas == "") != (note == "") || !strings.Contains(note, c.noteHas) {
				t.Fatalf("note = %q, want it to hold %q", note, c.noteHas)
			}
			if pc, _ := a.StoreClass(ctx, "me"); pc != direction.Personal {
				t.Fatalf("me class = %s", pc)
			}

			withProject(t, a)
			if _, err = run(t, a, opsmemory.OpAdd, `{"body":"first core write"}`, false); err != nil {
				t.Fatal(err)
			}
			who, err := identity.ActorID(testEmail)
			if err != nil {
				t.Fatal(err)
			}
			f := rowFile(t, filepath.Join(repo, ".lore", "data", schema.TableActors, who.String()+".json"))
			_, hasKey := f["stable_key"]
			if hasKey != c.emailRow {
				t.Fatalf("actor row stable_key present = %v, want %v: %v", hasKey, c.emailRow, f)
			}
			if !c.emailRow && strings.Contains(string(mustRead(t, filepath.Join(repo, ".lore", "data", schema.TableActors, who.String()+".json"))), "alice@example.com") {
				t.Fatal("the email reached a client repo's actor row")
			}
		})
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRepoClassDecidesF15: with an internal store attached, a repo memory
// pointing at the store's project is refused in a client repo and allowed
// in an internal one (the hook reaches the engine's F15 check).
func TestRepoClassDecidesF15(t *testing.T) {
	for _, client := range []bool{true, false} {
		repo := gitRepo(t)
		writeStoresYAML(t, repo, "stores:\n  - name: agency\n    url: "+folderStoreAt(t, folderStore, rowfs.VisibilityInternal)+"\n")
		a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return client, nil }})
		withProject(t, a)
		name, err := value.TextOf("agency-project")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Create(background(), "agency", schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
			t.Fatal(err)
		}
		agPrj := projectIn(t, a, "agency")
		fields := map[string]value.Value{}
		for k, v := range map[string]string{"project_id": agPrj, "kind": schema.MemoryKindCore, "body": "points inside", "source_kind": "manual"} {
			if fields[k], err = value.TextOf(v); err != nil {
				t.Fatal(err)
			}
		}
		_, err = a.Create(background(), store.SourceRepo, schema.TableMemories, fields)
		switch {
		case client && !errs.Has(err, errs.LeakBlocked):
			t.Fatalf("client repo → internal project: %v, want E_LEAK_BLOCKED", err)
		case !client && err != nil:
			t.Fatalf("internal repo → internal project refused: %v", err)
		}
	}
}

// projectIn returns the id of the first live project of store n.
func projectIn(t *testing.T, a *App, n store.Source) string {
	t.Helper()
	rows, err := a.List(background(), n, schema.TableProjects)
	if err != nil || len(rows) == 0 {
		t.Fatalf("no project in %s: %v", n, err)
	}
	return rows[0].ID.String()
}
