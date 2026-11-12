package wire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// TestLayoutCommitLine: the layout commit's line says what the commit
// changed — rows, README, marker, in each combination — and never prints a
// zero count (a store whose commit only refreshed the README and the
// marker once printed "0 file(s)").
func TestLayoutCommitLine(t *testing.T) {
	const sha = "abc1234def5678"
	for _, c := range []struct {
		ch   rowfs.LayoutChange
		want string
	}{
		{rowfs.LayoutChange{Readme: rowfs.ReadmeRefreshed, Marker: 4}, "store lore-x: layout commit abc1234 — README refreshed, marker lore_store: 4"},
		{rowfs.LayoutChange{Marker: 4}, "store lore-x: layout commit abc1234 — marker lore_store: 4"},
		{rowfs.LayoutChange{Readme: rowfs.ReadmeAdded}, "store lore-x: layout commit abc1234 — README added"},
		{rowfs.LayoutChange{Moved: 12, Readme: rowfs.ReadmeRefreshed, Marker: 4}, "store lore-x: layout commit abc1234 — 12 rows moved to _store/, README refreshed, marker lore_store: 4"},
		{rowfs.LayoutChange{Moved: 12, Marker: 4}, "store lore-x: layout commit abc1234 — 12 rows moved to _store/, marker lore_store: 4"},
		{rowfs.LayoutChange{Moved: 2, FromOld: true, Folders: []string{"."}, Marker: 4}, "store lore-x: layout commit abc1234 — 2 rows moved from <folder>/.lore/data/ to <folder>/, the store's own rows to _store/ (folders: .), marker lore_store: 4"},
	} {
		got := LayoutCommitLine("lore-x", sha, c.ch)
		if got != c.want {
			t.Errorf("%+v:\n got %q\nwant %q", c.ch, got, c.want)
		}
		if strings.Contains(got, " 0 ") {
			t.Errorf("zero count in %q", got)
		}
	}
}

// TestMoveOldLayoutQuietOnANewMachine: a home that never had an earlier
// build's clones gets no line and no folder from the first-run step.
func TestMoveOldLayoutQuietOnANewMachine(t *testing.T) {
	home := t.TempDir()
	a := newTestAppHome(t, t.TempDir(), home)
	var out bytes.Buffer
	a.MoveOldLayout(context.Background(), &out)
	if out.Len() != 0 {
		t.Fatalf("the first-run step printed on a new machine:\n%s", out.String())
	}
	for _, p := range []string{git.StoresDir, "state"} {
		if _, err := os.Stat(filepath.Join(home, p)); err == nil {
			t.Errorf("the first-run step made %s", p)
		}
	}
}

// TestDoctorOldClones: doctor names an org link clone of an earlier build
// and half an old store clone, each with the exact command that removes it.
func TestDoctorOldClones(t *testing.T) {
	home := t.TempDir()
	v017 := filepath.Join(home, git.OldOrgClonesDir, "example.com", "acme", "notes.git")
	half := filepath.Join(home, git.OldClonesDir, "example.com", "acme", "hub")
	// half: the bare clone and the stem's lock, its worktree gone (a bare
	// clone alone is lore 0.1.17's whole clone, TestMoveOldCloneV017)
	for _, f := range []string{filepath.Join(v017, "HEAD"), filepath.Join(half+".git", "HEAD"), filepath.Join(half, "lock")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("ref: refs/heads/main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestAppHome(t, t.TempDir(), home)
	got, err := a.DoctorOldClones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range got {
		lines = append(lines, f.Line())
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"rm -rf " + v017, "rm -rf " + half + " " + half + ".git", "half of an old store clone", "no longer uses it"} {
		if !strings.Contains(text, want) {
			t.Errorf("doctor does not say %q:\n%s", want, text)
		}
	}
}

// TestOldLayoutSkips: the step never runs for help, version or the merge
// driver git calls.
func TestOldLayoutSkips(t *testing.T) {
	for _, c := range []string{"", "version", "help", "merge-driver", "__complete"} {
		if !OldLayoutSkips(c) {
			t.Errorf("%q runs the first-run step", c)
		}
	}
	for _, c := range []string{"doctor", "task", "sync", "store"} {
		if OldLayoutSkips(c) {
			t.Errorf("%q skips the first-run step", c)
		}
	}
}

// TestMovedAliasCloneFollowsSSHConfig: a clone moved from the old layout
// keeps its origin's alias spelling; lore opening the store by its
// canonical url finds that clone when the person's ~/.ssh/config renames
// the alias to the canonical host, even an alias with a dot (the no-dot
// guess alone would make a "~2" copy), and keeps a clone of an alias the
// config sends to another host apart. Not parallel: it sets HOME.
func TestMovedAliasCloneFollowsSSHConfig(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	if err := os.MkdirAll(filepath.Join(user, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	sshCfg := "Host gh.work.example\n  HostName github.com\n  User git\n\nHost gl-work\n  HostName gitlab.com\n"
	if err := os.WriteFile(filepath.Join(user, ".ssh", "config"), []byte(sshCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	clone := func(rel, origin string) string {
		dir := filepath.Join(home, git.StoresDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		cfg := "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = " + origin + "\n"
		if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	same := clone("acme/notes", "git@gh.work.example:acme/notes.git")
	got, _, err := git.CloneDirOf(home, "git@github.com:acme/notes.git", "")
	if err != nil || got != same {
		t.Fatalf("the canonical url opens %s (%v), want the moved alias clone %s", got, err, same)
	}
	other := clone("acme/plans", "git@gl-work:acme/plans.git")
	got, _, err = git.CloneDirOf(home, "git@github.com:acme/plans.git", "")
	if err != nil || got == other {
		t.Fatalf("the github store opens %s (%v): the clone of gl-work (gitlab.com) is another store's", got, err)
	}
}

// TestDoctorNamesAnOldBinaryWritingOldClones: an old-layout clone written
// after the move record (a v0.1.17 bare clone at
// clones/<host>/<owner>/<repo>.git) was written by another lore binary,
// since this one never writes there; doctor says so and names `which -a
// lore`. One older than the move gets no such warning.
func TestDoctorNamesAnOldBinaryWritingOldClones(t *testing.T) {
	home := t.TempDir()
	rec := git.MoveRecordPath(home)
	bare := filepath.Join(home, git.OldClonesDir, "github.com", "acme", "notes.git")
	for _, f := range []string{rec, filepath.Join(bare, "HEAD")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestAppHome(t, t.TempDir(), home)
	text := func() string {
		got, err := a.DoctorOldClones(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, f := range got {
			lines = append(lines, f.Line())
		}
		return strings.Join(lines, "\n")
	}
	old := time.Now().Add(-time.Hour)
	for _, p := range []string{rec, filepath.Join(bare, "HEAD"), bare, filepath.Dir(bare)} {
		_ = os.Chtimes(p, old, old)
	}
	_ = os.Chtimes(rec, time.Now(), time.Now()) // the move came after the clone
	if s := text(); strings.Contains(s, "which -a lore") {
		t.Fatalf("doctor blames another binary for a clone older than the move:\n%s", s)
	}
	_ = os.Chtimes(rec, old.Add(-time.Hour), old.Add(-time.Hour)) // the clone came after the move
	s := text()
	for _, want := range []string{bare + " was written after the move", "an older lore binary on this machine", "still writes here", "`which -a lore`"} {
		if !strings.Contains(s, want) {
			t.Errorf("doctor does not say %q:\n%s", want, s)
		}
	}
}
