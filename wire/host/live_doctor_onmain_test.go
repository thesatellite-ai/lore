package host

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/clock"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveDoctorRowOnMainIsCopied (devco N-14): an open row stays live after
// its max-age copy lands on the default branch (team:side-9). Once main
// holds that exact version, lore doctor must not call it "older than
// live.max_age not copied to main yet (run lore sync)" or count it as
// existing only in live: no lore sync can change that, so doctor stayed
// DEGRADED on every clone.
func TestLiveDoctorRowOnMainIsCopied(t *testing.T) {
	clk := clock.NewFake(ltBase)
	bare := filepath.Join(t.TempDir(), "origin.git")
	f := newLiveFixtureWith(t, "live:\n  max_age: 1h\n", clk, func(dir string) {
		gitT(t, dir, "init", "-q", "--bare", "-b", "main", bare)
		gitT(t, dir, "remote", "add", "origin", bare)
		gitT(t, dir, "push", "-q", "origin", "main")
	})
	open := ltRow(t, schema.TableTasks, ltTaskA, 1, "an open task", false)
	if err := f.b.Write(f.ctx, []store.Change{{Row: open}}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Hour)
	if out, err := f.s.landPass(f.ctx, live.RootFolder, nil, false); err != nil || out.Copied != 1 {
		t.Fatalf("the max-age copy did not land: %+v %v", out, err)
		t.Fatal(err)
	}
	clk.Advance(2 * time.Hour) // the copy is old again; main still holds this version
	base, err := liveDoctor{h: f.s.h}.Base(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(append(append([]string{}, base.Notes...), base.Warnings...), "\n")
	if strings.Contains(all, "older than live.max_age") {
		t.Fatalf("doctor says a row main already holds is not copied to main:\n%s", all)
	}
	if strings.Contains(all, "exist only in live") {
		t.Fatalf("doctor counts a row main already holds as only in live:\n%s", all)
	}
}
