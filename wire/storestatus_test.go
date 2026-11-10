package wire

// store list REACH/VISIBILITY over a real clone (later 518, OV57):
// StoreStatus reads only the local disk, so a git store reads
// no_local_copy until lore cloned it and local_copy after, a pinned branch
// has its own clone, and a file:// store reads missing until its folder
// holds the marker.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

func TestStoreStatusReachRealClone(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	bare := filepath.Join(hosts, "acme", "kb")
	if err := os.MkdirAll(bare, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, nil, "init", "-q", "--bare", "-b", "main")
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	ctx := background()
	kb := opsstores.Entry{Name: "kb", URL: "https://" + histHost + "/acme/kb"}
	folderDir := filepath.Join(t.TempDir(), "notes")
	notes := opsstores.Entry{Name: "notes", URL: "file://" + filepath.ToSlash(folderDir)}

	check := func(label string, e opsstores.Entry, reach opsstores.Reach, vis string) {
		t.Helper()
		if got := a.StoreStatus(ctx, e); got.Reach != reach || got.Visibility != vis {
			t.Errorf("%s: StoreStatus = %+v, want reach %s visibility %q", label, got, reach, vis)
		}
	}
	check("git, never cloned", kb, opsstores.ReachNoLocalCopy, "")
	check("file, no folder", notes, opsstores.ReachMissing, "")
	check("bad url", opsstores.Entry{Name: "x", URL: "ftp://nowhere/x"}, opsstores.ReachMissing, "")

	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"`+kb.URL+`"}`, true); err != nil {
		t.Fatal(err)
	}
	check("git, cloned by store init", kb, opsstores.ReachLocalCopy, string(rowfs.VisibilityInternal))
	check("git, same url with .git", opsstores.Entry{Name: "kb2", URL: kb.URL + ".git"}, opsstores.ReachLocalCopy, string(rowfs.VisibilityInternal))
	check("git, pinned branch never cloned", opsstores.Entry{Name: "kb", URL: kb.URL, Branch: "staging"}, opsstores.ReachNoLocalCopy, "")

	if err := os.MkdirAll(folderDir, 0o750); err != nil {
		t.Fatal(err)
	}
	check("file, folder without marker", notes, opsstores.ReachMissing, "")
	if _, err := run(t, a, opsstores.OpInit, `{"name":"notes","url":"`+notes.URL+`"}`, true); err != nil {
		t.Fatal(err)
	}
	check("file, marker", notes, opsstores.ReachOK, string(rowfs.VisibilityInternal))

	// the clone is gone (a person removed ~/.lore/clones): back to no_local_copy
	dir, isGit, err := a.localStoreDir(kb)
	if err != nil || !isGit {
		t.Fatalf("localStoreDir: %q %v %v", dir, isGit, err)
	}
	if err = os.RemoveAll(filepath.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	check("git, clone removed", kb, opsstores.ReachNoLocalCopy, "")
}
