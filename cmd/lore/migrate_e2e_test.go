package main

// `lore migrate run` and `lore migrate rollback` on the release binary
// (FORMAT_EVOLUTION rule 4, CLI_REFERENCE 3.4), through main in a child process (cliworld_test.go):
// v0.1.17 row files (`_v: 1`) of several tables in a fresh checkout; a dry
// run lists them and changes no byte; the run rewrites each as `_v: 2`
// with an `_hlc`, applies the format steps (plans.status_str → status) and
// prints the one-line-per-item text; a second run has nothing to migrate;
// rollback puts every v1 file back byte for byte, and a second rollback
// finds nothing to undo.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v1 row files of the fixture: table → id → document (the shape a v0.1.17
// export wrote: `_v: 1`, `_table`, id, times, the table's columns).
var migrateV1Rows = []struct{ table, id, doc string }{
	{"memories", "mem_01a1af106a357424ba3b084bd5010e99",
		`{"_table":"memories","_v":1,"body":"the staging cluster sleeps","created_at":"2026-11-04T08:09:55.000000000Z","id":"mem_01a1af106a357424ba3b084bd5010e99","kind":"retrieved","source_kind":"manual","trust_score":0.5,"updated_at":"2026-11-04T08:09:55.000000000Z"}`},
	{"tasks", "tsk_01a1af106a357424ba3b084bd5010e98",
		`{"_table":"tasks","_v":1,"attempts":0,"claim_epoch":0,"commitment":"accepted","created_at":"2026-11-04T08:09:55.000000000Z","id":"tsk_01a1af106a357424ba3b084bd5010e98","priority":"medium","status":"todo","title":"fix the build","updated_at":"2026-11-04T08:09:55.000000000Z"}`},
	{"plans", "pln_01a1af106a357424ba3b084bd5010e97",
		`{"_table":"plans","_v":1,"body":"steps","created_at":"2026-11-04T08:09:55.000000000Z","id":"pln_01a1af106a357424ba3b084bd5010e97","status_str":"in progress","title":"rollout","updated_at":"2026-11-04T08:09:55.000000000Z"}`},
}

// rowPath is a row file of the repo store.
func rowPath(root, table, id string) string {
	return filepath.Join(root, ".lore", "data", table, id+".json")
}

// TestMigrateRunRollbackOnRelease: see the file comment.
func TestMigrateRunRollbackOnRelease(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	root := w.initRepo(t)
	for _, r := range migrateV1Rows {
		p := rowPath(root, r.table, r.id)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(r.doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unchanged := func(when string) {
		t.Helper()
		for _, r := range migrateV1Rows {
			b, err := os.ReadFile(filepath.Clean(rowPath(root, r.table, r.id)))
			if err != nil || string(b) != r.doc {
				t.Fatalf("%s: %s/%s = %q (%v), want the v1 bytes", when, r.table, r.id, b, err)
			}
		}
	}

	code, out, errOut := w.lore(t, root, "migrate", "run", "--dry-run")
	if code != 0 || !strings.HasPrefix(out, "Would upgrade 3 rows\n") || !strings.HasSuffix(out, "Nothing written (dry run).\n") || strings.Contains(out, "rows:") {
		t.Fatalf("dry run: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, r := range migrateV1Rows {
		if !strings.Contains(out, "\n  "+r.table+"/"+r.id+"\n") {
			t.Fatalf("dry run does not list %s/%s:\n%s", r.table, r.id, out)
		}
	}
	unchanged("after the dry run")

	code, out, errOut = w.lore(t, root, "migrate", "run")
	if code != 0 || !strings.HasPrefix(out, "Upgraded 3 rows\n") || !strings.Contains(out, "undo: lore migrate rollback") || strings.Contains(out, "rows:") {
		t.Fatalf("run: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, r := range migrateV1Rows {
		var doc map[string]json.RawMessage
		b, err := os.ReadFile(filepath.Clean(rowPath(root, r.table, r.id)))
		if err != nil || json.Unmarshal(b, &doc) != nil {
			t.Fatalf("%s/%s after run: %v %s", r.table, r.id, err, b)
		}
		if string(doc["_v"]) != "2" || len(doc["_hlc"]) == 0 {
			t.Fatalf("%s/%s is not _v 2 with _hlc: %s", r.table, r.id, b)
		}
		if r.table == "plans" {
			if _, kept := doc["status_str"]; kept || string(doc["status"]) == "" || string(doc["status"]) == `""` {
				t.Fatalf("plan step not applied (status_str kept or no status): %s", b)
			}
		}
	}

	code, out, errOut = w.lore(t, root, "migrate", "run")
	if code != 0 || !strings.HasPrefix(out, "Nothing to migrate") {
		t.Fatalf("second run: exit %d\n%s\n%s", code, out, errOut)
	}

	code, out, errOut = w.lore(t, root, "migrate", "rollback")
	if code != 0 {
		t.Fatalf("rollback: exit %d\n%s\n%s", code, out, errOut)
	}
	unchanged("after rollback")

	code, _, errOut = w.lore(t, root, "migrate", "rollback")
	if code != 27 || !strings.Contains(errOut, "E_NOT_FOUND") {
		t.Fatalf("second rollback: exit %d\n%s", code, errOut)
	}
}
