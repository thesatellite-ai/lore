package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTidyHome: what lore left behind goes by age (a day for tmp/, a week
// for damaged cache copies, an hour for half-written files), the empty
// tmp/ goes, and younger leftovers and real files stay.
func TestTidyHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	mk := func(rel string, age time.Duration) string {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldTmp := mk("tmp/lore-deleted-1", 48*time.Hour)
	oldCorrupt := mk("cache/stores/sto_x/main/root.db.corrupt-20261101T000000Z", 10*24*time.Hour)
	newCorrupt := mk("cache/stores/sto_x/main/root.db.corrupt-20261111T000000Z", 24*time.Hour)
	oldHalf := mk(".config.yaml.tmp-123", 2*time.Hour)
	cfg := mk("config.yaml", 100*24*time.Hour)
	tidyHome(home, now)
	for _, gone := range []string{oldTmp, filepath.Join(home, "tmp"), oldCorrupt, oldHalf} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v)", gone, err)
		}
	}
	for _, kept := range []string{newCorrupt, cfg} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	// a young tmp entry keeps the folder
	young := mk("tmp/probe-2", time.Minute)
	tidyHome(home, now)
	if _, err := os.Stat(young); err != nil {
		t.Errorf("a young probe folder was removed: %v", err)
	}
}

// TestDoctorHomeLeftovers: the folders of earlier builds are named with
// the command that removes them (old clones excepted: old_clones names
// those), and kept damaged cache copies are counted; a clean home says
// nothing.
func TestDoctorHomeLeftovers(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	a := &App{opts: Options{Home: home}}
	if got, err := a.DoctorHomeLeftovers(background()); err != nil || len(got) != 0 {
		t.Fatalf("clean home: %+v %v", got, err)
	}
	// clones/ and clones-v017/ are not named here: doctor's old_clones
	// check names each old clone once its content is safe in the new one.
	for _, d := range []string{"worktrees", "clones-v017", "clones"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	corrupt := filepath.Join(home, "cache", "x.db.corrupt-1")
	if err := os.MkdirAll(filepath.Dir(corrupt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corrupt, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := a.DoctorHomeLeftovers(background())
	if err != nil || len(got) != 2 {
		t.Fatalf("leftovers: %+v %v", got, err)
	}
	var lines []string
	for _, f := range got {
		lines = append(lines, f.Line())
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"rm -rf " + filepath.Join(home, "worktrees"), "1 damaged cache file(s)"} {
		if !strings.Contains(all, want) {
			t.Errorf("doctor does not say %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "clones") {
		t.Errorf("home_leftovers names the old clones (old_clones does, per clone):\n%s", all)
	}
}
