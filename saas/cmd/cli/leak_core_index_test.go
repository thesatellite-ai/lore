package main

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"

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
