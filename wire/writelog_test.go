package wire

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestWrittenLines: up to three files are named one by one (a store's
// name after its path, none for the repo's own rows); more are counted
// per folder; a store with no file per row is named by its store.
func TestWrittenLines(t *testing.T) {
	t.Parallel()
	repo := filepath.FromSlash("/r/.lore/data")
	few := []Written{{Store: "repo", File: filepath.Join(repo, "tasks", "tsk_1.json")}, {Store: "notes", File: filepath.FromSlash("/h/stores/acme/notes/x/tasks/tsk_2.json")}, {Store: "plain"}}
	want := []string{
		"lore: wrote " + filepath.Join(repo, "tasks", "tsk_1.json"),
		"lore: wrote " + filepath.FromSlash("/h/stores/acme/notes/x/tasks/tsk_2.json") + " (store notes)",
		"lore: wrote a row in store plain",
	}
	if got := WrittenLines(few); !slices.Equal(got, want) {
		t.Fatalf("few:\n%q\nwant\n%q", got, want)
	}
	var many []Written
	for _, id := range []string{"a", "b", "c", "d"} {
		many = append(many, Written{Store: "repo", File: filepath.Join(repo, "memories", "mem_"+id+".json")})
	}
	if got := WrittenLines(many); !slices.Equal(got, []string{"lore: wrote 4 row file(s) in " + repo}) {
		t.Fatalf("many: %q", got)
	}
	if WrittenLines(nil) != nil {
		t.Fatal("nothing written prints nothing")
	}
}

// TestWriteLogOnce: the same file written twice in one command is named
// once; a context without a log records nothing.
func TestWriteLogOnce(t *testing.T) {
	t.Parallel()
	ctx, l := WithWriteLog(context.Background())
	if writeLogOf(context.Background()) != nil || writeLogOf(ctx) != l {
		t.Fatal("the log is carried by its context only")
	}
	l.add(Written{Store: "repo", File: "/x"})
	l.add(Written{Store: "repo", File: "/x"})
	if got := l.Files(); len(got) != 1 {
		t.Fatalf("files = %v", got)
	}
}

// TestWriteLogSkipsSecretEvents (W-9): a secret.* audit event only names
// the row it is about (`secrets allow --file` names the project row) and
// writes no file, so it is not listed as written; a row write still is.
func TestWriteLogSkipsSecretEvents(t *testing.T) {
	t.Parallel()
	ctx, l := WithWriteLog(context.Background())
	r := &routedAudit{rowFile: func(store.Source, row.Table, ids.AnyID) string { return "/r/.lore/data/projects/prj_1.json" }}
	r.noteWrite(ctx, engine.Event{Action: engine.ActionSecretAllowAdded, Store: store.SourceRepo, Table: schema.TableProjects})
	if got := l.Files(); len(got) != 0 {
		t.Fatalf("a secret audit event was listed as a write: %v", got)
	}
	r.noteWrite(ctx, engine.Event{Action: engine.ActionCreate, Store: store.SourceRepo, Table: schema.TableProjects})
	if got := l.Files(); len(got) != 1 {
		t.Fatalf("a row write was not listed: %v", got)
	}
}
