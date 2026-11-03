package lsync

import (
	"context"
	"reflect"
	"testing"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/merge3"
)

func TestTableSynced(t *testing.T) {
	for _, name := range []string{"memories", "tasks", "decisions", "rules"} {
		if !TableSynced(name) {
			t.Errorf("%s must be synced", name)
		}
	}
	for _, name := range []string{"", "Memories", "memories ", "_lore_sync_dirty", "_lore_sync_trash", "sqlite_master", "no_such_table", "日本"} {
		if TableSynced(name) {
			t.Errorf("%q must not be synced", name)
		}
	}
}

func purgedIDs(t *testing.T, b []byte) []string {
	t.Helper()
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
	list, _ := doc[purgedKeyIDs].([]any)
	out := []string{}
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

// AddPurged is a set union in canonical (sorted) form, so two branches
// that each purge an id merge to the same bytes.
func TestAddPurged(t *testing.T) {
	cases := []struct {
		name     string
		existing []byte
		ids      []string
		want     []string
	}{
		{"absent, nothing", nil, nil, []string{}},
		{"absent, one", nil, []string{"mem_b"}, []string{"mem_b"}},
		{"empty bytes count as absent", []byte{}, []string{"mem_a"}, []string{"mem_a"}},
		{"dedupe and sort", nil, []string{"mem_c", "mem_a", "mem_c", "mem_b"}, []string{"mem_a", "mem_b", "mem_c"}},
		{"union with existing", []byte(`{"ids":["mem_z","mem_a"],"_v":1}`), []string{"mem_m", "mem_a"}, []string{"mem_a", "mem_m", "mem_z"}},
		{"non-string entries are dropped", []byte(`{"ids":["mem_a",3,null,{"x":1}],"_v":1}`), nil, []string{"mem_a"}},
		{"no ids key", []byte(`{"_v":1}`), []string{"mem_a"}, []string{"mem_a"}},
		{"unicode ids", nil, []string{"mem_日本"}, []string{"mem_日本"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := AddPurged(tc.existing, tc.ids)
			if err != nil {
				t.Fatal(err)
			}
			if got := purgedIDs(t, b); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ids = %v, want %v", got, tc.want)
			}
			again, err := AddPurged(b, tc.ids)
			if err != nil || string(again) != string(b) {
				t.Fatalf("adding the same ids again is a no-op: %q vs %q (%v)", again, b, err)
			}
		})
	}
	a, _ := AddPurged(nil, []string{"mem_1", "mem_2"})
	b, _ := AddPurged(nil, []string{"mem_2", "mem_1"})
	if string(a) != string(b) {
		t.Fatal("order of ids must not change the bytes")
	}
	if _, err := AddPurged([]byte("not json"), []string{"mem_a"}); err == nil {
		t.Fatal("a corrupt existing file is an error, never silently replaced")
	}
}

// RecordLive marks a version as already in lore.db, so LiveApply does not
// import it again; a changed live version is still imported.
func TestRecordLive(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.addMemory("Buttons are blue")
	e.reconcile()
	b, ok, err := RowDoc(ctx, e.db, "memories", id)
	if err != nil || !ok {
		t.Fatal(err)
	}
	k := LiveKey{Table: "memories", ID: id}
	d := LiveDoc{Body: b}
	if err := RecordLive(ctx, e.db, k, d); err != nil {
		t.Fatal(err)
	}
	states, err := LiveStates(ctx, e.db)
	if err != nil || states[k].Hash != LiveRowHash(d) || states[k].Deleted {
		t.Fatalf("recorded: %+v %v", states[k], err)
	}
	rep, err := LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: d}})
	if err != nil || rep.Changed() {
		t.Fatalf("a recorded version is not re-imported: %+v %v", rep, err)
	}
	// Upsert: recording again replaces, never duplicates.
	tomb := LiveDoc{Deleted: true}
	if err := RecordLive(ctx, e.db, k, tomb); err != nil {
		t.Fatal(err)
	}
	if err := RecordLive(ctx, e.db, k, tomb); err != nil {
		t.Fatal(err)
	}
	states, _ = LiveStates(ctx, e.db)
	if len(states) != 1 || !states[k].Deleted || states[k].Hash != LiveRowHash(tomb) {
		t.Fatalf("one state per row, last write wins: %+v", states)
	}
}

// ImportMissing imports only rows lore.db does not hold, and skips
// invalid, unknown and conflict-marked documents.
func TestImportMissing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	keep := e.addMemory("Run the tests before you push")
	gone := e.addMemory("The client replies faster on WhatsApp than email")
	marked := e.addMemory("Never put passwords in the code")
	e.reconcile()
	docOf := func(id string) []byte {
		b, ok, err := RowDoc(ctx, e.db, "memories", id)
		if err != nil || !ok {
			t.Fatalf("doc %s: %v", id, err)
		}
		return b
	}
	keepDoc, goneDoc, markedDoc := docOf(keep), docOf(gone), docOf(marked)
	m, err := canonjson.Decode(markedDoc, canonjson.DefaultMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	m["body"] = merge3.MarkConflict("ours", "theirs")
	markedDoc, err = canonjson.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{gone, marked} {
		if _, err := e.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	// keepDoc but edited: must NOT overwrite the row lore.db holds.
	km, _ := canonjson.Decode(keepDoc, canonjson.DefaultMaxDepth)
	km["body"] = "edited elsewhere"
	keepEdited, _ := canonjson.Encode(km)

	docs := [][]byte{
		nil, []byte("not json"), []byte(`{"_table":"no_such","id":"x_1"}`), []byte(`{"_table":"memories"}`),
		keepEdited, markedDoc, goneDoc,
	}
	got, err := ImportMissing(ctx, e.db, docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (LiveKey{Table: "memories", ID: gone}) {
		t.Fatalf("imported %+v, want only %s", got, gone)
	}
	if !e.memoryExists(gone) || e.memoryBody(gone) != "The client replies faster on WhatsApp than email" {
		t.Fatal("the missing row is back with its body")
	}
	if e.memoryBody(keep) != "Run the tests before you push" {
		t.Fatal("a row lore.db holds is never overwritten")
	}
	if e.memoryExists(marked) {
		t.Fatal("a conflict-marked document is never imported")
	}
	again, err := ImportMissing(ctx, e.db, docs)
	if err != nil || len(again) != 0 {
		t.Fatalf("a second run imports nothing: %+v %v", again, err)
	}
	if none, err := ImportMissing(ctx, e.db, nil); err != nil || len(none) != 0 {
		t.Fatalf("no docs: %+v %v", none, err)
	}
}
