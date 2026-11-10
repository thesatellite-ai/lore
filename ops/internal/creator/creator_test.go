package creator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// addCases is one minimal add input per noun whose created_by_actor_id
// column used to be settable by hand through --created-by (INTEG-AUDIT-1).
var addCases = []struct{ noun, in string }{
	{"behaviour", `{"name":"n","body":"b"}`},
	{"architecturenote", `{"title":"t","body":"b"}`},
	{"playbook", `{"title":"t","body":"b"}`},
	{"memory", `{"body":"b"}`},
	{"rule", `{"body":"b"}`},
	{"mission", `{"title":"t"}`},
	{"pattern", `{"title":"t","body":"b"}`},
	{"reminder", `{"message":"m","due":"2099-01-01"}`},
	{"tastepref", `{"body":"b"}`},
	{"decision", `{"title":"t","body":"b"}`},
	{"prompt", `{"title":"t","body":"b"}`},
	{"plan", `{"title":"t","body":"b"}`},
	{"incident", `{"title":"t","body":"b"}`},
	{"task", `{"title":"t"}`},
	{"hotfix", `{"title":"t","body":"b"}`},
	{"suggestion", `{"title":"t","body":"b"}`},
	{"cookbookrecipe", `{"title":"t","body":"b"}`},
}

// storedField reads one string member of the stored row file of id,
// whatever table it landed in ("" when absent).
func storedField(t *testing.T, repo, id, key string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repo, ".lore", "data", "*", id+".json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("row file of %s: %v %v", id, paths, err)
	}
	b, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var s string
	if raw, ok := m[key]; ok {
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	return s
}

// TestAddCreatorFromCaller (INTEG-AUDIT-1): no <noun>.add accepts a
// hand-set creator (no created_by input field, so no --created-by flag and
// no REST body key), and the new row's created_by_actor_id is the calling
// actor, the same one the engine stamps into the envelope's created_by.
func TestAddCreatorFromCaller(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	// The caller's actor row, as a real first write makes it (wire
	// ensureActor; the harness identity has no stable_key, so it is
	// written here): the column is a checked ref to it.
	me, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{}
	for k, v := range map[string]string{"display_name": "Mira", "kind": schema.ActorKindHuman} {
		if fields[k], err = value.TextOf(v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableActors, me.Any(), fields); err != nil {
		t.Fatal(err)
	}
	for _, c := range addCases {
		t.Run(c.noun, func(t *testing.T) {
			e, ok := h.App.Registry().Lookup(c.noun + ".add")
			if !ok {
				t.Fatalf("no op %s.add", c.noun)
			}
			for _, f := range e.InputFields() {
				if f.Name == "created_by" {
					t.Fatalf("%s.add still takes created_by (--created-by)", c.noun)
				}
			}
			id := h.ID(h.Must(c.noun+".add", c.in))
			if got := storedField(t, h.Repo, id, "created_by_actor_id"); got != opstest.Actor {
				t.Fatalf("%s created_by_actor_id = %q, want the caller %s", c.noun, got, opstest.Actor)
			}
			if got := storedField(t, h.Repo, id, "created_by"); got != opstest.Actor {
				t.Fatalf("%s envelope created_by = %q, want %s", c.noun, got, opstest.Actor)
			}
		})
	}
}
