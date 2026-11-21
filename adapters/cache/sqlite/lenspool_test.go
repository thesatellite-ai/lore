package sqlite

import (
	"context"
	"path/filepath"
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
