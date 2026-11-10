package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// openAt makes a real clone at <home>/clones/<norm> and closes it.
func openAt(t *testing.T, home, norm, remote string) ClonePaths {
	t.Helper()
	base := filepath.Join(home, ClonesDir, filepath.FromSlash(norm))
	b, err := OpenClone(context.Background(), remote, base, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	return PathsOf(base)
}

// scanStates scans home and returns each stem by its norm.
func scanStates(t *testing.T, home string) map[string]CloneFound {
	t.Helper()
	found, err := ScanClones(home)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]CloneFound{}
	for _, f := range found {
		if _, dup := out[f.Norm]; dup {
			t.Fatalf("stem %s listed twice", f.Norm)
		}
		out[f.Norm] = f
	}
	return out
}

// TestPathsOfIsOpenClonesLayout: the <repo>/ and <repo>.git/ folders a real
// open makes are exactly PathsOf's, so a whole clone is one stem (never two
// "duplicate" folders) and reads as CloneWhole.
func TestPathsOfIsOpenClonesLayout(t *testing.T) {
	home, remote := t.TempDir(), newRemote(t, true)
	p := openAt(t, home, "host.example/owner/notes", remote)
	for _, d := range []string{p.Base, p.Bare, p.Work} {
		if !isDir(d) {
			t.Fatalf("%s missing after an open", d)
		}
	}
	if !isFile(p.Lock) {
		t.Fatalf("lock %s missing", p.Lock)
	}
	if p.Bare != p.Base+".git" {
		t.Fatalf("bare %s is not beside %s", p.Bare, p.Base)
	}
	got := scanStates(t, home)
	if len(got) != 1 || got["host.example/owner/notes"].State != CloneWhole {
		t.Fatalf("scan of one whole clone: %+v", got)
	}
}

// TestScanClonesStates: every way a clone can lose a half, side by side
// with a whole clone and a pinned-branch clone, each read as its own state.
func TestScanClonesStates(t *testing.T) {
	home, remote := t.TempDir(), newRemote(t, true)
	whole := openAt(t, home, "host.example/owner/whole", remote)
	branch := openAt(t, home, "host.example/owner/whole@feature/x", remote)
	noBare := openAt(t, home, "host.example/owner/nobare", remote)
	noWork := openAt(t, home, "host.example/owner/nowork", remote)
	other := openAt(t, home, "host.example/owner/other", remote)
	mis := openAt(t, home, "host.example/owner/mislinked", remote)
	orphan := openAt(t, home, "host.example/owner/orphan", remote)

	if err := os.RemoveAll(noBare.Bare); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(noWork.Base); err != nil {
		t.Fatal(err)
	}
	// the worktree of "mislinked" now names the git data of "other"
	link, err := os.ReadFile(filepath.Join(other.Work, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(mis.Work, ".git"), link, 0o600); err != nil {
		t.Fatal(err)
	}

	// "orphan" got a fresh bare clone: its worktree's admin folder is gone
	if err = os.RemoveAll(filepath.Join(orphan.Bare, "worktrees")); err != nil {
		t.Fatal(err)
	}

	got := scanStates(t, home)
	want := map[string]CloneState{
		"host.example/owner/orphan":          CloneOrphanWork,
		"host.example/owner/whole":           CloneWhole,
		"host.example/owner/whole@feature/x": CloneWhole,
		"host.example/owner/nobare":          CloneNoBare,
		"host.example/owner/nowork":          CloneNoWork,
		"host.example/owner/other":           CloneWhole,
		"host.example/owner/mislinked":       CloneMislinked,
	}
	if len(got) != len(want) {
		t.Fatalf("scan found %d stems, want %d: %+v", len(got), len(want), got)
	}
	for norm, st := range want {
		if got[norm].State != st {
			t.Errorf("%s: state %q, want %q", norm, got[norm].State, st)
		}
	}
	if l := got["host.example/owner/mislinked"].LinkedTo; !within(l, other.Bare) {
		t.Errorf("mislinked names %q, want inside %s", l, other.Bare)
	}
	if got["host.example/owner/whole"].Paths != whole || got["host.example/owner/whole@feature/x"].Paths != branch {
		t.Errorf("paths differ from PathsOf: %+v", got)
	}
}

// TestScanClonesReadsOnly: no clones folder is no clones, and a scan
// leaves every folder it saw in place.
func TestScanClonesReadsOnly(t *testing.T) {
	home := t.TempDir()
	if got, err := ScanClones(home); err != nil || len(got) != 0 {
		t.Fatalf("empty home: %+v %v", got, err)
	}
	p := openAt(t, home, "host.example/owner/notes", newRemote(t, true))
	if err := os.RemoveAll(p.Bare); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanClones(home); err != nil {
		t.Fatal(err)
	}
	if !isDir(p.Work) {
		t.Fatal("a scan removed a clone half")
	}
}
