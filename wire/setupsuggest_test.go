package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// Issue 6 end to end over the real config writers: a setup that works as
// octo-alice is live, then trashed, then purged (the tombstone carries
// github_sha); AccountCover follows it, "Suggest again" is kept per purge
// in ~/.lore/state, and a newer purge for the same account hides it again.
func TestAccountCoverFollowsTheSetup(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	a := &App{opts: Options{Home: home}}
	ctx := context.Background()
	cfg := filepath.Join(home, "config.yaml")
	setup := func(name string) string {
		return "setups:\n  - name: " + name + "\n    kind: solo\n    github:\n      user: octo-alice\n"
	}
	if err := os.WriteFile(cfg, []byte(setup("personal")), 0o600); err != nil {
		t.Fatal(err)
	}
	state := func() opssetup.AccountCoverFact {
		f, ok := a.AccountCover(ctx, "octo-alice", "personal")
		if !ok {
			return opssetup.AccountCoverFact{}
		}
		return f
	}
	if f := state(); f.State != "live" || f.Setup != "personal" {
		t.Fatalf("live: %+v", f)
	}
	if found, err := TrashSetup(home, "personal", time.Date(2026, 11, 8, 1, 0, 0, 0, time.UTC)); err != nil || !found {
		t.Fatalf("trash: %v %v", found, err)
	}
	if f := state(); f.State != "trashed" {
		t.Fatalf("trashed: %+v", f)
	}
	first := time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC)
	if _, err := PurgeSetup(home, "personal", PurgeOptions{At: first}); err != nil {
		t.Fatal(err)
	}
	f := state()
	if f.State != opssetup.CoverPurged || f.Setup != "personal" || f.PurgedAt != "2026-11-08T02:00:00Z" || f.Again ||
		!strings.HasPrefix(f.Line, "personal (deleted for good) used GitHub account octo-alice;") {
		t.Fatalf("purged: %+v", f)
	}
	for range 2 {
		if err := a.RecordSuggestAgain(ctx, "Octo-Alice", f.PurgedAt); err != nil {
			t.Fatal(err)
		}
	}
	if f := state(); !f.Again {
		t.Fatalf("suggest again not remembered: %+v", f)
	}
	if n := len(a.readSuggestAgain().Keys); n != 1 {
		t.Fatalf("recording twice stored %d keys", n)
	}
	if st, err := os.Stat(a.suggestAgainPath()); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", st, err)
	}
	// Made again under another name and purged again: hidden again.
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(setup("personal-2")+strings.TrimPrefix(string(raw), "setups: []\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := TrashSetup(home, "personal-2", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := PurgeSetup(home, "personal-2", PurgeOptions{At: first.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if f := state(); f.State != opssetup.CoverPurged || f.Setup != "personal-2" || f.Again {
		t.Fatalf("newer purge: %+v", f)
	}
}
