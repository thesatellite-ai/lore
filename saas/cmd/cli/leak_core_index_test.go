package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
)

// TestLeakIndexesClass pins which store classes feed the leak index for a
// client repo and for any other checked repo (v0.1.17's split).
func TestLeakIndexesClass(t *testing.T) {
	cases := []struct {
		class  direction.Class
		client bool
		want   bool
	}{
		{direction.Personal, true, true},
		{direction.Personal, false, true},
		{direction.Internal, true, true},
		{direction.Internal, false, false},
		{direction.Client, true, false},
		{direction.Client, false, false},
		{direction.Class(""), true, false},
		{direction.Class("repo"), true, false},
		{direction.Class("bogus"), true, false},
	}
	for _, tc := range cases {
		if got := leakIndexesClass(tc.class, tc.client); got != tc.want {
			t.Errorf("leakIndexesClass(%q, client=%v) = %v, want %v", tc.class, tc.client, got, tc.want)
		}
	}
}

// testRow builds a live row of table with text fields.
func testRow(t *testing.T, id string, fields map[string]string) row.Row {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{ID: aid, Fields: map[string]value.Value{}}
	for k, v := range fields {
		tv, err := value.TextOf(v)
		if err != nil {
			t.Fatal(err)
		}
		r.Fields[k] = tv
	}
	return r
}

// TestLeakIndexRows: prose fields become index texts, id-holding fields do
// not, tombstones are skipped, and the indexed text is what the text rule
// then matches in a client file.
func TestLeakIndexRows(t *testing.T) {
	const (
		memID   = "mem_01a1a67a33b7777587cca0cfc9e5d82b"
		taskID  = "tsk_01a1a67a33b7777587cca0cfc9e5d82c"
		deadID  = "rul_01a1a67a33b7777587cca0cfc9e5d82d"
		clientT = "tsk_01a1a67a33b7777587cca0cfc9e5d8ff"
		secret  = "client is slow to pay so always send the estimate before the call"
	)
	dead := testRow(t, deadID, map[string]string{"body": "a deleted rule body with many words in it here"})
	dead.Deleted = true
	rows := []row.Row{
		testRow(t, memID, map[string]string{"body": secret}),
		testRow(t, taskID, map[string]string{"title": "chase the invoice", "description": "ünïcödé notes about the client call", "parent_task_id": clientT}),
		testRow(t, "rul_01a1a67a33b7777587cca0cfc9e5d8aa", nil), // no prose: id only
		dead,
	}
	got := leakIndexRows(rows, row.Table("memories"), "agency", leakStoreLabelPrefix+"agency")
	if len(got) != 3 {
		t.Fatalf("want 3 live rows, got %d: %+v", len(got), got)
	}
	if got[0].ID != memID || got[0].Source != "agency" || got[0].Label != "store agency" || len(got[0].Texts) != 1 || got[0].Texts[0] != secret {
		t.Fatalf("memory row: %+v", got[0])
	}
	for _, s := range got[1].Texts {
		if strings.Contains(s, clientT) {
			t.Fatalf("an id-holding field must not be indexed text: %+v", got[1])
		}
	}
	if len(got[1].Texts) != 2 || len(got[2].Texts) != 0 {
		t.Fatalf("texts: %+v / %+v", got[1].Texts, got[2].Texts)
	}
	for _, r := range got {
		if r.ID == deadID {
			t.Fatal("a tombstone is not indexed")
		}
	}

	// End to end through leakcheck: the internal text in a client file blocks,
	// the client task id an internal row points at does not.
	idx := leakcheck.NewIndex(got, nil)
	hit := leakcheck.Blocking(leakcheck.Check([]leakcheck.Item{{Where: "README.md", Content: []byte("Note: " + secret + ".")}}, idx, leakcheck.Options{}))
	if len(hit) == 0 {
		t.Fatal("internal text copied into a client file must block")
	}
	clean := leakcheck.Blocking(leakcheck.Check([]leakcheck.Item{{Where: "README.md", Content: []byte("see " + clientT)}}, idx, leakcheck.Options{}))
	if len(clean) != 0 {
		t.Fatalf("a client id named by an internal row is not internal: %v", clean)
	}
}

// fakeLeakStores is a leakStores over in-memory stores: classes by name,
// rows by store (every table answers the same rows, tagged by table), and
// optional failures per call.
type fakeLeakStores struct {
	idents     []wire.StoreIdentity
	classes    map[store.Source]direction.Class
	rows       map[store.Source][]row.Row
	identsErr  error
	classErr   map[store.Source]error
	listErr    map[store.Source]error
	listedFrom map[store.Source]bool
}

func (f *fakeLeakStores) StoreIdentities(context.Context) ([]wire.StoreIdentity, error) {
	return f.idents, f.identsErr
}

func (f *fakeLeakStores) StoreClass(_ context.Context, name store.Source) (direction.Class, error) {
	if err := f.classErr[name]; err != nil {
		return "", err
	}
	return f.classes[name], nil
}

func (f *fakeLeakStores) List(_ context.Context, src store.Source, t row.Table) ([]row.Row, error) {
	if f.listedFrom == nil {
		f.listedFrom = map[store.Source]bool{}
	}
	f.listedFrom[src] = true
	if err := f.listErr[src]; err != nil && t == schema.TableRules {
		return nil, err
	}
	if t != schema.TableMemories {
		return nil, nil
	}
	return f.rows[src], nil
}

// leakWorld is one store of every class (plus a shareable internal store
// and an odd unicode-named personal store), each holding one memory.
func leakWorld(t *testing.T) *fakeLeakStores {
	t.Helper()
	f := &fakeLeakStores{classes: map[store.Source]direction.Class{}, rows: map[store.Source][]row.Row{}}
	add := func(name store.Source, class direction.Class, shareable bool, id string) {
		f.idents = append(f.idents, wire.StoreIdentity{Name: name, Shareable: shareable})
		f.classes[name] = class
		f.rows[name] = []row.Row{testRow(t, id, map[string]string{"body": "a note that lives in " + string(name) + " only"})}
	}
	add(store.SourceRepo, direction.Class("repo"), false, "mem_01a1a67a33b7777587cca0cfc9e50001")
	add("agency", direction.Internal, false, "mem_01a1a67a33b7777587cca0cfc9e50002")
	add("me", direction.Personal, false, "mem_01a1a67a33b7777587cca0cfc9e50003")
	add("acme", direction.Client, false, "mem_01a1a67a33b7777587cca0cfc9e50004")
	add("handbook", direction.Internal, true, "mem_01a1a67a33b7777587cca0cfc9e50005")
	add("日記", direction.Personal, false, "mem_01a1a67a33b7777587cca0cfc9e50006")
	add("unclassed", direction.Class(""), false, "mem_01a1a67a33b7777587cca0cfc9e50007")
	return f
}

// TestLeakRowsFrom_StoreChoice: which attached stores feed the index, for
// a client repo and for any other checked repo (CFG6: a shareable store is
// never internal; repo and client stores are what the client reads).
func TestLeakRowsFrom_StoreChoice(t *testing.T) {
	cases := []struct {
		client bool
		want   []string
	}{
		{true, []string{"agency", "me", "日記"}},
		{false, []string{"me", "日記"}},
	}
	for _, tc := range cases {
		f := leakWorld(t)
		got, err := leakRowsFrom(context.Background(), f, tc.client)
		if err != nil {
			t.Fatalf("client=%v: %v", tc.client, err)
		}
		var srcs []string
		for _, r := range got {
			srcs = append(srcs, r.Source)
			if r.Label != leakStoreLabelPrefix+r.Source || r.Table != string(schema.TableMemories) || len(r.Texts) != 1 {
				t.Errorf("client=%v: index row %+v", tc.client, r)
			}
		}
		if strings.Join(srcs, ",") != strings.Join(tc.want, ",") {
			t.Errorf("client=%v: indexed stores %v, want %v", tc.client, srcs, tc.want)
		}
		if f.listedFrom["handbook"] || f.listedFrom[store.SourceRepo] || f.listedFrom["acme"] {
			t.Errorf("client=%v: a store outside the index was read: %v", tc.client, f.listedFrom)
		}
	}
}

// TestLeakRowsFrom_FailsClosed: an internal store that cannot be read
// refuses the check (E_LEAK_BLOCKED, cannot check) instead of counting as
// empty; a failure in a store that is not indexed changes nothing.
func TestLeakRowsFrom_FailsClosed(t *testing.T) {
	boom := errors.New("disk on fire")
	cases := []struct {
		name    string
		client  bool
		mutate  func(*fakeLeakStores)
		refused bool
	}{
		{"store list fails", true, func(f *fakeLeakStores) { f.identsErr = boom }, true},
		{"store list fails, own repo", false, func(f *fakeLeakStores) { f.identsErr = boom }, true},
		{"internal class unreadable", true, func(f *fakeLeakStores) { f.classErr = map[store.Source]error{"agency": boom} }, true},
		{"internal table unlistable", true, func(f *fakeLeakStores) { f.listErr = map[store.Source]error{"agency": boom} }, true},
		{"personal table unlistable", false, func(f *fakeLeakStores) { f.listErr = map[store.Source]error{"me": boom} }, true},
		{"internal unlistable, own repo (not indexed)", false, func(f *fakeLeakStores) { f.listErr = map[store.Source]error{"agency": boom} }, false},
		{"client store unlistable (not indexed)", true, func(f *fakeLeakStores) { f.listErr = map[store.Source]error{"acme": boom} }, false},
		{"shareable store's class unreadable (skipped first)", true, func(f *fakeLeakStores) { f.classErr = map[store.Source]error{"handbook": boom} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := leakWorld(t)
			tc.mutate(f)
			got, err := leakRowsFrom(context.Background(), f, tc.client)
			if !tc.refused {
				if err != nil {
					t.Fatalf("want no refusal, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("an unreadable internal store must refuse, got %d rows", len(got))
			}
			if !errcodes.IsCode(err, errcodes.LeakBlocked) || !strings.Contains(err.Error(), "cannot check") {
				t.Fatalf("want E_LEAK_BLOCKED cannot check, got %v", err)
			}
			if got != nil {
				t.Fatalf("a refusal returns no partial index: %v", got)
			}
		})
	}
}
