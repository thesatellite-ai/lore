package whycontext

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// rec is a render record whose scope summary holds a cut of two entries.
func rec() Record {
	return Record{
		ID: "rh_1", CreatedAt: time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC), TargetPath: ".lore/LORE.md",
		TotalBytes: 1200, SHA256: "abc",
		ScopeSummary: `{"included":{},"cut_summary":{"file":".lore/LORE.md","profile":"codex","budget":16000,"before":20000,"after":15000,"budget_exceeded":false,"index_collapsed":true,"entries":[{"id":"rul_1","reason":"superseded"},{"id":"rul_2","reason":"budget"}]}}`,
		Text:         "# LORE\n",
	}
}

// TestShowSummaryAndRendered: the default summary prints v0.1.17's lines
// and JSON keys; the text only with --rendered.
func TestShowSummaryAndRendered(t *testing.T) {
	out, err := Show(rec(), ShowIn{})
	if err != nil {
		t.Fatal(err)
	}
	text := out.Text()
	for _, want := range []string{"render_id:    rh_1", "created_at:   2026-11-05 09:30:00Z", "target:       .lore/LORE.md", "total_bytes:  1200", "sha256:       abc", `scope:        {"included"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# LORE") {
		t.Fatal("the text without --rendered")
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema_version":1`, `"render_id":"rh_1"`, `"rendered_sha256":"abc"`, `"scope_summary":{"included"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("json misses %s: %s", key, b)
		}
	}
	full, _ := Show(rec(), ShowIn{Rendered: true})
	if !strings.Contains(full.Text(), "--- rendered text ---\n# LORE\n") {
		t.Fatalf("--rendered:\n%s", full.Text())
	}
}

// TestShowCut: --cut prints the moved-out list and {data: cut} as JSON; a
// record without a cut summary is E_NOT_FOUND naming `lore render`.
func TestShowCut(t *testing.T) {
	out, err := Show(rec(), ShowIn{Cut: true})
	if err != nil {
		t.Fatal(err)
	}
	text := out.Text()
	for _, want := range []string{"file:     .lore/LORE.md (profile codex)", "bytes:    15000 of 16000 budget (every pinned entry in full: 20000)", "index:    collapsed to one line", "moved out of the core (2):", "  rul_2  budget", "lore show <id>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cut misses %q:\n%s", want, text)
		}
	}
	b, _ := json.Marshal(out)
	if !strings.HasPrefix(string(b), `{"data":{"file":".lore/LORE.md"`) {
		t.Fatalf("cut json = %s", b)
	}
	old := rec()
	old.ScopeSummary = `{"included":{}}`
	if _, nerr := Show(old, ShowIn{Cut: true}); !errs.Has(nerr, errs.NotFound) || !strings.Contains(errs.EnvelopeOf(nerr).Error.Hint, "lore render") {
		t.Fatalf("no cut summary: %v", nerr)
	}
	empty := rec()
	empty.ScopeSummary = `{"cut_summary":{"file":"AGENTS.md","profile":"codex","budget":16000}}`
	e, err := Show(empty, ShowIn{Cut: true})
	if err != nil || !strings.Contains(e.Text(), "moved out of the core: nothing") || e.Data.Entries == nil || !e.cut {
		t.Fatalf("empty cut: %+v %v", e, err)
	}
}

// TestOpsReadsEnv: the operation is Local and passes the source's error
// through (no render yet).
func TestOpsReadsEnv(t *testing.T) {
	e := Ops(noRender{})[0]
	if s := e.Spec(); s.Name != OpShow || !s.Local {
		t.Fatalf("spec = %+v", s)
	}
	if _, err := (noRender{}).LastRender(context.Background()); !errs.Has(err, errs.NotFound) {
		t.Fatal(err)
	}
}

// noRender is a machine with no render record yet.
type noRender struct{}

func (noRender) LastRender(context.Context) (Record, error) {
	return Record{}, errs.New(errs.NotFound, "no render history yet")
}
