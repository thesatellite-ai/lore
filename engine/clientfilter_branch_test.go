package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestClientFilterIsTheGitBranchGuard (G-DECIDE-4, later 400): leakcheck
// cannot see git, so the engine's ClientFilter is the only guard for
// `_o.git.branch` in a client repo (PROV8). Every write path into the
// client repo keeps the branch only when the client's remote already has
// it (an upstream for that branch); an upstream for another branch does
// not count. The commit and state always stay.
func TestClientFilterIsTheGitBranchGuard(t *testing.T) {
	const branch = "mira/leaving-acme"
	upstreams := []struct {
		name string
		up   map[string]bool
		keep bool
	}{
		{"no upstream", nil, false},
		{"upstream for another branch", map[string]bool{"main": true}, false},
		{"upstream for this branch", map[string]bool{branch: true}, true},
	}
	paths := []struct {
		name  string
		write func(t *testing.T, w *f15World) row.Row
	}{
		{"create in the client repo", func(t *testing.T, w *f15World) row.Row {
			return w.memIn(t, store.SourceRepo, w.project(t, store.SourceRepo, "app"), "client row")
		}},
		{"move into the client repo (born here)", func(t *testing.T, w *f15World) row.Row {
			ctx := context.Background()
			w.project(t, store.SourceRepo, "app")
			prj := w.project(t, agency, "acme-hub")
			id, err := w.eng.ids.NewID(mustPrefix(t, w, schema.TableMemories))
			if err != nil {
				t.Fatal(err)
			}
			born, err := value.JSONOf([]byte(`{"cmd":"memory add","git":{"branch":"` + branch + `","commit":"` + provCommit + `","state":"branch"},"repo":"` + provRepo + `","store":"agency"}`))
			if err != nil {
				t.Fatal(err)
			}
			ref, err := value.RefOf(prj)
			if err != nil {
				t.Fatal(err)
			}
			r := row.Row{Table: schema.TableMemories, ID: id, HLC: hlc.Next(hlc.HLC{}, w.eng.wall(), testActor), Origin: born,
				Fields: map[string]value.Value{"project_id": ref, "kind": txt(schema.MemoryKindCore), "body": txt("born here"), "source_kind": txt("manual")}}
			sr, err := store.FromRow(r)
			if err != nil {
				t.Fatal(err)
			}
			if err = w.data[agency].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
				t.Fatal(err)
			}
			got, err := w.eng.Move(ctx, Ref{ID: id}, store.SourceRepo)
			if err != nil {
				t.Fatal(err)
			}
			return got.Row
		}},
	}
	for _, p := range paths {
		for _, u := range upstreams {
			t.Run(p.name+"/"+u.name, func(t *testing.T) {
				w := newProvWorld(t, fakeProv{upstream: u.up})
				o := p.write(t, w).Origin.String()
				if strings.Contains(o, branch) != u.keep {
					t.Fatalf("branch kept = %v, want %v: %s", !u.keep, u.keep, o)
				}
				if !strings.Contains(o, `"commit":"`+provCommit+`"`) || !strings.Contains(o, `"state":"branch"`) {
					t.Fatalf("commit or state lost: %s", o)
				}
				// nothing in the client store holds the dropped branch
				if !u.keep {
					rows, _, err := w.data[store.SourceRepo].Open(store.OpenOptions{}).Read(context.Background(), "")
					if err != nil {
						t.Fatal(err)
					}
					for _, x := range rows {
						if strings.Contains(string(x.Doc), branch) {
							t.Fatalf("client store holds the branch: %s", x.Doc)
						}
					}
				}
			})
		}
	}
}
