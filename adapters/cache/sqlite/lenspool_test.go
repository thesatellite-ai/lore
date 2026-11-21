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
// next open build a new lens whose QueryKept reads the files again, so a
// kept answer never outlives its rows. A lens that is not pooled (every
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
	// the write made the next open build a new lens, which reads the files
	after := must(OpenLensWith(ctx, srcs, o))
	defer func() { _ = after.Close() }()
	if after == first {
		t.Fatal("a lens was reused after a write to its file")
	}
	if got := names(after); got != "alpha,bravo,charlie" {
		t.Fatalf("after a write QueryKept answered %q, want alpha,bravo,charlie", got)
	}
}
