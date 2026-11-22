package renderhost

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestSessionStartPinnedSet pins devco D-494: the SessionStart hook serves
// a repo without LORE.md the private render's always-on rows, so the plan
// must carry them as the file prints them: must rules then critical/high
// hotfixes, each block byte for byte in the store file, nothing else (a
// should rule stays a bullet). A repo with a .lore folder (its LORE.md and
// private import carry them) gets no always-on group, so nothing doubles.
// The group is never cut: with its bytes on top of the start cap, a
// pinned set far over contextSessionStartCap still goes in full.
func TestSessionStartPinnedSet(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	a, repo := privApp(t)
	projects, err := a.List(ctx, "inner", schema.TableProjects)
	if err != nil || len(projects) == 0 {
		t.Fatalf("inner project: %v", err)
	}
	add := func(table row.Table, fields map[string]string) string {
		vals := map[string]value.Value{"source_kind": text(t, "manual"), "project_id": text(t, projects[0].ID.String())}
		for k, v := range fields {
			vals[k] = text(t, v)
		}
		w, err := a.Create(ctx, "inner", table, vals)
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID.String()
	}
	hfx := add(schema.TableHotfixes, map[string]string{"title": "worker does not hot-reload", "body": "Restart the worker.", "severity": "critical"})
	must := add(schema.TableRules, map[string]string{"body": "Run the migrations before deploy.", "severity": "must", "activation": schema.RuleActivationAlways})
	should := add(schema.TableRules, map[string]string{"body": "Prefer short subjects.", "severity": "should", "activation": schema.RuleActivationAlways})

	h := New(Options{App: a, Home: t.TempDir()})
	p, err := h.planPrivate(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	// D-909: rank order, a critical hotfix before an always-on must rule
	if len(p.pins) != 2 || p.pins[0].id != hfx || p.pins[0].kind != privatePinHotfix || p.pins[1].id != must || p.pins[1].kind != privatePinRule {
		t.Fatalf("pins %+v, want the hotfix %s then the must rule %s (not the should rule %s)", p.pins, hfx, must, should)
	}
	var body string
	for _, f := range p.files {
		if strings.Contains(f.body, must) {
			body = f.body
		}
	}
	for _, pin := range p.pins {
		if pin.shown != pin.text || !strings.Contains(body, pin.shown) {
			t.Fatalf("pin %s is not in full, byte for byte, in the private file:\n%s\n----\n%s", pin.id, pin.shown, body)
		}
	}

	res, err := h.contextSessionStart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, pk := range res.picks {
		if pk.group == groupPinned {
			t.Fatalf("a repo with a .lore folder got the always-on group (its LORE.md holds them): %+v", pk)
		}
	}

	big := strings.Repeat("x", contextSessionStartCap)
	r := contextResult{title: "this session", always: 2 * len(big), picks: []contextPick{
		{ID: "rul_a", full: big, line: big, group: groupPinned}, {ID: "rul_b", full: big, line: big, group: groupPinned}}}
	text, ids := r.fit(contextSessionStartCap + r.always)
	if len(ids) != 2 || strings.Count(text, big) != 2 {
		t.Fatalf("the always-on group was cut under the start cap: ids %v", ids)
	}
}
