package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestKeptBytesCountsKeptAnswers (devco:D-887, issue #96 follow-up): the
// read server sizes its memory limits to what its idle pooled lenses keep,
// so KeptBytes grows with a kept answer's rows and text, and goes back to
// 0 when the rows move (sameShape drops the kept answers) and when reuse is
// turned off. Without the count the server's limits stayed fixed and a
// big store's kept answers alone passed them. Not parallel: the pool is
// process state.
func TestKeptBytesCountsKeptAnswers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	kind := must(ids.New[ids.Kind]())
	addResources := func(seq string, names ...string) {
		t.Helper()
		repo := f.open("repo.db")
		var rows []store.Row
		if seq == "1" {
			rows = append(rows, f.kindRow(kind, "server"))
		}
		for _, n := range names {
			rows = append(rows, f.resource(must(ids.New[ids.Resource]()), kind, n, nil))
		}
		f.apply(repo, store.SourceRepo, seq, rows...)
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
	long := strings.Repeat("x", 4000)
	addResources("1", "alpha-"+long, "bravo-"+long)
	srcs := []LensSource{{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")}}
	o := LensOptions{MaskSensitive: true, Tables: []string{"resources", "memories"}}
	const q = `SELECT name FROM resources_all ORDER BY name`

	stop := EnableLensReuse()
	if got := KeptBytes(); got != 0 {
		t.Fatalf("KeptBytes = %d with no lens, want 0", got)
	}
	l := must(OpenLensWith(ctx, srcs, o))
	_ = must(l.QueryKept(ctx, q, 0))
	_ = must(l.QueryKept(ctx, q, 0)) // a hit adds nothing
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	kept := KeptBytes()
	if kept < 2*4000 || kept > 2*(4000+1024) {
		t.Fatalf("KeptBytes = %d after keeping two rows of ~4,000 bytes of text, want about 8,000", kept)
	}

	addResources("2", "charlie")
	again := must(OpenLensWith(ctx, srcs, o)) // rows moved: same lens, kept answers dropped
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	if got := KeptBytes(); got != 0 {
		t.Fatalf("KeptBytes = %d after the rows moved, want 0 (the kept answers were dropped)", got)
	}
	stop()
	if got := KeptBytes(); got != 0 {
		t.Fatalf("KeptBytes = %d after reuse was turned off, want 0", got)
	}
}
