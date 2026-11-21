package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLensReuseRebuildsAfterAnyWrite (devco:D-833): with reuse on (the
// read server), a narrowed lens over unchanged files is handed out again,
// and a write to an attached file makes the next open build a new lens:
// a field marked sensitive after the first lens was built is masked by the
// next one (a reused lens would show it). Without reuse every open builds
// its own lens. Not parallel: reuse is process state.
func TestLensReuseRebuildsAfterAnyWrite(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repo := f.open("repo.db")
	kind, label := must(ids.New[ids.Kind]()), must(ids.New[ids.KindField]())
	f.apply(repo, store.SourceRepo, "1",
		f.kindRow(kind, "vault"),
		f.fieldRow(label, kind, "label", "text", ids.ID[ids.Kind]{}),
		f.resource(must(ids.New[ids.Resource]()), kind, "vault_prod", map[ids.ID[ids.KindField]][2]string{label: {"label", `"primary"`}}),
	)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	srcs := []LensSource{{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")}}
	o := LensOptions{MaskSensitive: true, Tables: []string{"resources", "memories"}}
	q := `SELECT json_extract(fields, '$."` + label.String() + `".v') FROM resources_all WHERE name = 'vault_prod'`
	labelOf := func(l *Lens) string {
		t.Helper()
		return must(l.Query(ctx, q)).Rows[0][0].String()
	}

	// Off (a one-shot process): two opens, two lenses.
	a, b := must(OpenLensWith(ctx, srcs, o)), must(OpenLensWith(ctx, srcs, o))
	if a == b {
		t.Fatal("lens reuse is on without EnableLensReuse")
	}
	_, _ = a.Close(), b.Close()

	stop := EnableLensReuse()
	defer stop()
	first := must(OpenLensWith(ctx, srcs, o))
	if got := labelOf(first); got != "primary" {
		t.Fatalf("label = %q, want primary", got)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again := must(OpenLensWith(ctx, srcs, o))
	if again != first {
		t.Fatal("an unchanged narrowed lens was built again instead of reused")
	}
	if got := labelOf(again); got != "primary" {
		t.Fatalf("reused lens label = %q, want primary", got)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}

	// Another connection marks the field sensitive: the next lens masks it.
	repo = f.open("repo.db")
	f.apply(repo, store.SourceRepo, "2", f.live(schema.TableKindFields, label.Any(), map[string]value.Value{
		fieldKindID: text(t, kind.String()), fieldName: text(t, "label"), "type": text(t, "text"), fieldSensitive: value.BoolOf(true),
	}))
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	after := must(OpenLensWith(ctx, srcs, o))
	defer func() { _ = after.Close() }()
	if after == first {
		t.Fatal("a lens was reused after a write to its file")
	}
	if got := labelOf(after); got != MaskedValue {
		t.Fatalf("after the field turned sensitive the lens shows %q, want %q", got, MaskedValue)
	}
}

// TestLensQueryKeptForgetsOnAnyWrite (devco:D-842): QueryKept on a pooled
// lens (the read server) hands out the answer it read first, even to the
// next command that reuses the lens; a write to an attached file makes the
// next open drop every kept answer (keeping the lens itself when the write
// only moved rows, D-848), so QueryKept reads the files again and a kept
// answer never outlives its rows. A lens that is not pooled (every
// one-shot command) reads every time, and a caller changing a kept answer
// does not change what the next caller gets. Not parallel: reuse is
// process state.
func TestLensQueryKeptForgetsOnAnyWrite(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	kind := must(ids.New[ids.Kind]())
	addResource := func(seq, name string) {
		t.Helper()
		repo := f.open("repo.db")
		rows := []store.Row{f.resource(must(ids.New[ids.Resource]()), kind, name, nil)}
		if seq == "1" {
			rows = append([]store.Row{f.kindRow(kind, "server")}, rows...)
		}
		f.apply(repo, store.SourceRepo, seq, rows...)
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
	addResource("1", "alpha")
	srcs := []LensSource{{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")}}
	o := LensOptions{MaskSensitive: true, Tables: []string{"resources", "memories"}}
	const q = `SELECT name FROM resources_all ORDER BY name`
	names := func(l *Lens) string {
		t.Helper()
		res := must(l.QueryKept(ctx, q, 0))
		var out []string
		for _, r := range res.Rows {
			out = append(out, r[0].String())
		}
		return strings.Join(out, ",")
	}

	// Off (a one-shot process): the same lens reads a write made after its
	// first QueryKept.
	off := must(OpenLensWith(ctx, srcs, o))
	if got := names(off); got != "alpha" {
		t.Fatalf("one-shot lens names = %q, want alpha", got)
	}
	addResource("2", "bravo")
	if got := names(off); got != "alpha,bravo" {
		t.Fatalf("a lens that is not pooled kept its answer: names = %q, want alpha,bravo", got)
	}
	_ = off.Close()

	stop := EnableLensReuse()
	defer stop()
	first := must(OpenLensWith(ctx, srcs, o))
	if got := names(first); got != "alpha,bravo" {
		t.Fatalf("pooled lens names = %q, want alpha,bravo", got)
	}
	// a caller changing the answer it got changes nothing kept
	mine := must(first.QueryKept(ctx, q, 0))
	mine.Rows[0][0] = text(t, "changed")
	mine.Rows = mine.Rows[:1]
	if got := names(first); got != "alpha,bravo" {
		t.Fatalf("a caller's change reached the kept answer: names = %q", got)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again := must(OpenLensWith(ctx, srcs, o))
	if again != first {
		t.Fatal("an unchanged narrowed lens was built again instead of reused")
	}
	// kept: the answer comes from the lens, not the files (a write in the
	// middle of one command is not read again by that command's lens)
	addResource("3", "charlie")
	if got := names(again); got != "alpha,bravo" {
		t.Fatalf("a pooled lens read QueryKept's statement again: names = %q, want the kept alpha,bravo", got)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	// the write only added a row: the next open keeps the lens (its views
	// are the same, D-848) but drops what it kept, so it reads the files
	after := must(OpenLensWith(ctx, srcs, o))
	defer func() { _ = after.Close() }()
	if after != first {
		t.Fatal("a lens whose file only gained a row was built again (D-848: only its kept answers are stale)")
	}
	if got := names(after); got != "alpha,bravo,charlie" {
		t.Fatalf("after a write QueryKept answered %q, want alpha,bravo,charlie", got)
	}
}

// TestLensReuseKeepsShapeAfterRowWrites (devco:D-848): a sync that adds a
// row to one folder used to rebuild the read server's whole lens (ATTACH
// parses every folder's schema again). Rows written since its build keep
// the lens when the views they decide come out the same, and every answer
// still reads the new rows; a write that changes a view (a source starts
// holding resources of a kind another source defines: a new kind arm)
// builds a new lens. Not parallel: reuse is process state.
func TestLensReuseKeepsShapeAfterRowWrites(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	kind := must(ids.New[ids.Kind]())
	write := func(name, seq string, rows ...store.Row) {
		t.Helper()
		c := f.open(name)
		f.apply(c, store.Source(name), seq, rows...)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("repo.db", "1", f.kindRow(kind, "server"), f.resource(must(ids.New[ids.Resource]()), kind, "alpha", nil))
	write("acme.db", "1", f.memory(newMem(), newPrj(), "first note"))
	srcs := []LensSource{{Name: "repo.db", Path: filepath.Join(f.dir, "repo.db")}, {Name: "acme.db", Path: filepath.Join(f.dir, "acme.db")}}
	o := LensOptions{MaskSensitive: true, Tables: []string{"resources", "memories", "res_server"}}
	read := func(l *Lens, q string) string {
		t.Helper()
		var out []string
		for _, r := range must(l.QueryKept(ctx, q, 0)).Rows {
			out = append(out, r[0].String())
		}
		return strings.Join(out, ",")
	}
	const notes = `SELECT body FROM memories_all ORDER BY 1`
	const servers = `SELECT name FROM res_server_all ORDER BY name`

	stop := EnableLensReuse()
	defer stop()
	first := must(OpenLensWith(ctx, srcs, o))
	before := read(first, notes)
	if !strings.Contains(before, "first note") || read(first, servers) != "alpha" {
		t.Fatalf("first lens: notes %q, servers %q", before, read(first, servers))
	}
	_ = first.Close()

	write("acme.db", "2", f.memory(newMem(), newPrj(), "second note"))
	again := must(OpenLensWith(ctx, srcs, o))
	if again != first {
		t.Fatal("a row added to one folder rebuilt the whole lens")
	}
	if got := read(again, notes); !strings.Contains(got, "second note") || !strings.Contains(got, "first note") {
		t.Fatalf("the kept lens answered %q: the new row is missing (a stale kept answer)", got)
	}
	_ = again.Close()

	// acme now holds a server too: res_server_all gains an arm over acme
	write("acme.db", "3", f.resource(must(ids.New[ids.Resource]()), kind, "bravo", nil))
	after := must(OpenLensWith(ctx, srcs, o))
	defer func() { _ = after.Close() }()
	if after == first {
		t.Fatal("a write that adds a kind arm kept the old views")
	}
	if got := read(after, servers); got != "alpha,bravo" {
		t.Fatalf("servers after the arm = %q, want alpha,bravo", got)
	}
}
