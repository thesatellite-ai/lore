package wire

// App.RefusedRows (kinds_refused.go, RES 3.13) over a folder store clone,
// not the repo: a kinds and a kind_fields row whose field holds conflict
// markers are refused on import yet returned by RefusedRows for their own
// table only; a row whose markers sit only inside a meta entry is refused
// too but is not a definition anyone can read, so it stays out.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refusedConflict is a field value as an unresolved git merge leaves it.
const refusedConflict = "<<<<<<< ours\nweb hosts\n=======\nall hosts\n>>>>>>> theirs"

// refusedMetaKey is an arbitrary meta key id for the meta-only case,
// built at run time from testActor's hex (no key-shaped literal in source).
var refusedMetaKey = "mky_" + strings.TrimPrefix(testActor, "act_")

// storeRowFile finds the one row file of id under the store folder dir.
func storeRowFile(t *testing.T, dir, table, id string) string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, werr error) error {
		if werr == nil && !d.IsDir() && d.Name() == id+".json" && filepath.Base(filepath.Dir(p)) == table {
			hits = append(hits, p)
		}
		return werr
	})
	if err != nil || len(hits) != 1 {
		t.Fatalf("row file %s/%s in %s: %v %v", table, id, dir, hits, err)
	}
	return hits[0]
}

// editRowFile rewrites the JSON object of the row file at p with edit.
func editRowFile(t *testing.T, p string, edit func(doc map[string]json.RawMessage)) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	if b, err = json.MarshalIndent(doc, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// rawJSON marshals v for a row file edit.
func rawJSON(t *testing.T, v map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRefusedRowsInStoreClone(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	idOfKind := func(in string) string {
		t.Helper()
		out, err := run(t, a, settings.OpKindAdd, in, true)
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, out)
	}
	host := idOfKind(`{"kind":"host","to":"northwind"}`)
	box := idOfKind(`{"kind":"box","to":"northwind"}`)
	out, err := run(t, a, settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text","to":"northwind"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	fld := idOf(t, out)
	desc, err := json.Marshal(refusedConflict)
	if err != nil {
		t.Fatal(err)
	}
	marked := func(doc map[string]json.RawMessage) { doc["description"] = desc }
	editRowFile(t, storeRowFile(t, storeDir, schema.TableKinds, host), marked)
	editRowFile(t, storeRowFile(t, storeDir, schema.TableKindFields, fld), marked)
	// box: markers only inside a meta entry (its fields are clean)
	editRowFile(t, storeRowFile(t, storeDir, schema.TableKinds, box), func(doc map[string]json.RawMessage) {
		meta, merr := json.Marshal(map[string]json.RawMessage{refusedMetaKey: rawJSON(t, map[string]string{"name": "owner", "scope": "global", "v": refusedConflict})})
		if merr != nil {
			t.Fatal(merr)
		}
		doc["meta"] = meta
	})

	b := newTestApp(t, repo) // a fresh open: refresh reads the edited files
	ctx := background()
	src := store.Source("northwind")
	refusedIDs := func(table row.Table) []string {
		t.Helper()
		rows, rerr := b.RefusedRows(ctx, src, table)
		if rerr != nil {
			t.Fatal(rerr)
		}
		var got []string
		for _, r := range rows {
			if r.Table != table {
				t.Errorf("RefusedRows(%s) returned a %s row", table, r.Table)
			}
			got = append(got, r.ID.String())
		}
		slices.Sort(got)
		return got
	}
	if got := refusedIDs(schema.TableKinds); !slices.Equal(got, []string{host}) {
		t.Errorf("refused kinds = %v, want only %s (box's markers are meta-only)", got, host)
	}
	if got := refusedIDs(schema.TableKindFields); !slices.Equal(got, []string{fld}) {
		t.Errorf("refused kind_fields = %v, want %s", got, fld)
	}
	if got := refusedIDs(schema.TableResources); len(got) != 0 {
		t.Errorf("refused resources = %v, want none", got)
	}
	if got, rerr := b.RefusedRows(ctx, store.SourceRepo, schema.TableKinds); rerr != nil || len(got) != 0 {
		t.Errorf("repo has no refused kinds: %v %v", got, rerr)
	}
	if got, rerr := b.RefusedRows(ctx, store.Source("nowhere"), schema.TableKinds); rerr != nil || len(got) != 0 {
		t.Errorf("an unknown store: %v %v", got, rerr)
	}
	// the refused host row is returned as stored, markers and all
	rows, err := b.RefusedRows(ctx, src, schema.TableKinds)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	if v := rows[0].Fields["description"]; v == nil || !strings.Contains(v.String(), "<<<<<<<") {
		t.Errorf("refused row not as stored: %v", rows[0].Fields)
	}
}
