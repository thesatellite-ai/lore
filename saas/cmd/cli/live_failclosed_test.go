package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lsync"
)

// badPurgedLists are _live/purged.json bodies that cannot be read as a set
// of ids. Each must stop every action that would otherwise treat the set as
// empty (and bring purged rows back).
var badPurgedLists = map[string]string{
	"not json":           `{"ids":[`,
	"ids is a string":    `{"ids":"mem_1"}`,
	"ids is an object":   `{"ids":{"mem_1":true}}`,
	"ids holds a number": `{"ids":["mem_1",7]}`,
	"top level is list":  `["mem_1"]`,
}

// liveTestRow returns a live tree file for one memories row.
func liveTestRow(t *testing.T, id string) []byte {
	t.Helper()
	f := live.NewVersion(nil, map[string]any{"_v": "1", "_table": "memories", "id": id, "body": "x"}, time.Unix(1, 0).UTC(), "act_1")
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLiveApplyInputStopsOnAnUnreadablePurgeList(t *testing.T) {
	for name, purged := range badPurgedLists {
		t.Run(name, func(t *testing.T) {
			tree := live.Tree{
				live.PurgedPath: []byte(purged),
				live.RowPath("acme", "memories", "mem_1"): liveTestRow(t, "mem_1"),
			}
			in, err := liveApplyInput(tree, "acme")
			if err == nil {
				t.Fatalf("want an error, got %d rows to import", len(in.rows))
			}
			if len(in.rows) != 0 || in.purged != nil {
				t.Fatal("nothing may be handed to the apply when the purge list is unreadable")
			}
		})
	}
}

func TestLiveApplyInputKeepsUnreadableRowsOnLive(t *testing.T) {
	tree := live.Tree{
		live.PurgedPath: []byte(`{"ids":["mem_9"]}`),
		live.RowPath("acme", "memories", "mem_1"): liveTestRow(t, "mem_1"),
		live.RowPath("acme", "memories", "mem_2"): []byte(`{not a row`),
		live.RowPath("other", "memories", "m_3"):  []byte(`{not a row`),
	}
	in, err := liveApplyInput(tree, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := in.rows[lsync.LiveKey{Table: "memories", ID: "mem_1"}]; !ok || len(in.rows) != 1 {
		t.Fatalf("rows: %+v", in.rows)
	}
	if !in.purged["mem_9"] {
		t.Fatalf("purged: %v", in.purged)
	}
	bad := lsync.LiveKey{Table: "memories", ID: "mem_2"}
	if !in.unreadable[bad] || len(in.unreadable) != 1 {
		t.Fatalf("an undecodable row of this folder must be marked unreadable (not left out): %v", in.unreadable)
	}
	if len(in.warnings) != 1 || !strings.Contains(in.warnings[0], "mem_2") {
		t.Fatalf("warnings: %q", in.warnings)
	}
}

func TestRestorableTreeStopsOnAnUnreadablePurgeList(t *testing.T) {
	for name, purged := range badPurgedLists {
		t.Run(name, func(t *testing.T) {
			tree := live.Tree{
				live.PurgedPath: []byte(purged),
				live.RowPath("acme", "memories", "mem_1"): liveTestRow(t, "mem_1"),
			}
			// The session has no ref or repo: the purge list must be
			// checked before anything else is read.
			keep, dropped, err := (&liveSession{}).restorableTree(context.Background(), tree)
			if err == nil || keep != 0 || dropped != nil {
				t.Fatalf("want an error and nothing to restore, got keep=%d dropped=%v err=%v", keep, dropped, err)
			}
		})
	}
}

func TestWhyMissingOnLiveReportsAnUnreadablePurgeList(t *testing.T) {
	cases := map[string]struct {
		purged string
		want   string
	}{
		"purged":     {`{"ids":["mem_1"]}`, "deleted on purpose"},
		"not purged": {`{"ids":["mem_2"]}`, ""},
		"no list":    {"", ""},
		"unreadable": {`{"ids":"mem_1"}`, "could not be read"},
		"not json":   {`{`, "could not be read"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tree := live.Tree{}
			if c.purged != "" {
				tree[live.PurgedPath] = []byte(c.purged)
			}
			got := whyMissingOnLive(tree, "acme", "mem_1")
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// A refused live write must not drop rows from lore.db when the live
// bookkeeping cannot be read: "unknown" is not "never shared".
func TestNeverSharedRowsFailsWhenLiveStatesCannotBeRead(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close() // every query on a closed handle fails: the injected read failure
	diverted := []lsync.DivertedRow{{RowRef: lsync.RowRef{Table: "memories", ID: "mem_1"}, Doc: []byte(`{}`)}}
	never, err := neverSharedRows(context.Background(), db, diverted)
	if err == nil {
		t.Fatalf("want an error, got rows to drop: %v", never)
	}
	if len(never) != 0 {
		t.Fatalf("nothing may be dropped: %v", never)
	}
}

// Main's _purged.json decides what landing leaves out; when it cannot be
// read the landing plan must stop, never treat main as having purged
// nothing.
func TestMainPurgedRefusesAnUnreadableList(t *testing.T) {
	cases := map[string]struct {
		body    string // "" = file absent
		wantErr bool
		want    []string
	}{
		"absent":           {"", false, nil},
		"valid":            {`{"ids":["mem_1","mem_2"],"_v":1}`, false, []string{"mem_1", "mem_2"}},
		"non-string entry": {`{"ids":["mem_1",3],"_v":1}`, false, []string{"mem_1"}},
		"ids is a string":  {`{"ids":"mem_1","_v":1}`, true, nil},
		"ids is an object": {`{"ids":{"mem_1":true},"_v":1}`, true, nil},
		"not json":         {`{"ids":[`, true, nil},
		"no version":       {`{"ids":["mem_1"]}`, true, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx := context.Background()
			dir := t.TempDir()
			gitT(t, dir, "init", "-q", "-b", "main")
			p := mainDataPath(live.RootFolder, lsync.PurgedFileName)
			if c.body != "" {
				full := filepath.Join(dir, filepath.FromSlash(p))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(c.body), 0o600); err != nil {
					t.Fatal(err)
				}
				gitT(t, dir, "add", "-A")
			}
			gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "main")
			repo, err := gitref.Open(ctx, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			set, err := (&liveSession{repo: repo}).mainPurged(ctx, "HEAD", live.RootFolder)
			if c.body == "" {
				// A branch that does not exist yet holds no purge list either.
				if none, nerr := (&liveSession{repo: repo}).mainPurged(ctx, "refs/remotes/origin/main", live.RootFolder); nerr != nil || len(none) != 0 {
					t.Fatalf("missing ref: %v %v", none, nerr)
				}
			}
			if c.wantErr {
				if err == nil {
					t.Fatalf("want an error, got %v", set)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(set) != len(c.want) {
				t.Fatalf("got %v want %v", set, c.want)
			}
			for _, id := range c.want {
				if !set[id] {
					t.Fatalf("got %v want %v", set, c.want)
				}
			}
		})
	}
}
