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

// fieldRowID returns the fld_ id of the kind_fields row named name, read
// from the row files (the field rows carry no name in their file name).
func fieldRowID(t *testing.T, h *opstest.H, name string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", schema.TableKindFields, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		b, rerr := os.ReadFile(filepath.Clean(p))
		if rerr != nil {
			t.Fatal(rerr)
		}
		var doc struct {
			Name string `json:"name"`
		}
		if jerr := json.Unmarshal(b, &doc); jerr != nil {
			t.Fatal(jerr)
		}
		if doc.Name == name {
			return strings.TrimSuffix(filepath.Base(p), ".json")
		}
	}
	t.Fatalf("no kind_fields row named %q in %v", name, paths)
	return ""
}

// TestKindMarkedFieldColdCache (RES 3.13, T-E2E1-1): only a FIELD row of
// the kind holds conflict markers and the cache is cold (a fresh clone of
// an already-conflicted store). The import refuses the marked field row,
// so the cache sees the kind with its other fields only and could record
// that as a "last good" definition. It is not one: the kind was never seen
// good on this machine, so reads refuse E_KIND_CONFLICT naming the marked
// row instead of showing the kind without that field, and writes refuse
// as always. The warm case (seen good before the conflict) falls back with
// the field intact (TestKindLastGoodMarkedField).
func TestKindMarkedFieldColdCache(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"arch","type":"text"}`},
		[2]string{settings.OpResAdd, `{"kind":"host","name":"h1","set":["os=linux","arch=arm64"]}`},
	)
	h.Must(settings.OpResList, `{"kind":"host"}`)
	fid := fieldRowID(t, h, "arch")
	markRow(t, h, schema.TableKindFields, fid)
	cold := h.ReopenCold()
	for _, r := range []struct{ op, in string }{
		{settings.OpResList, `{"kind":"host"}`},
		{settings.OpResShow, `{"id":"h1"}`},
		{settings.OpResExport, `{"kind":"host","format":"json"}`},
		{settings.OpResEdit, `{"id":"h1","set":["os=bsd"]}`},
	} {
		if msg := cold.Fails(r.op, r.in, errs.KindConflict); !strings.Contains(msg, fid) {
			t.Errorf("%s refusal does not name %s: %s", r.op, fid, msg)
		}
	}
	// kind show still answers, with the rows as stored, so a person sees
	// what to resolve (TestKindShowLastGood's never-seen-good branch).
	if s := opstest.Text(cold.Must(settings.OpKindShow, `{"id":"host"}`)); !strings.Contains(s, "no last good definition on this machine") || !strings.Contains(s, "arch") {
		t.Errorf("kind show, never seen good: %s", s)
	}
}
