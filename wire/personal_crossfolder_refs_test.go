package wire

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestPersonalRefsAcrossFolders pins devco D-793 (owner P-12): a note in
// one folder of the personal store naming a resource kept in another
// folder of it (me/journal -> me/work) gets its row_refs and row_expanded
// lines, though `memory add --to me/journal` opens me/journal alone (also
// when the note is written before the resource is added); and a
// cache whose references were expanded by an older binary (no refs stamp,
// no row_refs lines) gets them back on its next open, with no `lore search
// rebuild`. Before it the journal cache had no line at all, and an old
// cache kept none until a manual rebuild.
func TestPersonalRefsAcrossFolders(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	bare := filepath.Join(t.TempDir(), "me.git")
	gitIn(t, t.TempDir(), nil, "init", "-q", "--bare", "-b", "main", bare)
	dir := t.TempDir()
	a := newTestAppHome(t, dir, home)
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: opsstores.PersonalName, URL: bare, RepoOptional: true}); err != nil {
		t.Fatalf("store init me on a bare repo: %v", err)
	}
	do := func(app *App, name, in string) string {
		t.Helper()
		e, ok := app.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		res, err := app.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Yes: true})
		if err != nil {
			t.Fatalf("%s %s: %v", name, in, err)
		}
		out, err := res.JSON()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	note := func(body, to string) {
		t.Helper()
		in, err := json.Marshal(struct {
			Body   string `json:"body"`
			To     string `json:"to"`
			Create bool   `json:"create"`
		}{body, to, true})
		if err != nil {
			t.Fatal(err)
		}
		do(newTestAppHome(t, dir, home), "memory.add", string(in))
	}
	note("work folder exists", "me/work")
	note("journal folder exists", "me/journal")
	do(newTestAppHome(t, dir, home), opssettings.OpKindAdd, `{"kind":"person","to":"me/work"}`)
	do(newTestAppHome(t, dir, home), opssettings.OpResAdd, `{"kind":"person","name":"alice_ng","to":"me/work"}`)
	note("met ${res:person/alice_ng} about orbit", "me/journal")
	note("bare ${res:alice_ng} in the journal", "me/journal")
	// written before the person exists: the add in me/work opens me/work
	// alone, and the journal note fills in on the journal's next open
	note("lunch with ${res:person/bob_li} next week", "me/journal")
	do(newTestAppHome(t, dir, home), opssettings.OpResAdd, `{"kind":"person","name":"bob_li","to":"me/work"}`)

	journalRefs := func(when string) {
		t.Helper()
		out := do(newTestAppHome(t, dir, home), opssql.OpRun,
			`{"query":"SELECT e.text FROM row_refs r JOIN row_expanded e USING (entity_table, entity_id, field) ORDER BY e.text","store":"me/journal"}`)
		for _, want := range []string{"met alice_ng about orbit", "bare alice_ng in the journal", "lunch with bob_li next week"} {
			if !strings.Contains(out, want) {
				t.Errorf("[D-793] %s: me/journal has no row_refs/row_expanded line %q for the me/work resource:\n%s", when, want, out)
			}
		}
	}
	journalRefs("after memory add --to me/journal")

	// an older binary's cache: its references expanded by older rules (no
	// line for the cross-folder note) and never stamped
	var files []string
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "journal.db" {
			files = append(files, p)
		}
		return err
	})
	if err != nil || len(files) != 1 {
		t.Fatalf("the me/journal cache file under HOME: %v %v", files, err)
	}
	db, err := sql.Open("sqlite", files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM row_refs`, `DELETE FROM row_expanded`, `DELETE FROM "_refs_stamp"`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	journalRefs("on the first open of a cache an older binary expanded")
}
