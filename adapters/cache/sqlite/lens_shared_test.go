package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestLensSharedPlainViewOncePerRow (devco D-757, owner P-7): a store's
// cache file is one per place, and every open that reads the place applies
// its rows under the name it gives the store (a repo's stores.yaml name,
// `--store me/<folder>`, the store clone's own name). A lens that attaches
// the file as one Shared source must list each row once under the plain
// table name as it does under "<table>_all", masked or not, for row tables
// and for the per-kind views alike. Before the fix `SELECT * FROM memories`
// read the attached table whole: every row once per name that filled it.
func TestLensSharedPlainViewOncePerRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	prj := newPrj()
	c := f.open("place.db")
	kind := must(ids.New[ids.Kind]())
	ipF := must(ids.New[ids.KindField]())
	rows := []store.Row{
		f.memory(newMem(), prj, "first personal note"),
		f.memory(newMem(), prj, "second personal note"),
		f.kindRow(kind, "server"),
		f.fieldRow(ipF, kind, "ip", "ip", ids.ID[ids.Kind]{}),
		f.resource(must(ids.New[ids.Resource]()), kind, "web_prod", map[ids.ID[ids.KindField]][2]string{ipF: {"ip", `"203.0.113.10"`}}),
	}
	// three opens filled the one file, each under its own name (the
	// kind tables come from the repo's kind rows, as in kindWorld)
	f.apply(c, store.SourceRepo, "k", rows[2:4]...)
	for _, name := range []store.Source{"me", "mine", "notes"} {
		f.apply(c, name, "1", rows...)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	src := LensSource{Name: "mine", Path: filepath.Join(f.dir, "place.db"), Shared: true}
	cases := []struct {
		query string
		want  string
	}{
		{`SELECT COUNT(*) FROM memories`, "2"},
		{`SELECT COUNT(*) FROM memories_all`, "2"},
		{`SELECT COUNT(*) FROM res_server`, "1"},
		{`SELECT COUNT(*) FROM res_server_all`, "1"},
		{`SELECT COUNT(*) FROM kinds`, "1"},
	}
	for _, masked := range []bool{false, true} {
		l, err := OpenLensWith(context.Background(), []LensSource{src}, LensOptions{MaskSensitive: masked})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			res, qerr := l.Query(context.Background(), tc.query)
			if qerr != nil {
				t.Fatalf("masked=%v %s: %v", masked, tc.query, qerr)
			}
			if got := res.Rows[0][0].String(); got != tc.want {
				t.Errorf("masked=%v %s = %s, want %s (each row once, not once per name that filled the file)", masked, tc.query, got, tc.want)
			}
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLensSharedReadsTheFolderKey (devco D-933, public issue #142): a
// store folder's file keeps its rows under the folder's key, not the
// attach name, so a Shared source that names its Key lists those rows
// (labelled with its own name) and none of an older lore's name-keyed
// copies.
func TestLensSharedReadsTheFolderKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	prj := newPrj()
	c := f.open("folder.db")
	rows := []store.Row{f.memory(newMem(), prj, "first note"), f.memory(newMem(), prj, "second note")}
	f.apply(c, "@sto_acme/main/app", "1", rows...)
	f.apply(c, "kb/app", "1", rows...) // an older lore's copy under an attach name
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	src := LensSource{Name: "notes", Key: "@sto_acme/main/app", Path: filepath.Join(f.dir, "folder.db"), Shared: true}
	l, err := OpenLensWith(context.Background(), []LensSource{src}, LensOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	for q, want := range map[string]string{
		`SELECT COUNT(*) FROM memories`:                            "2",
		`SELECT COUNT(*) FROM memories_all`:                        "2",
		`SELECT COUNT(*) FROM memories_all WHERE source = 'notes'`: "2",
	} {
		res, qerr := l.Query(context.Background(), q)
		if qerr != nil {
			t.Fatalf("%s: %v", q, qerr)
		}
		if got := res.Rows[0][0].String(); got != want {
			t.Errorf("%s = %s, want %s (the folder key's rows once, labelled with the name)", q, got, want)
		}
	}
}
