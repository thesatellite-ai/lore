package host

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestBadStoreRow: the store clone's pre-commit check accepts a valid row
// and refuses broken JSON, conflict markers, a row whose path names another
// id, and a field its table refuses, each with a line.
func TestBadStoreRow(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	text := func(s string) value.Value {
		v, verr := value.TextOf(s)
		if verr != nil {
			t.Fatal(verr)
		}
		return v
	}
	stamp := hlc.NewClock(actor, time.Now).Now()
	project, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := row.Encode(row.Row{Table: schema.TableMemories, ID: id.Any(), HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: actor, CreatedBy: actor, CreatedAt: stamp.Wall,
		Fields: map[string]value.Value{"body": text("run the linter"), "kind": text(schema.MemoryKindCore), "project_id": text(project.String()), "source_kind": text("manual")}})
	if err != nil {
		t.Fatal(err)
	}
	rel := rowfs.RowPath(schema.TableMemories, id.Any())
	if line, why := badStoreRow(reg, rel, doc); why != "" {
		t.Fatalf("a valid row was refused at line %d: %s", line, why)
	}
	if _, why := badStoreRow(reg, rel, []byte("{\n  \"body\": \n")); why == "" {
		t.Error("broken JSON was accepted")
	}
	if line, why := badStoreRow(reg, rel, []byte("{\n<<<<<<< ours\n}\n")); why == "" || line != 2 {
		t.Errorf("conflict markers: line %d, %q", line, why)
	}
	other, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	if _, why := badStoreRow(reg, rowfs.RowPath(schema.TableMemories, other.Any()), doc); !strings.Contains(why, "path names") {
		t.Errorf("a row under another id's name: %q", why)
	}
	bad := strings.Replace(string(doc), `"kind": "core"`, `"kind": "someday"`, 1)
	if bad == string(doc) {
		t.Fatalf("the test row has no kind line: %s", doc)
	}
	if line, why := badStoreRow(reg, rel, []byte(bad)); why == "" || line <= 1 || !strings.Contains(why, "kind") {
		t.Errorf("a bad kind: line %d, %q", line, why)
	}
	// D-18: the code repo's pre-commit refuses what the hand-edit pick-up
	// refuses (a changed field that is unknown or outside its set), at that
	// field's line; a valid row, a new file, a row lore restamped and a
	// file that is not a row pass.
	repoRel := loreDataPathspec + "/" + rel
	if _, why := invalidRepoRow(reg, repoRel, doc, doc); why != "" {
		t.Fatalf("an unchanged repo row was refused: %s", why)
	}
	extra := strings.Replace(string(doc), "\n}", ",\n  \"my_note\": \"x\"\n}", 1)
	if line, why := invalidRepoRow(reg, repoRel, []byte(extra), doc); !strings.Contains(why, "unknown field my_note") || line != strings.Count(extra[:strings.Index(extra, "my_note")], "\n")+1 {
		t.Errorf("unknown field: line %d, %q", line, why)
	}
	if _, why := invalidRepoRow(reg, repoRel, []byte(bad), doc); !strings.Contains(why, "kind") {
		t.Errorf("a bad kind in the code repo: %q", why)
	}
	if _, why := invalidRepoRow(reg, repoRel, []byte(extra), nil); why != "" {
		t.Errorf("a new file was checked as a hand edit: %q", why)
	}
	restamped := strings.Replace(extra, stamp.String(), hlc.NewClock(actor, func() time.Time { return time.Now().Add(time.Hour) }).Now().String(), 1)
	if restamped == extra {
		t.Fatalf("the test row has no _hlc line: %s", extra)
	}
	if _, why := invalidRepoRow(reg, repoRel, []byte(restamped), doc); why != "" {
		t.Errorf("a row lore restamped was checked as a hand edit: %q", why)
	}
	if _, why := invalidRepoRow(reg, loreDataPathspec+"/_meta.json", []byte(`{"my_note":1}`), []byte(`{}`)); why != "" {
		t.Errorf("a file that is not a row was checked: %q", why)
	}
}
