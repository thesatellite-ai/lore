package lsync

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// OV31 (P2-OLDSYNC-1): DropPurgedCopies drops only this machine's safety
// copies whose row id is listed in the repo's own _purged.json; a dry run
// lists them and drops nothing; a _purged.json that cannot be read is an
// error, never "nothing purged" (fail closed on a destructive path).
func TestDropPurgedCopies(t *testing.T) {
	ctx := context.Background()
	trashed := []string{"mem_purged1", "mem_kept", "rul_purged2", "mem_purged1"} // two copies of one id
	seed := func(t *testing.T) *env {
		t.Helper()
		e := newEnv(t)
		e.reconcile() // creates the sync tables
		for _, id := range trashed {
			if _, err := e.db.ExecContext(ctx, `INSERT INTO `+tblTrash+`(table_name, row_id, doc, reason, at) VALUES ('memories', ?, '{}', 'test', '2026-11-05T00:00:00Z')`, id); err != nil {
				t.Fatal(err)
			}
		}
		return e
	}
	rowIDs := func(rs []TrashRecord) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.RowID)
		}
		slices.Sort(out)
		return out
	}
	remaining := func(t *testing.T, e *env) []string {
		t.Helper()
		all, err := ListTrash(ctx, e.db)
		if err != nil {
			t.Fatal(err)
		}
		return rowIDs(all)
	}

	cases := []struct {
		name     string
		purged   []string // nil = no _purged.json
		raw      string   // written as-is instead of purged when set
		dryRun   bool
		wantGone []string
		wantLeft []string
		wantErr  bool
	}{
		{name: "no _purged.json drops nothing", wantLeft: []string{"mem_kept", "mem_purged1", "mem_purged1", "rul_purged2"}},
		{name: "empty list drops nothing", purged: []string{}, wantLeft: []string{"mem_kept", "mem_purged1", "mem_purged1", "rul_purged2"}},
		{name: "drops every copy of listed ids only", purged: []string{"mem_purged1", "rul_purged2", "mem_never_trashed"}, wantGone: []string{"mem_purged1", "mem_purged1", "rul_purged2"}, wantLeft: []string{"mem_kept"}},
		{name: "dry run lists and keeps", purged: []string{"mem_purged1"}, dryRun: true, wantGone: []string{"mem_purged1", "mem_purged1"}, wantLeft: []string{"mem_kept", "mem_purged1", "mem_purged1", "rul_purged2"}},
		{name: "unreadable _purged.json is an error", raw: "{not json", wantErr: true, wantLeft: []string{"mem_kept", "mem_purged1", "mem_purged1", "rul_purged2"}},
		{name: "ids not a list is an error", raw: `{"_v": 1, "ids": "mem_purged1"}`, wantErr: true, wantLeft: []string{"mem_kept", "mem_purged1", "mem_purged1", "rul_purged2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := seed(t)
			if err := os.MkdirAll(e.data, 0o750); err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.raw != "":
				if err := os.WriteFile(filepath.Join(e.data, PurgedFileName), []byte(tc.raw), 0o600); err != nil {
					t.Fatal(err)
				}
			case tc.purged != nil:
				set := map[string]bool{}
				for _, id := range tc.purged {
					set[id] = true
				}
				if err := writePurged(e.data, set); err != nil {
					t.Fatal(err)
				}
			}
			got, err := DropPurgedCopies(ctx, e.db, e.data, tc.dryRun)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err == nil && !slices.Equal(rowIDs(got), tc.wantGone) {
				t.Errorf("listed %v, want %v", rowIDs(got), tc.wantGone)
			}
			if left := remaining(t, e); !slices.Equal(left, tc.wantLeft) {
				t.Errorf("left in trash %v, want %v", left, tc.wantLeft)
			}
		})
	}
	// A missing data dir means no _purged.json: nothing purged, no error.
	e := seed(t)
	if got, err := DropPurgedCopies(ctx, e.db, filepath.Join(e.dir, "nope"), false); err != nil || len(got) != 0 {
		t.Fatalf("missing data dir: %v %v", got, err)
	}
}
