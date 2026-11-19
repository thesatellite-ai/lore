package wire

import (
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestDoctorFixBaseChecksAreReads (devco D-474): under `lore doctor --fix`
// the base checks (stale commit links asks for this folder's project) run
// as reads, as they do in a plain doctor (D-441). They ran with the fix's
// write access, so in a store folder or a repo linked to one with no
// project row yet, the check committed a nameless project row: a store
// commit no repair asked for (doctor --fix made 3 commits for 2 repairs).
// Only the repairs write.
func TestDoctorFixBaseChecksAreReads(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	repo := openWith(t, gitRepo(t), Options{Home: home})
	if _, err := run(t, repo, opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	clone := filepath.Join(home, gitbackend.StoresDir, "acme", "kb")
	commits := func() string { return strings.TrimSpace(gitIn(t, clone, nil, "rev-list", "--count", "HEAD")) }
	before, tree := commits(), strings.Join(treeOf(t, clone), "\n")

	top := openWith(t, clone, Options{Home: home})
	if _, err := top.CoreDoctorBase(background(), true); err != nil {
		t.Fatalf("doctor base with fix: %v", err)
	}
	if after := commits(); after != before {
		t.Fatalf("doctor --fix's checks committed in the store (%s -> %s commits): %s", before, after,
			gitIn(t, clone, nil, "log", "-1", "--format=%s", "--name-only"))
	}
	if after := strings.Join(treeOf(t, clone), "\n"); after != tree {
		t.Fatalf("doctor --fix's checks changed the store's files:\nbefore:\n%s\nafter:\n%s", tree, after)
	}
}
