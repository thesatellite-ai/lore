package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRestoreRunsClientLeakCheck (DELETION 3.3 step 3 and 3.11, P3-DEL-1):
// a restore into the repo store asks Options.GuardRepoWrite like a create
// there, before anything is written, with the row's table and text as it
// comes back (the delete reason is not part of it) and explicit=true (the
// row already lives in this repo: no re-routing). A refusal (the leak
// check finding internal text that reached the org store since the
// delete) keeps the row in the trash with the guard's own code; once the
// guard allows, the same restore succeeds. A row restored into another
// store never asks the repo guard.
func TestRestoreRunsClientLeakCheck(t *testing.T) {
	const marker = "zephyr-internal-pricing"
	cases := []struct {
		name      string
		target    store.Source // where the memory lives
		leak      bool         // the guard refuses text holding marker
		wantAsked bool
		wantCode  errs.Code // "" = restored
	}{
		{"repo row, leak found: refused", store.SourceRepo, true, true, errs.LeakBlocked},
		{"repo row, clean: restored", store.SourceRepo, false, true, ""},
		{"org store row: repo guard not asked", rosterStore, true, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := background()
			orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
			leak := false // creates and deletes pass; the restore meets c.leak
			var asked []WriteQuery
			var explicitSeen []bool
			opts := Options{
				OrgStores: func(context.Context) ([]attach.Store, error) {
					return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
				},
				RepoIsClient: func(context.Context) (bool, error) { return true, nil },
				GuardRepoWrite: func(_ context.Context, q WriteQuery, explicit bool) error {
					if q.Table != schema.TableMemories {
						return nil
					}
					asked = append(asked, q)
					explicitSeen = append(explicitSeen, explicit)
					if leak && strings.Contains(q.Text, marker) {
						return errs.New(errs.LeakBlocked, "leak check: the row matches internal content")
					}
					return nil
				},
			}
			a := openWith(t, gitRepo(t), opts)
			pname, err := value.TextOf("leak-restore")
			if err != nil {
				t.Fatal(err)
			}
			p, err := a.Create(ctx, c.target, schema.TableProjects, map[string]value.Value{"name": pname})
			if err != nil {
				t.Fatal(err)
			}
			text := func(s string) value.Value {
				v, terr := value.TextOf(s)
				if terr != nil {
					t.Fatal(terr)
				}
				return v
			}
			m, err := a.Create(ctx, c.target, schema.TableMemories, map[string]value.Value{
				"project_id": text(p.Row.ID.String()), "kind": text(schema.MemoryKindCore),
				"body": text("we agreed " + marker + " for Q3"), "source_kind": text("manual"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Delete(engine.WithDeleteReason(ctx, "reason-not-checked"), engine.Ref{ID: m.Row.ID}); err != nil {
				t.Fatal(err)
			}
			asked, explicitSeen, leak = nil, nil, c.leak
			_, err = a.Restore(ctx, m.Row.ID.String())
			if (len(asked) > 0) != c.wantAsked {
				t.Fatalf("guard asked %d times, want asked=%v", len(asked), c.wantAsked)
			}
			if c.wantAsked {
				q := asked[0]
				if !strings.Contains(q.Text, marker) || strings.Contains(q.Text, "reason-not-checked") || !explicitSeen[0] {
					t.Fatalf("query = %+v explicit=%v; want the body, no delete reason, explicit", q, explicitSeen[0])
				}
			}
			eng, eerr := a.Engine(ctx)
			if eerr != nil {
				t.Fatal(eerr)
			}
			inTrash := func() bool { return errs.Has(eng.InTrash(ctx, engine.Ref{ID: m.Row.ID}), errs.InTrash) }
			if c.wantCode != "" {
				if !errs.Has(err, c.wantCode) {
					t.Fatalf("restore: %v, want %s", err, c.wantCode)
				}
				if !inTrash() {
					t.Fatal("a refused restore brought the row back")
				}
				leak = false // the internal text is gone: the same restore now passes
				if _, err = a.Restore(ctx, m.Row.ID.String()); err != nil {
					t.Fatalf("restore after the guard allows: %v", err)
				}
			} else if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if inTrash() {
				t.Fatal("row still in the trash after a passing restore")
			}
		})
	}
}
