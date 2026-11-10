package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// healthApp is an App on home with only what the health file needs.
func healthApp(home string, now func() time.Time) *App {
	return &App{opts: Options{Home: home}, doctorCache: opsdoctor.NewMemCache(0, now)}
}

// A doctor run in one process (the CLI) reaches setup.list in another
// (the open page), and still shows after the 60 s probe cache expired:
// Home's Health cell follows the last doctor run, not a stale fallback.
func TestSetupHealthAcrossProcesses(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	later := func() time.Time { return at.Add(time.Hour) } // past every TTL
	cli, page := healthApp(home, later), healthApp(home, later)
	if rs, ok := page.SetupHealth(context.Background(), "personal"); ok {
		t.Fatalf("health before any run: %+v", rs)
	}
	run := []onboard.CheckResult{{Check: onboard.CheckGitHubGHAccount, Status: onboard.StatusWarn, Level: onboard.LevelWarn, CheckedAt: at}}
	if err := cli.RecordHealth(context.Background(), "personal", run); err != nil {
		t.Fatal(err)
	}
	rs, ok := page.SetupHealth(context.Background(), "personal")
	if !ok || len(rs) != 1 || rs[0].Status != onboard.StatusWarn || !rs[0].CheckedAt.Equal(at) {
		t.Fatalf("page health = %+v %v", rs, ok)
	}
	// A newer run replaces it; other setups are kept.
	_ = cli.RecordHealth(context.Background(), "work", run)
	ok2 := []onboard.CheckResult{{Check: onboard.CheckGitHubGHAccount, Status: onboard.StatusOK, CheckedAt: at.Add(time.Minute)}}
	_ = page.RecordHealth(context.Background(), "personal", ok2)
	if rs, _ := cli.SetupHealth(context.Background(), "personal"); len(rs) != 1 || rs[0].Status != onboard.StatusOK {
		t.Fatalf("after re-run: %+v", rs)
	}
	if _, ok := cli.SetupHealth(context.Background(), "work"); !ok {
		t.Fatal("other setup lost")
	}
	// A broken file is replaced by the next run, never an error for Home.
	if err := os.WriteFile(page.healthPath(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := page.SetupHealth(context.Background(), "personal"); ok {
		t.Fatal("broken file read as health")
	}
	if err := page.RecordHealth(context.Background(), "personal", ok2); err != nil {
		t.Fatal(err)
	}
	if _, ok := page.SetupHealth(context.Background(), "personal"); !ok {
		t.Fatal("rewrite of a broken file not read")
	}
}

// Health belongs to the entry doctor checked (polish item 2, realmac #10):
// a setup saved since the run, or a file written before fingerprints, is
// "not checked yet"; a note or the disabled flag changes nothing a check
// reads, so they keep the health.
func TestSetupHealthFollowsTheSavedEntry(t *testing.T) {
	t.Parallel()
	const base = "setups:\n  - name: personal\n    kind: solo\n    folders: [\"~/code/**\"]\n"
	run := []onboard.CheckResult{{Check: onboard.CheckGitHubGHAccount, Status: onboard.StatusOK, CheckedAt: time.Now()}}
	for _, c := range []struct {
		name  string
		after string // config.yaml after the doctor run ("" = unchanged)
		old   bool   // the health file has no fingerprints (written before them)
		want  bool
	}{
		{"unchanged", "", false, true},
		{"note added", base + "    note: why\n", false, true},
		{"disabled", base + "    disabled: true\n    disabled_at: \"2026-11-07T00:00:00Z\"\n", false, true},
		{"folders changed", "setups:\n  - name: personal\n    kind: solo\n    folders: [\"~/work/**\"]\n", false, false},
		{"github added", base + "    github:\n      user: someone\n", false, false},
		{"default toggled", base + "    default: true\n", false, false},
		{"setup gone", "setups: []\n", false, false},
		{"file before fingerprints", "", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			cfg := filepath.Join(home, ConfigFile)
			if err := os.WriteFile(cfg, []byte(base), 0o600); err != nil {
				t.Fatal(err)
			}
			a := healthApp(home, time.Now)
			if _, ok := a.SetupHealth(context.Background(), "personal"); ok {
				t.Fatal("health before any doctor run")
			}
			if err := a.RecordHealth(context.Background(), "personal", run); err != nil {
				t.Fatal(err)
			}
			if c.old {
				if err := os.WriteFile(a.healthPath(), []byte(`{"setups":{"personal":[{"check":"github.gh_account","status":"ok"}]}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if c.after != "" {
				if err := os.WriteFile(cfg, []byte(c.after), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, ok := a.SetupHealth(context.Background(), "personal"); ok != c.want {
				t.Fatalf("health shown = %v, want %v", ok, c.want)
			}
		})
	}
}
