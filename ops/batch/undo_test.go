package batch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/batch"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// memAdd is the memory add operation the tests seed rows with.
const memAdd = "memory.add"

// undoHint is the "(undo: lore batch undo bat_…)" line a bulk edit prints
// (METADATA 3.19); its group is the batch id.
var undoHint = regexp.MustCompile(`\(undo: lore batch undo (bat_[0-9a-f]{32})\)`)

// batchOf returns the batch id of the undo hint in out.
func batchOf(t *testing.T, out string) string {
	t.Helper()
	m := undoHint.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no undo hint in %q", out)
	}
	return m[1]
}

// metaOf is `lore meta get <id>` as text.
func metaOf(h *opstest.H, id string) string {
	return opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`))
}

// TestBatchUndoMetaSet pins PASS3-A-6 (R14): a bulk meta set prints the
// undo hint, undo puts every row back (a value that was there, and a key
// that was not), the undo is itself a batch that can be undone, and an
// undo over a row edited again after the batch is E_STALE with nothing
// written (P3-BATCH-1).
func TestBatchUndoMetaSet(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"pages","type":"list"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"owner","type":"text"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["pages=billing","owner=ops"]}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["pages=billing"]}`))
	c := h.ID(h.MustYes(memAdd, `{"body":"c","meta":["pages=home","owner=web"]}`))

	bat := batchOf(t, opstest.Text(h.MustYes(settings.OpMetaSet, `{"id":"owner=finance","where":["pages=billing"]}`)))
	for _, id := range []string{a, b} {
		if got := metaOf(h, id); !strings.Contains(got, "owner\tfinance") {
			t.Fatalf("%s after set %q", id, got)
		}
	}
	undo := opstest.Text(h.MustYes(batch.OpUndo, `{"id":"`+bat+`"}`))
	if !strings.Contains(undo, "restored 2 rows to their values before "+bat) {
		t.Fatalf("undo %q", undo)
	}
	if got := metaOf(h, a); !strings.Contains(got, "owner\tops") {
		t.Errorf("a after undo %q", got)
	}
	if got := metaOf(h, b); strings.Contains(got, "owner") {
		t.Errorf("b after undo still has owner: %q", got)
	}
	if got := metaOf(h, c); !strings.Contains(got, "owner\tweb") {
		t.Errorf("c (not in the batch) %q", got)
	}

	// the undo is a batch of its own: undoing it re-applies the set
	again := opstest.Text(h.MustYes(batch.OpUndo, `{"id":"`+batchOf(t, undo)+`"}`))
	if !strings.Contains(again, "restored 2 rows") {
		t.Fatalf("undo of the undo %q", again)
	}
	if got := metaOf(h, b); !strings.Contains(got, "owner\tfinance") {
		t.Errorf("b after undo of the undo %q", got)
	}

	// a row edited after the batch: refused, nothing written
	bat2 := batchOf(t, opstest.Text(h.MustYes(settings.OpMetaSet, `{"id":"owner=legal","where":["pages=billing"]}`)))
	h.MustYes(settings.OpMetaSet, `{"id":"`+a+`","pair":"owner=later"}`)
	msg := h.Fails(batch.OpUndo, `{"id":"`+bat2+`"}`, errs.Stale)
	if !strings.Contains(msg, a) || !strings.Contains(msg, "nothing written") {
		t.Errorf("stale refusal %q", msg)
	}
	if got := metaOf(h, b); !strings.Contains(got, "owner\tlegal") {
		t.Errorf("refused undo wrote b: %q", got)
	}
	if got := metaOf(h, a); !strings.Contains(got, "owner\tlater") {
		t.Errorf("refused undo wrote a: %q", got)
	}
}

// TestBatchUndoMetaUnset: a bulk unset is undone by putting the values back.
func TestBatchUndoMetaUnset(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"owner","type":"text"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["owner=ops"]}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["owner=web"]}`))
	bat := batchOf(t, opstest.Text(h.MustYes(settings.OpMetaUnset, `{"id":"owner","where":["owner?"]}`)))
	if got := metaOf(h, a); strings.Contains(got, "owner") {
		t.Fatalf("a after unset %q", got)
	}
	h.MustYes(batch.OpUndo, `{"id":"`+bat+`"}`)
	for id, want := range map[string]string{a: "owner\tops", b: "owner\tweb"} {
		if got := metaOf(h, id); !strings.Contains(got, want) {
			t.Errorf("%s after undo %q", id, got)
		}
	}
}

// TestBatchUndoResourceEditAndCheck: a resource bulk edit and a bulk check
// are undone (fields and last_checked back as before).
func TestBatchUndoResourceEditAndCheck(t *testing.T) {
	h := opstest.New(t)
	for _, in := range []string{`{"kind":"host"}`, `{"kind":"service"}`} {
		h.MustYes(settings.OpKindAdd, in)
	}
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1","set":["provider=hetzner"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h2","set":["provider=aws"]}`)
	for _, n := range []string{"a", "b"} {
		h.MustYes(settings.OpResAdd, `{"kind":"service","name":"`+n+`","set":["runs_on=h1"]}`)
	}
	show := func(n string) string { return opstest.Text(h.Must(settings.OpResShow, `{"id":"`+n+`"}`)) }

	bat := batchOf(t, opstest.Text(h.MustYes(settings.OpResEdit, `{"kind":"service","where":["runs_on=h1"],"set":["runs_on=h2"]}`)))
	h.MustYes(batch.OpUndo, `{"id":"`+bat+`"}`)
	for _, n := range []string{"a", "b"} {
		if s := show(n); !strings.Contains(s, "runs_on = h1") {
			t.Errorf("%s after undo %q", n, s)
		}
	}

	chk := batchOf(t, opstest.Text(h.MustYes(settings.OpResCheck, `{"kind":"host","where":["provider=hetzner"]}`)))
	if s := show("h1"); !strings.Contains(s, "(by manual)") {
		t.Fatalf("h1 not checked %q", s)
	}
	h.MustYes(batch.OpUndo, `{"id":"`+chk+`"}`)
	if s := show("h1"); strings.Contains(s, "checked") {
		t.Errorf("h1 still checked after undo %q", s)
	}
}

// TestBatchUndoMetaImport: a metadata import (one batch of edits) is undone.
func TestBatchUndoMetaImport(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"owner","type":"text"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["owner=ops"]}`))
	file := filepath.Join(t.TempDir(), "m.jsonl")
	if err := os.WriteFile(file, []byte(`{"id":"`+a+`","key":"owner","value":"sales"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bat := batchOf(t, opstest.Text(h.MustYes(settings.OpMetaImport, `{"file":`+opstest.Quote(filepath.ToSlash(file))+`}`)))
	if got := metaOf(h, a); !strings.Contains(got, "owner\tsales") {
		t.Fatalf("after import %q", got)
	}
	h.MustYes(batch.OpUndo, `{"id":"`+bat+`"}`)
	if got := metaOf(h, a); !strings.Contains(got, "owner\tops") {
		t.Errorf("after undo %q", got)
	}
}
