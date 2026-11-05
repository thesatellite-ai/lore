package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
)

// conflictText is a field value holding git conflict markers.
const conflictText = "<<<<<<< ours\nweb hosts\n=======\nall hosts\n>>>>>>> theirs"

// markRow writes conflict markers into the text column col of the row
// file table/id, as an unresolved git merge would leave it.
func markRow(t *testing.T, h *opstest.H, table, id, col string) {
	t.Helper()
	p := filepath.Clean(filepath.Join(h.Repo, ".lore", "data", table, id+".json"))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc[col], err = json.Marshal(conflictText); err != nil {
		t.Fatal(err)
	}
	if b, err = json.MarshalIndent(doc, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// kindID returns the knd_ id of kind name from resource kind show.
func kindID(t *testing.T, h *opstest.H, name string) string {
	t.Helper()
	return h.ID(h.Must(settings.OpKindShow, `{"id":"`+name+`"}`))
}

// lastGoodHarness: a host kind with one field and one resource, every
// read op run once so the cache has seen the definition good.
func lastGoodHarness(t *testing.T) *opstest.H {
	t.Helper()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
		[2]string{settings.OpResAdd, `{"kind":"host","name":"h1","set":["os=linux"]}`},
	)
	h.Must(settings.OpResList, `{"kind":"host"}`)
	return h
}

// TestKindLastGoodFallback (RES 3.13, P2-RES-4): a kind row with conflict
// markers makes list, show, export, report and lint use the last good
// definition with a warning note, while add and edit of that kind are
// refused with E_KIND_CONFLICT naming the row.
func TestKindLastGoodFallback(t *testing.T) {
	h := lastGoodHarness(t)
	kid := kindID(t, h, "host")
	markRow(t, h, schema.TableKinds, kid, "description")
	h = h.Reopen()
	warn := "showing its last good definition (read-only)"
	for _, r := range []struct{ op, in string }{
		{settings.OpResList, `{"kind":"host"}`},
		{settings.OpResShow, `{"id":"h1"}`},
		{settings.OpResExport, `{"kind":"host","format":"json"}`},
		{settings.OpResReport, `{"kind":"host"}`},
		{settings.OpResLint, `{}`},
	} {
		res, err := h.Run(r.op, r.in)
		if err != nil {
			t.Errorf("%s %s: %v", r.op, r.in, err)
			continue
		}
		if all := h.JSON(res) + opstest.Text(res); !strings.Contains(all, warn) || !strings.Contains(all, kid) {
			t.Errorf("%s: no last-good warning naming %s: %s", r.op, kid, all)
		}
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); !strings.Contains(s, "os = linux") {
		t.Errorf("show with the last good definition %q", s)
	}
	for _, w := range []struct{ op, in string }{
		{settings.OpResAdd, `{"kind":"host","name":"h2","set":["os=bsd"]}`},
		{settings.OpResEdit, `{"id":"h1","set":["os=bsd"]}`},
	} {
		msg := h.Fails(w.op, w.in, errs.KindConflict)
		if !strings.Contains(msg, kid) {
			t.Errorf("%s refusal does not name %s: %s", w.op, kid, msg)
		}
	}
}

// TestKindLastGoodMarkedField: a marked field row of the kind falls back
// and refuses the same way.
func TestKindLastGoodMarkedField(t *testing.T) {
	h := lastGoodHarness(t)
	var fid string
	paths, err := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", schema.TableKindFields, "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("field rows %v %v", paths, err)
	}
	fid = strings.TrimSuffix(filepath.Base(paths[0]), ".json")
	markRow(t, h, schema.TableKindFields, fid, "description")
	h = h.Reopen()
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); !strings.Contains(s, "os = linux") {
		t.Errorf("show %q", s)
	}
	if msg := h.Fails(settings.OpResEdit, `{"id":"h1","set":["os=bsd"]}`, errs.KindConflict); !strings.Contains(msg, fid) {
		t.Errorf("edit refusal does not name %s: %s", fid, msg)
	}
}

// TestKindNeverSeenGood: with no last good definition (a cold cache, as a
// fresh clone of an already-conflicted store), reads refuse too: load
// nothing rather than guess (RES 3.13).
func TestKindNeverSeenGood(t *testing.T) {
	h := lastGoodHarness(t)
	markRow(t, h, schema.TableKinds, kindID(t, h, "host"), "description")
	h = h.ReopenCold()
	h.Fails(settings.OpResList, `{"kind":"host"}`, errs.KindConflict)
	h.Fails(settings.OpResShow, `{"id":"h1"}`, errs.KindConflict)
	h.Fails(settings.OpResAdd, `{"kind":"host","name":"h2"}`, errs.KindConflict)
}

// TestKindShowLastGood (RES 3.13, P3-RES-2): kind show and kinds of a kind
// with conflict markers show its last good definition with the warning
// naming the row; on a machine that never saw it good they show the rows
// as stored, with a warning saying so, so a person sees what to resolve.
func TestKindShowLastGood(t *testing.T) {
	h := lastGoodHarness(t)
	kid := kindID(t, h, "host")
	markRow(t, h, schema.TableKinds, kid, "description")
	warm := h.Reopen()
	for _, r := range []struct{ op, in string }{
		{settings.OpKindShow, `{"id":"host"}`},
		{settings.OpKinds, `{}`},
	} {
		s := opstest.Text(warm.Must(r.op, r.in))
		if !strings.Contains(s, "showing its last good definition (read-only)") || !strings.Contains(s, kid) {
			t.Errorf("%s: no last-good warning naming %s: %s", r.op, kid, s)
		}
		if strings.Contains(s, "<<<<<<<") {
			t.Errorf("%s shows the marked row, not the last good one: %s", r.op, s)
		}
	}
	if s := opstest.Text(warm.Must(settings.OpKindShow, `{"id":"host"}`)); !strings.Contains(s, "os  text") {
		t.Errorf("last good fields missing: %s", s)
	}
	cold := h.ReopenCold()
	s := opstest.Text(cold.Must(settings.OpKindShow, `{"id":"host"}`))
	if !strings.Contains(s, "no last good definition on this machine") || !strings.Contains(s, "<<<<<<<") {
		t.Errorf("never seen good: %s", s)
	}
}
