package renderhost

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestContextSessionStart (CONTEXT_BUDGET 6.9, OV50): the first session
// in a repo sends no "changed since" group (there is no last session) and
// stamps the start; the next session lists every knowledge row changed
// since that stamp, each as a line form in the "Changed since your last
// session" group; a session right after lists nothing changed again. A
// missing or unreadable stamp reads as "no last session".
func TestContextSessionStart(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	a, project := repoApp(t)
	clock := time.Now().UTC().Add(-time.Hour)
	h := New(Options{App: a, Now: func() time.Time { return clock }})

	changed := func(res contextResult) []string {
		var ids []string
		for _, p := range res.picks {
			if p.group == groupChanged {
				if p.full != p.line || p.line == "" {
					t.Errorf("changed pick %s is not a line form: full %q line %q", p.ID, p.full, p.line)
				}
				ids = append(ids, p.ID)
			}
		}
		return ids
	}
	newRule := func(body string) string {
		w, err := a.Create(ctx, store.SourceRepo, schema.TableRules, map[string]value.Value{
			"body": text(t, body), "severity": text(t, "must"), "activation": text(t, schema.RuleActivationAlways),
			"source_kind": text(t, "manual"), "project_id": text(t, project)})
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID.String()
	}

	old := newRule("Old rule from before any session.")
	first, err := h.contextSessionStart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(first); len(got) != 0 {
		t.Fatalf("first session listed changed rows %v (there is no last session)", got)
	}
	if first.title != "this session" || first.more == "" {
		t.Fatalf("result %+v", first)
	}

	fresh := newRule("New rule written after the first session started.") // real time > clock
	clock = time.Now().UTC().Add(time.Hour)
	second, err := h.contextSessionStart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := changed(second)
	hasFresh, hasOld := false, false
	for _, id := range got {
		hasFresh = hasFresh || id == fresh
		hasOld = hasOld || id == old
	}
	// the old rule was written before the first stamp only by the wall
	// clock; the stamp is the host clock, an hour behind, so both count
	if !hasFresh || !hasOld {
		t.Fatalf("second session changed %v, want %s and %s", got, fresh, old)
	}

	clock = clock.Add(time.Minute)
	third, err := h.contextSessionStart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(third); len(got) != 0 {
		t.Fatalf("a session right after listed %v as changed", got)
	}

	if !readSessionStart("").IsZero() || !readSessionStart(t.TempDir()+"/none").IsZero() {
		t.Fatal("a missing stamp must read as no last session")
	}
}
