package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/model/errs"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestExplain_CopiesAndWinner: `lore explain <id>` on cmd/lore (was
// E_UNSUPPORTED) names every copy of the id and the winning rule, for a
// live row, a live tombstone, a row only on this branch and an id found
// nowhere; every line names the real default branch (trunk), never main.
func TestExplain_CopiesAndWinner(t *testing.T) {
	const branch = "trunk"
	const branchMem = "mem_01a1a6841fc0784d92a62b0acf148b2a"
	const nowhere = "tsk_01a1a6841fc0784d92a62b0acf148b2b"
	f := newLiveFixtureWith(t, "default_branch: "+branch+"\n", clock.NewFake(ltBase))
	if err := f.b.Write(f.ctx, []store.Change{
		{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "open work", false)},
		{Row: ltRow(t, schema.TableTasks, ltTaskB, 2, "", true)},
	}); err != nil {
		t.Fatal(err)
	}
	// A row only on this branch (committed .lore/data file, never live).
	rel := mainDataPath(live.RootFolder, "memories/"+branchMem+".json")
	full := filepath.Join(f.s.top, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(`{"_v":2}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.s.top, "add", "-A")
	gitT(t, f.s.top, "commit", "-q", "-m", "branch row")

	cases := map[string]struct {
		id   string
		want []string
	}{
		"live row":       {ltTaskA, []string{ltTaskA + " in ", "live (local ", ": row, live since ", "this branch (HEAD): not present", "origin/" + branch + ": not present", "wins: the live copy (rule 4", "  land: "}},
		"live tombstone": {ltTaskB, []string{": tombstone, ", "wins: the live tombstone (rule 4)"}},
		"branch only":    {branchMem, []string{"live: not present", "this branch (HEAD): " + rel + " (", "wins: this branch's copy (not live)"}},
		"nowhere":        {nowhere, []string{"live: not present", "not found in live or on this branch", "lore sync peek --ref origin/" + branch}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := liveSync{h: f.s.h}.Explain(f.ctx, opssync.ExplainIn{ID: c.id})
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(out.Lines, "\n")
			for _, w := range c.want {
				if !strings.Contains(joined, w) {
					t.Errorf("missing %q in\n%s", w, joined)
				}
			}
			if strings.Contains(joined, "main") {
				t.Errorf("names main, but the default branch is %q:\n%s", branch, joined)
			}
			if len(out.Items) != 1 || out.Items[0] != c.id {
				t.Errorf("items %v, want [%s]", out.Items, c.id)
			}
		})
	}
	if l := explainLeaveLine(branch); !strings.Contains(l, branch) {
		t.Errorf("leave line %q does not name %q", l, branch)
	}
	if l := explainWaitsLine(branch); !strings.Contains(l, branch) {
		t.Errorf("waits line %q does not name %q", l, branch)
	}
}

// TestExplain_Refusals: no id is E_INVALID_INPUT; live rows switched off
// (LORE_LIVE=0) is E_UNSUPPORTED with the live reason, not "not built".
func TestExplain_Refusals(t *testing.T) {
	f := newLiveFixture(t)
	cases := map[string]struct {
		id   string
		off  bool
		code errs.Code
	}{
		"no id":    {"  ", false, errs.InvalidInput},
		"live off": {ltTaskA, true, errs.Unsupported},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.off {
				t.Setenv(EnvLive, envOff)
			}
			_, err := liveSync{h: f.s.h}.Explain(f.ctx, opssync.ExplainIn{ID: c.id})
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != c.code {
				t.Fatalf("got %v, want %s", err, c.code)
			}
			if strings.Contains(e.Message, "not available in this binary") {
				t.Fatalf("still the unported answer: %v", err)
			}
		})
	}
}

// TestSyncStatusMetaLines: `lore sync status` on cmd/lore prints the
// metadata lines (FA-META-11, v0.1.17 wording) from LiveConfig.MetaStatus:
// the quarantine count always, one line per quarantined row and per
// registry past its key cap; nil MetaStatus prints none of them.
func TestSyncStatusMetaLines(t *testing.T) {
	cases := map[string]struct {
		q, cap []string
		nilFn  bool
		want   []string
		absent []string
	}{
		"none":        {want: []string{"meta quarantined: 0"}, absent: []string{"meta registry:"}},
		"quarantined": {q: []string{"tsk_1 (bad meta)"}, want: []string{"meta quarantined: 1", "  tsk_1 (bad meta)"}},
		"over cap":    {cap: []string{"this repo: 3 key rows"}, want: []string{"meta registry: this repo: 3 key rows"}},
		"not wired":   {nilFn: true, absent: []string{"meta quarantined", "meta registry"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t)
			h := f.s.h
			h.cfg.MetaStatus = func(context.Context) ([]string, []string) { return c.q, c.cap }
			if c.nilFn {
				h.cfg.MetaStatus = nil
			}
			out, err := liveSync{h: h}.SyncStatus(f.ctx, opssync.StatusIn{})
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(out.Lines, "\n")
			for _, w := range c.want {
				if !strings.Contains(joined, w) {
					t.Errorf("missing %q in\n%s", w, joined)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(joined, a) {
					t.Errorf("unexpected %q in\n%s", a, joined)
				}
			}
			if len(out.Data.MetaQuarantined) != len(c.q) || len(out.Data.MetaRegistriesCap) != len(c.cap) {
				t.Errorf("data %v %v, want %v %v", out.Data.MetaQuarantined, out.Data.MetaRegistriesCap, c.q, c.cap)
			}
		})
	}
}
