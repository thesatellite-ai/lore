package lsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AddPurged rewrites _purged.json; an existing file whose "ids" value is
// not a list must fail the rewrite, never come back holding only the new
// ids (the old purged ids would be importable again).
func TestAddPurgedRefusesAMalformedExistingList(t *testing.T) {
	cases := map[string]string{
		"ids is a string":      `{"ids":"a","_v":1}`,
		"ids is an object":     `{"ids":{"a":true},"_v":1}`,
		"not json":             `{"ids":[`,
		"ids is null":          `{"ids":null,"_v":1}`,
		"ids is a bare number": `{"ids":1,"_v":1}`,
	}
	for name, existing := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := AddPurged([]byte(existing), []string{"new"})
			if err == nil {
				t.Fatalf("want an error, got %s", b)
			}
			if !strings.Contains(err.Error(), PurgedFileName) {
				t.Fatalf("error should name %s: %v", PurgedFileName, err)
			}
		})
	}
}

// The happy paths keep every old id and add the new ones.
func TestAddPurgedUnionsExistingAndNewIDs(t *testing.T) {
	cases := map[string]struct {
		existing string
		want     []string
	}{
		"absent":          {"", []string{"new"}},
		"empty list":      {`{"ids":[],"_v":1}`, []string{"new"}},
		"no ids key":      {`{"_v":1}`, []string{"new"}},
		"old ids":         {`{"ids":["a","b"],"_v":1}`, []string{"a", "b", "new"}},
		"overlapping id":  {`{"ids":["new"],"_v":1}`, []string{"new"}},
		"unicode old id":  {`{"ids":["é-1"],"_v":1}`, []string{"new", "é-1"}},
		"empty string id": {`{"ids":[""],"_v":1}`, []string{"", "new"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := AddPurged([]byte(c.existing), []string{"new"})
			if err != nil {
				t.Fatal(err)
			}
			doc, err := decodeDoc(b)
			if err != nil {
				t.Fatal(err)
			}
			got, err := purgedIDList(doc)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// A row still on live whose file could not be read is handed in as
// Unreadable: it must keep its live bookkeeping and its lore.db copy, and
// main's landed copy must not replace it (it has not left live).
func TestLiveApplyUnreadableRowDoesNotLeaveLive(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.addMemory("live version")
	e.reconcile()
	cur, _, _ := RowDoc(ctx, e.db, "memories", id)
	k := LiveKey{Table: "memories", ID: id}
	if _, err := LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: {Body: cur}}}); err != nil {
		t.Fatal(err)
	}
	doc := e.readDoc("memories", id)
	doc["body"] = "older copy on main"
	e.writeDoc("memories", "landed", doc)
	landed, _ := os.ReadFile(e.file("memories", "landed"))
	_ = os.Remove(e.file("memories", "landed"))
	rep, err := LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{},
		Unreadable: map[LiveKey]bool{k: true},
		Landed:     func(LiveKey) ([]byte, bool) { return landed, true }})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Left) != 0 {
		t.Fatalf("an unreadable live row must not leave live: %+v", rep.Left)
	}
	if got := e.memoryBody(id); got != "live version" {
		t.Fatalf("lore.db copy must stay as is, got %q", got)
	}
	states, err := LiveStates(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := states[k]; !ok {
		t.Fatal("live bookkeeping for the unreadable row must be kept")
	}
}

// badPurgedFiles are _purged.json files whose id set cannot be read.
var badPurgedFiles = map[string]string{
	"ids is a string":  `{"ids":"mem_a","_v":1}`,
	"ids is an object": `{"ids":{"mem_a":true},"_v":1}`,
	"ids is null":      `{"ids":null,"_v":1}`,
	"ids is a number":  `{"ids":3,"_v":1}`,
}

// The merge driver never writes a union that lost one side's ids: a side
// with a malformed list fails the merge and ours stays as it was.
func TestMergePurgedRefusesAMalformedSide(t *testing.T) {
	good := `{"ids":["mem_z"],"_v":1}`
	for name, bad := range badPurgedFiles {
		for _, side := range []string{"ours", "theirs"} {
			t.Run(name+"/"+side, func(t *testing.T) {
				dir := t.TempDir()
				ours, theirs := good, bad
				if side == "ours" {
					ours, theirs = bad, good
				}
				paths := map[string]string{"base": "", "ours": ours, "theirs": theirs}
				for n, body := range paths {
					if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				_, err := MergeFiles(context.Background(), filepath.Join(dir, "base"), filepath.Join(dir, "ours"), filepath.Join(dir, "theirs"), ".lore/data/"+PurgedFileName)
				if err == nil {
					t.Fatal("want an error")
				}
				got, _ := os.ReadFile(filepath.Join(dir, "ours"))
				if string(got) != ours {
					t.Fatalf("ours must be left untouched, got %s", got)
				}
			})
		}
	}
}

// The happy merge still unions both sides.
func TestMergePurgedUnionsBothSides(t *testing.T) {
	dir := t.TempDir()
	for n, body := range map[string]string{"base": "", "ours": `{"ids":["mem_a",7],"_v":1}`, "theirs": `{"ids":["mem_b"],"_v":1}`} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := MergeFiles(context.Background(), filepath.Join(dir, "base"), filepath.Join(dir, "ours"), filepath.Join(dir, "theirs"), ".lore/data/"+PurgedFileName); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "ours"))
	ids, err := PurgedIDs(b)
	if err != nil || strings.Join(ids, ",") != "mem_a,mem_b" {
		t.Fatalf("ids %v err %v", ids, err)
	}
}

// readPurged (import's view of main's purge list) reads a malformed list
// as an error, never as "nothing purged".
func TestReadPurgedRefusesAMalformedList(t *testing.T) {
	for name, bad := range badPurgedFiles {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, PurgedFileName), []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			if set, err := readPurged(dir); err == nil {
				t.Fatalf("want an error, got %v", set)
			}
		})
	}
	dir := t.TempDir()
	if set, err := readPurged(dir); err != nil || len(set) != 0 {
		t.Fatalf("absent file is an empty set: %v %v", set, err)
	}
	if err := os.WriteFile(filepath.Join(dir, PurgedFileName), []byte(`{"ids":["mem_a"],"_v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if set, err := readPurged(dir); err != nil || !set["mem_a"] || len(set) != 1 {
		t.Fatalf("valid list: %v %v", set, err)
	}
}

func TestPurgedIDsRefusesUndecodableBytes(t *testing.T) {
	for _, b := range []string{`{`, `{"ids":["a"]}`, `[]`} {
		if ids, err := PurgedIDs([]byte(b)); err == nil {
			t.Fatalf("%s: want an error, got %v", b, ids)
		}
	}
}
