package wire

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestDefaultTargetMatrix (Options.DefaultTarget, CONFIG_GUIDE
// default-target rules): asked only for a create that names no target,
// with the row's table and text; its answer routes the row (an attached
// store), "" and repo keep the repo (then GuardRepoWrite runs, not
// explicit), an error refuses with nothing written, an unattached answer
// is refused, and an explicit target never asks.
func TestDefaultTargetMatrix(t *testing.T) {
	ctx := background()
	cases := []struct {
		name      string
		target    store.Source // the create's own target
		answer    store.Source
		answerErr error
		wantIn    store.Source // "" = refused
		wantAsked bool
		wantGuard bool
	}{
		{"routes to the org store", "", rosterStore, nil, rosterStore, true, false},
		{"empty answer keeps repo", "", "", nil, store.SourceRepo, true, true},
		{"repo answer keeps repo", "", store.SourceRepo, nil, store.SourceRepo, true, true},
		{"hook error refuses", "", "", errs.New(errs.PolicyDenied, "no"), "", true, false},
		{"unattached answer refused", "", "nowhere", nil, "", true, false},
		{"explicit target never asks", rosterStore, store.SourceRepo, nil, rosterStore, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
			var asked []WriteQuery
			guarded := 0
			opts := Options{
				OrgStores: func(context.Context) ([]attach.Store, error) {
					return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
				},
				RepoIsClient: func(context.Context) (bool, error) { return false, nil },
				DefaultTarget: func(_ context.Context, q WriteQuery) (store.Source, error) {
					asked = append(asked, q)
					return c.answer, c.answerErr
				},
				GuardRepoWrite: func(_ context.Context, q WriteQuery, explicit bool) error {
					if q.Table == schema.TableProjects && !explicit {
						guarded++
					}
					return nil
				},
			}
			a := openWith(t, gitRepo(t), opts)
			asked, guarded = nil, 0
			name, err := value.TextOf("routed-project-ü")
			if err != nil {
				t.Fatal(err)
			}
			w, err := a.Create(ctx, c.target, schema.TableProjects, map[string]value.Value{"name": name})
			if (len(asked) > 0) != c.wantAsked {
				t.Fatalf("asked %d times, want asked=%v", len(asked), c.wantAsked)
			}
			if c.wantAsked && (asked[0].Table != schema.TableProjects || !strings.Contains(asked[0].Text, "routed-project-ü")) {
				t.Fatalf("query = %+v", asked[0])
			}
			if (guarded > 0) != c.wantGuard {
				t.Fatalf("guard ran %d times, want %v", guarded, c.wantGuard)
			}
			eng, eerr := a.Engine(ctx)
			if eerr != nil {
				t.Fatal(eerr)
			}
			if c.wantIn == "" {
				if err == nil {
					t.Fatalf("created %s, want a refusal", w.Row.ID)
				}
				if c.answerErr != nil && !errors.Is(err, c.answerErr) {
					t.Fatalf("got %v, want the hook's error", err)
				}
				for _, src := range eng.Stores() {
					rows, lerr := eng.List(ctx, src, schema.TableProjects)
					if lerr != nil {
						t.Fatal(lerr)
					}
					if slices.ContainsFunc(rows, func(r row.Row) bool { return r.Fields["name"].String() == "routed-project-ü" }) {
						t.Fatalf("a refused create wrote into %s", src)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			src, _, gerr := eng.Get(ctx, engine.Ref{ID: w.Row.ID})
			if gerr != nil || src != c.wantIn {
				t.Fatalf("row in %s (%v), want %s", src, gerr, c.wantIn)
			}
		})
	}
}
