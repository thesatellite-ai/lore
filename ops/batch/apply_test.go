package batch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/batch"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// Operations the apply tests read rows back with.
const (
	memShow = "memory.show"
	memList = "memory.list"
)

// applyFile writes lines as a JSON-lines file and returns the apply input.
func applyFile(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ops.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestBatchApplyMixedFileIsOneStoreWrite pins devco:D-925 (owner's
// 440-action import): adds, edits, archives and metadata sets of one file
// land as ONE store write under one bat_ id (one commit on a store
// clone), each line checked by its own op; # and blank lines are skipped;
// `lore batch undo` puts the edited rows back and trashes the added ones.
func TestBatchApplyMixedFileIsOneStoreWrite(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"team","type":"text"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"alpha before"}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"bravo stays"}`))
	c := h.ID(h.MustYes(memAdd, `{"body":"charlie tagged"}`))
	file := applyFile(t,
		"# the import of the week",
		`{"op": "memory add", "input": {"body": "delta new"}}`,
		"",
		`{"op": "lore memory add", "input": {"body": "echo new"}}`,
		`{"op": "memory.edit", "input": {"id": "`+a+`", "body": "alpha after"}}`,
		`{"op": "memory archive", "input": {"id": "`+b+`"}}`,
		`{"op": "meta set", "input": {"id": "`+c+`", "pair": "team=core"}}`,
	)
	out := opstest.Text(h.MustYes(batch.OpApply, `{"file":"`+file+`"}`))
	if !strings.Contains(out, "applied 5 line(s) in 1 store write(s) (stores: repo)") {
		t.Fatalf("apply: %q, want 5 lines in 1 store write", out)
	}
	if got := opstest.Text(h.Must(memShow, `{"id":"`+a+`"}`)); !strings.Contains(got, "alpha after") {
		t.Errorf("the edit line did not land: %q", got)
	}
	if got := metaOf(h, c); !strings.Contains(got, "team\tcore") {
		t.Errorf("the meta set line did not land: %q", got)
	}
	list := opstest.Text(h.Must(memList, `{}`))
	if !strings.Contains(list, "delta new") || !strings.Contains(list, "echo new") {
		t.Errorf("the add lines did not land: %q", list)
	}
	undo := opstest.Text(h.MustYes(batch.OpUndo, `{"id":"`+batchOf(t, out)+`"}`))
	if !strings.Contains(undo, "moved 2 rows") || !strings.Contains(undo, "restored 3 rows") {
		t.Fatalf("undo of the apply: %q, want 2 added rows trashed and 3 edited rows restored", undo)
	}
	if got := opstest.Text(h.Must(memShow, `{"id":"`+a+`"}`)); !strings.Contains(got, "alpha before") {
		t.Errorf("undo did not put the edit back: %q", got)
	}
}

// TestBatchApplyRefusesBeforeWriting: every bad line is listed at once
// (bad JSON, unknown op, a read, a delete, a setup op, a meta key
// registration, a stray flag, a line's own dry_run) and nothing is
// written; a line a write refuses (a secret) stops the file with nothing
// written either; --dry-run runs every line and writes nothing.
func TestBatchApplyRefusesBeforeWriting(t *testing.T) {
	h := opstest.New(t)
	a := h.ID(h.MustYes(memAdd, `{"body":"alpha"}`))
	good := `{"op": "memory add", "input": {"body": "should never land"}}`
	msg := h.Fails(batch.OpApply, `{"file":"`+applyFile(t,
		good,
		`not json`,
		`{"op": "nope add", "input": {}}`,
		`{"op": "memory list", "input": {}}`,
		`{"op": "memory delete", "input": {"id": "`+a+`"}}`,
		`{"op": "store add", "input": {"name": "x"}}`,
		`{"op": "meta add", "input": {"name": "k", "type": "text"}}`,
		`{"op": "memory edit", "input": {"id": "`+a+`", "nope": "x"}}`,
		`{"op": "memory add", "input": {"body": "x", "dry-run": true}}`,
	)+`"}`, errs.InvalidInput)
	for _, want := range []string{"8 line(s) cannot run; nothing written", "line 2: not a", "line 3: unknown op", "line 4: \"memory list\" cannot run",
		"line 5: \"memory delete\" cannot run", "line 6: \"store add\" cannot run", "line 7: \"meta add\" cannot run", "line 8: ", "line 9: \"dry_run\" in a line"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal lacks %q:\n%s", want, msg)
		}
	}
	secret := `{"op": "memory add", "input": {"body": "key AKIA` + "IOSFODNN7EXAMPLQ" + `"}}`
	msg = h.Fails(batch.OpApply, `{"file":"`+applyFile(t, good, secret)+`"}`, errs.NeedsConfirm)
	if !strings.Contains(msg, "line 2 (memory add); nothing written") {
		t.Errorf("a refused write line: %q", msg)
	}
	out := opstest.Text(h.MustYes(batch.OpApply, `{"file":"`+applyFile(t, good)+`","dry_run":true}`))
	if !strings.Contains(out, "checked 1 line(s)") || !strings.Contains(out, "nothing written (--dry-run)") {
		t.Errorf("dry run: %q", out)
	}
	if list := opstest.Text(h.Must(memList, `{}`)); strings.Contains(list, "should never land") {
		t.Fatalf("a refused or dry-run apply wrote a row:\n%s", list)
	}
}

// TestBatchApplyTwoWritesOfOneRow: a line writing a row an earlier line
// of the file already wrote lands what is staged first and goes on, so
// the file ends as its lines say (one more store write).
func TestBatchApplyTwoWritesOfOneRow(t *testing.T) {
	h := opstest.New(t)
	a := h.ID(h.MustYes(memAdd, `{"body":"alpha"}`))
	out := opstest.Text(h.MustYes(batch.OpApply, `{"file":"`+applyFile(t,
		`{"op": "memory edit", "input": {"id": "`+a+`", "body": "first edit"}}`,
		`{"op": "memory edit", "input": {"id": "`+a+`", "body": "second edit"}}`,
	)+`"}`))
	if !strings.Contains(out, "applied 2 line(s) in 2 store write(s)") {
		t.Fatalf("apply: %q", out)
	}
	if got := opstest.Text(h.Must(memShow, `{"id":"`+a+`"}`)); !strings.Contains(got, "second edit") {
		t.Errorf("the last edit did not win: %q", got)
	}
}
