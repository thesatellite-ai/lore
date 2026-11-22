package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// tagIDsOf is the tag ids a row holds, in order.
func tagIDsOf(t *testing.T, e *Engine, src store.Source, id ids.AnyID) []string {
	t.Helper()
	_, got, err := e.Get(context.Background(), Ref{Store: src, Table: schema.TableMemories, ID: id})
	if err != nil {
		t.Fatalf("get %s in %s: %v", id, src, err)
	}
	l, ok := got.Fields[schema.FieldTagIDs].(value.List)
	if !ok {
		t.Fatalf("%s has no tag_ids list: %#v", id, got.Fields[schema.FieldTagIDs])
	}
	var out []string
	for _, v := range l.Items() {
		out = append(out, v.String())
	}
	return out
}

// tagNamesIn maps tag id to name for every live tag of store name.
func tagNamesIn(t *testing.T, e *Engine, name store.Source) map[string]string {
	t.Helper()
	rows, err := e.List(context.Background(), name, schema.TableTags)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.ID.String()] = str(r.Fields[tagNameField])
	}
	return out
}

// TestBulkMoveCopiesTagsIntoTarget is devco D-891 (bug hunt H-152): a
// bulk move of tagged rows out of a place left their tag ids pointing at
// the source place's tags, so the next move from a checkout that reads
// only the target was refused "tag_ids refers to rows no attached store
// holds". Each moved row now holds the target's tag of the same name,
// written once for every row that shares it (no unique-key clash inside
// the batch); the source keeps its tags for the rows that stay; the dry
// run plans the same copies and writes none.
func TestBulkMoveCopiesTagsIntoTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newBulkWorld(t)
	dstPrj, err := w.eng.Create(ctx, bulkDst, schema.TableProjects, map[string]value.Value{"name": txt("new")})
	if err != nil {
		t.Fatal(err)
	}
	tag := func(name string) ids.AnyID {
		return mustCreate(t, w.eng, bulkSrc, schema.TableTags, map[string]value.Value{"project_id": mustRef(t, w.prj), "name": txt(name), "color": txt("#112233")})
	}
	billing, urgent := tag("billing"), tag("urgent")
	mem := func(body string, tags ...ids.AnyID) ids.AnyID {
		return mustCreate(t, w.eng, bulkSrc, schema.TableMemories, map[string]value.Value{
			"project_id": mustRef(t, w.prj), "kind": txt(schema.MemoryKindCore), "body": txt(body), "source_kind": txt("manual"),
			schema.FieldTagIDs: refList(t, tags...)})
	}
	a, b := mem("rounding happens at the line", billing, urgent), mem("totals are in cents", billing)
	refs := []Ref{{Store: bulkSrc, Table: schema.TableMemories, ID: a}, {Store: bulkSrc, Table: schema.TableMemories, ID: b}}

	plan, err := w.eng.MoveMany(ctx, refs, bulkDst, MoveOptions{Plan: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	copied := 0
	for _, r := range plan.Rows {
		for _, wn := range r.Warnings {
			if wn.Code == WarnTagCopied {
				copied++
			}
		}
	}
	if copied != 2 {
		t.Errorf("the dry run planned %d tag copies, want 2 (billing once, urgent once): %+v", copied, plan.Rows)
	}
	if n := len(tagNamesIn(t, w.eng, bulkDst)); n != 0 {
		t.Fatalf("the dry run wrote %d tag(s) into %s", n, bulkDst)
	}

	if _, err = w.eng.MoveMany(ctx, refs, bulkDst, MoveOptions{}); err != nil {
		t.Fatalf("move: %v", err)
	}
	dstTags := tagNamesIn(t, w.eng, bulkDst)
	if len(dstTags) != 2 {
		t.Fatalf("%s holds tags %v, want billing and urgent once each", bulkDst, dstTags)
	}
	for _, id := range []ids.AnyID{a, b} {
		for _, tid := range tagIDsOf(t, w.eng, bulkDst, id) {
			if _, ok := dstTags[tid]; !ok {
				t.Errorf("moved %s still points at %s, a tag %s does not hold (it holds %v)", id, tid, bulkDst, dstTags)
			}
		}
	}
	// a stored list comes back in id order: compare the names as a set
	got := tagIDsOf(t, w.eng, bulkDst, a)
	names := make([]string, 0, len(got))
	for _, id := range got {
		names = append(names, dstTags[id])
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"billing", "urgent"}) {
		t.Errorf("tags of %s after the move = %v (%v), want billing and urgent", a, got, dstTags)
	}
	rows, err := w.eng.List(ctx, bulkDst, schema.TableTags)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if str(r.Fields["project_id"]) != dstPrj.Row.ID.String() || str(r.Fields["color"]) != "#112233" {
			t.Errorf("copied tag %s = %v, want project %s and the source's color", r.ID, r.Fields, dstPrj.Row.ID)
		}
	}
	if src := tagNamesIn(t, w.eng, bulkSrc); src[billing.String()] != "billing" || src[urgent.String()] != "urgent" {
		t.Errorf("the source lost its tags: %v", src)
	}
}

// TestMoveKeepsTagNoStoreHolds is the repair path of D-891: rows an
// older lore moved out of a place keep tag ids only that place holds. A
// move from a checkout that does not read it was refused; it now moves
// the row, keeps those ids as they were and says so (W_TAG_UNSEEN).
func TestMoveKeepsTagNoStoreHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newTwoCheckouts(t)
	ownPrj := mustCreate(t, c.own, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("own")})
	orgPrj := mustCreate(t, c.own, agency, schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
	hidden := mustCreate(t, c.own, store.SourceRepo, schema.TableTags, map[string]value.Value{"project_id": refOf(t, ownPrj), "name": txt("billing")})
	// the shape an older move left: a row in the org store on a tag only
	// the own repo holds
	mem := mustCreate(t, c.own, agency, schema.TableMemories, map[string]value.Value{
		"project_id": refOf(t, orgPrj), "kind": txt(schema.MemoryKindCore), "body": txt("totals are in cents"), "source_kind": txt("manual"),
		schema.FieldTagIDs: refList(t, hidden)})

	got, err := c.client.Move(ctx, Ref{Store: agency, Table: schema.TableMemories, ID: mem}, personal)
	if err != nil {
		t.Fatalf("a move from a checkout that cannot see the tag was refused: %v", err)
	}
	unseen := false
	for _, wn := range got.Warnings {
		unseen = unseen || wn.Code == WarnTagUnseen && strings.Contains(wn.Message, hidden.String())
	}
	if !unseen {
		t.Errorf("no %s naming %s: %+v", WarnTagUnseen, hidden, got.Warnings)
	}
	if ids := tagIDsOf(t, c.client, personal, mem); len(ids) != 1 || ids[0] != hidden.String() {
		t.Errorf("tag_ids after the move = %v, want %s kept", ids, hidden)
	}
	if n := len(tagNamesIn(t, c.client, personal)); n != 0 {
		t.Errorf("a tag nobody can read was copied: %d tag(s) in %s", n, personal)
	}
}

// TestMoveListsMergedTagOnce: two source tags of one name (in two
// projects) become the one tag of that name in the target; the moved row
// lists it once, not twice (D-891).
func TestMoveListsMergedTagOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newBulkWorld(t)
	if _, err := w.eng.Create(ctx, bulkDst, schema.TableProjects, map[string]value.Value{"name": txt("new")}); err != nil {
		t.Fatal(err)
	}
	other := mustCreate(t, w.eng, bulkSrc, schema.TableProjects, map[string]value.Value{"name": txt("other")})
	one := mustCreate(t, w.eng, bulkSrc, schema.TableTags, map[string]value.Value{"project_id": mustRef(t, w.prj), "name": txt("billing")})
	two := mustCreate(t, w.eng, bulkSrc, schema.TableTags, map[string]value.Value{"project_id": mustRef(t, other), "name": txt("billing")})
	mem := mustCreate(t, w.eng, bulkSrc, schema.TableMemories, map[string]value.Value{
		"project_id": mustRef(t, w.prj), "kind": txt(schema.MemoryKindCore), "body": txt("totals are in cents"), "source_kind": txt("manual"),
		schema.FieldTagIDs: refList(t, one, two)})
	if _, err := w.eng.MoveMany(ctx, []Ref{{Store: bulkSrc, Table: schema.TableMemories, ID: mem}}, bulkDst, MoveOptions{}); err != nil {
		t.Fatalf("move: %v", err)
	}
	dst := tagNamesIn(t, w.eng, bulkDst)
	got := tagIDsOf(t, w.eng, bulkDst, mem)
	if len(dst) != 1 || len(got) != 1 || dst[got[0]] != "billing" {
		t.Errorf("tag_ids after the move = %v, target tags %v; want the one billing tag once", got, dst)
	}
}
