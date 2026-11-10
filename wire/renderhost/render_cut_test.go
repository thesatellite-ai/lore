package renderhost

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opspin "github.com/thesatellite-ai/lore/ops/pin"
	"github.com/thesatellite-ai/lore/ops/whycontext"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// repoApp is an App over a fresh git repo with one project row; it
// returns the App and the project id.
func repoApp(t *testing.T) (*wire.App, string) {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	a, err := wire.New(wire.Options{RepoDir: repo, Home: t.TempDir(), FlagActor: "human:alice@example.com",
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	p, err := a.Create(context.Background(), store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": text(t, "demo")})
	if err != nil {
		t.Fatal(err)
	}
	return a, p.Row.ID.String()
}

// TestCutSummaryToWhyContext (CONTEXT_BUDGET 6.11, fa-ctx later:46): the
// chain from the pinned-set loader through the budgeted render to `lore
// why-context --cut` names every reason a must rule is not in full:
// superseded and manual (left out by the loader), entry_max, a line pin,
// and budget. Rules in full are not listed.
func TestCutSummaryToWhyContext(t *testing.T) {
	ctx := context.Background()
	a, project := repoApp(t)
	lineV, err := value.TextOf(string(contextfit.FormLine))
	if err != nil {
		t.Fatal(err)
	}
	pinnedAt, err := value.DateTimeOf("2026-11-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(n, body string, extra map[string]value.Value) ids.AnyID {
		t.Helper()
		id := goldenID(t, ids.PrefixRule, n)
		f := map[string]value.Value{"body": text(t, body), "severity": text(t, "must"), "activation": text(t, schema.RuleActivationAlways),
			"source_kind": text(t, "manual"), "project_id": text(t, project)}
		for k, v := range extra {
			f[k] = v
		}
		if _, cerr := a.CreateWithID(ctx, store.SourceRepo, schema.TableRules, id, f); cerr != nil {
			t.Fatal(cerr)
		}
		return id
	}
	full := mk("01", "Keep handlers small. "+strings.Repeat("Split a handler when it grows past one screen. ", 4), nil)
	sup := mk("02", "Old wording.", map[string]value.Value{"superseded_by_id": text(t, full.String())})
	manual := mk("03", "Only on request.", map[string]value.Value{"activation": text(t, schema.RuleActivationManual)})
	long := mk("04", strings.Repeat("a long rule body that will not fit one entry. ", 40), nil)
	pinLine := mk("05", "Pinned to its line.", map[string]value.Value{opspin.FieldPin: lineV, opspin.FieldPinnedAt: pinnedAt})

	h := New(Options{App: a})
	var skips pinSkips
	rules, err := h.loadPinnedRules(ctx, project, &skips)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		budget int
		want   map[string]contextfit.Reason // id -> reason; ids not here must not be listed
	}{
		{name: "unlimited budget", budget: 0, want: map[string]contextfit.Reason{
			sup.String(): contextfit.ReasonSuperseded, manual.String(): contextfit.ReasonManual,
			long.String(): contextfit.ReasonEntryMax, pinLine.String(): contextfit.ReasonPinned}},
		{name: "budget below the fixed part", budget: 100, want: map[string]contextfit.Reason{
			sup.String(): contextfit.ReasonSuperseded, manual.String(): contextfit.ReasonManual,
			long.String(): contextfit.ReasonEntryMax, pinLine.String(): contextfit.ReasonPinned, full.String(): contextfit.ReasonBudget}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := renderInput{Settings: contextSettings{Budget: c.budget, Profile: "codex", EntryMax: 600, WarnPct: fallbackWarnPct}, Rules: rules, Skips: skips}
			_, rep := buildBudgetedFile("rnd_x", in)
			ss := renderScopeSummary{Cut: new(newCutSummary(rep, skips))}
			raw, jerr := json.Marshal(ss)
			if jerr != nil {
				t.Fatal(jerr)
			}
			out, serr := whycontext.Show(whycontext.Record{ID: "rnd_1", CreatedAt: time.Now(), ScopeSummary: string(raw)}, whycontext.ShowIn{Cut: true})
			if serr != nil {
				t.Fatal(serr)
			}
			txt := out.Text()
			got := map[string]contextfit.Reason{}
			for _, e := range out.Data.Entries {
				got[e.ID] = e.Reason
				if !strings.Contains(txt, "  "+e.ID+"  "+string(e.Reason)) {
					t.Fatalf("text misses %s %s:\n%s", e.ID, e.Reason, txt)
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("cut entries %v, want %v", got, c.want)
			}
			for id, r := range c.want {
				if got[id] != r {
					t.Fatalf("%s: reason %q, want %q (all %v)", id, got[id], r, got)
				}
			}
		})
	}
}
