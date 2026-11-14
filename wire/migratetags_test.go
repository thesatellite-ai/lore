package wire

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// etgSeq makes every planted entity_tags row id distinct (the same link
// twice is two v0.1.17 rows).
var etgSeq int

// v1EntityTag writes a v0.1.17 entity_tags row file into repo.
func v1EntityTag(t *testing.T, repo, entityTable, entityID, tagID string) {
	t.Helper()
	etgSeq++
	id, err := ids.Natural(ids.PrefixEntityTag, entityTable, entityID, tagID, strconv.Itoa(etgSeq))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, ".lore", "data", entityTagsTable)
	if err = os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	doc := `{"_table":"entity_tags","_v":1,"created_at":"2026-11-04T08:09:56Z","entity_id":"` + entityID +
		`","entity_table":"` + entityTable + `","id":"` + id.String() + `","tag_id":"` + tagID + `","updated_at":"2026-11-04T08:09:56Z"}`
	if err = os.WriteFile(filepath.Join(dir, id.String()+".json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tagIDsOf reads the tag_ids of the repo row id.
func tagIDsOf(t *testing.T, a *App, id string) []string {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	rid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := eng.Get(background(), engine.Ref{Store: store.SourceRepo, ID: rid})
	if err != nil {
		t.Fatal(err)
	}
	return value.ItemStrings(r.Fields[schema.FieldTagIDs])
}

// TestMigrateTagsFlow (O11, CFG7 tags step): v0.1.17 entity_tags rows in
// the repo become each tagged row's tag_ids through the engine: the dry
// run lists the step per row and writes nothing; the run merges with the
// ids a row already lists (sorted, de-duplicated) for every tagged table
// (a memory and the project), reports a malformed link as skipped, and
// counts the rows written; a second run changes nothing a user can see
// (the same tag_ids). The entity_tags files stay for v0.1.17 readers.
func TestMigrateTagsFlow(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	project := projectID(t, a)
	tag := func(name string) string {
		out, err := run(t, a, opstag.OpAdd, `{"name":"`+name+`"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, out)
	}
	deploy, infra, ops := tag("deploy"), tag("infra"), tag("ops")
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"tagged note"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	mem := idOf(t, out)
	// the memory already lists ops (a 0.1.18 write before the migration)
	if _, err = run(t, a, opstag.OpAttach, `{"on_table":"memories","on_id":"`+mem+`","tag":"ops"}`, false); err != nil {
		t.Fatal(err)
	}
	before := tagIDsOf(t, a, mem)
	if !slices.Contains(before, ops) {
		t.Fatalf("fixture: memory tag_ids %v lack ops", before)
	}

	v1EntityTag(t, repo, "memories", mem, infra)
	v1EntityTag(t, repo, "memories", mem, deploy)
	v1EntityTag(t, repo, "memories", mem, deploy) // the same link twice
	v1EntityTag(t, repo, "projects", project, infra)
	v1EntityTag(t, repo, "memories", project, infra) // wrong prefix: skipped

	dry, err := a.migrateTags(ctx, true)
	steps, n := dry.Steps, dry.Rows
	if err != nil || n != 2 || len(dry.Upgraded) != 2 || len(dry.Detail) != 2 {
		t.Fatalf("dry run: %v %d %v %v, want 2 rows named", steps, n, dry.Upgraded, err)
	}
	if steps[0] != "tags: would fold 4 entity_tags links into the tag_ids of 2 rows (memories 1, projects 1)" {
		t.Fatalf("summary line %q", steps[0])
	}
	if got := tagIDsOf(t, a, mem); !slices.Equal(got, before) {
		t.Fatalf("the dry run wrote tag_ids: %v", got)
	}
	if !slices.ContainsFunc(steps, func(s string) bool { return strings.Contains(s, "skipped") }) {
		t.Fatalf("steps %v do not report the malformed link", steps)
	}

	if got, err := a.migrateTags(ctx, false); err != nil || got.Rows != 2 {
		t.Fatalf("run: %d %v", got.Rows, err)
	}
	want := []string{deploy, infra, ops}
	slices.Sort(want)
	if got := tagIDsOf(t, a, mem); !slices.Equal(got, want) {
		t.Fatalf("memory tag_ids = %v, want %v", got, want)
	}
	if got := tagIDsOf(t, a, project); !slices.Equal(got, []string{infra}) {
		t.Fatalf("project tag_ids = %v, want [%s]", got, infra)
	}

	if _, err = a.migrateTags(ctx, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := tagIDsOf(t, a, mem); !slices.Equal(got, want) {
		t.Fatalf("second run changed tag_ids: %v", got)
	}
	if ents, rerr := os.ReadDir(filepath.Join(repo, ".lore", "data", entityTagsTable)); rerr != nil || len(ents) != 5 {
		t.Fatalf("entity_tags files: %d %v, want all 5 kept", len(ents), rerr)
	}
}

// TestMigrateTagsOrphanLink (E-5): an entity_tags link to a row (or a tag)
// that no longer exists is skipped with a line naming its file and why;
// the other links still land and the step does not fail.
func TestMigrateTagsOrphanLink(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	out, err := run(t, a, opstag.OpAdd, `{"name":"infra"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	infra := idOf(t, out)
	if out, err = run(t, a, opsmemory.OpAdd, `{"body":"kept note"}`, false); err != nil {
		t.Fatal(err)
	}
	mem := idOf(t, out)
	gone := "mem_01a147bba77f702a810a5a30c1675789"    // hard-deleted by 0.1.17: no file
	goneTag := "tag_2dba3393d2487d919814327c57eacbea" // no such tag
	v1EntityTag(t, repo, "memories", mem, infra)
	v1EntityTag(t, repo, "memories", gone, infra)
	v1EntityTag(t, repo, "memories", mem, goneTag)
	dry, err := a.migrateTags(ctx, true)
	if err != nil || dry.Rows != 1 {
		t.Fatalf("dry run: %v %d %v, want 1 row and no error", dry.Steps, dry.Rows, err)
	}
	got, err := a.migrateTags(ctx, false)
	steps, n := got.Steps, got.Rows
	if err != nil || n != 1 || len(got.Upgraded) != 1 {
		t.Fatalf("run: %v %d %v, want 1 row and no error", steps, n, err)
	}
	// the summary counts only the link that landed, as the dry run did
	if want := "tags: folded 1 entity_tags link into the tag_ids of 1 row (memories 1)"; steps[0] != want {
		t.Fatalf("summary line %q, want %q", steps[0], want)
	}
	if want := "tags: would fold 1 entity_tags link into the tag_ids of 1 row (memories 1)"; dry.Steps[0] != want {
		t.Fatalf("dry summary line %q, want %q", dry.Steps[0], want)
	}
	for _, want := range []string{"the row " + gone + " no longer exists", "the tag " + goneTag + " no longer exists"} {
		if !slices.ContainsFunc(steps, func(s string) bool {
			return strings.HasPrefix(s, "tags: skipped entity_tags/etg_") && strings.Contains(s, want)
		}) {
			t.Fatalf("steps %v do not name the orphan link (%s)", steps, want)
		}
	}
	if got := tagIDsOf(t, a, mem); !slices.Equal(got, []string{infra}) {
		t.Fatalf("memory tag_ids = %v, want [%s]", got, infra)
	}
}
