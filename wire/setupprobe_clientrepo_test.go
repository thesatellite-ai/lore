package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestRepoInitProbeInClientRepoOffersOrgLink pins D-165: in a client repo
// of an org (a repo with no lore files whose remote the org claims) the
// repo.init check never fails with init's runnable fix; it is skipped
// with the org link step. A plain repo still gets the lore init fix, and
// the client answer only counts for the folder lore runs in.
func TestRepoInitProbeInClientRepoOffersOrgLink(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	other := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	client := func(context.Context) ClientRepo { return ClientRepo{Org: "acme"} }
	solo := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo}

	sp := setupProber{a: &App{opts: Options{RepoDir: repo, ClientRepo: client}}}
	r := sp.repoInit(ctx, opssetup.ProbeRequest{Setup: solo})
	if r.Status != onboard.StatusSkip {
		t.Fatalf("repo.init in a client repo = %+v, want skip", r)
	}
	if !strings.Contains(r.Detail, "lore org link --org acme") || strings.Contains(r.Detail, "lore init") {
		t.Fatalf("repo.init detail in a client repo = %q, want the org link and no lore init", r.Detail)
	}
	if _, err := os.Stat(filepath.Join(repo, ".lore")); err == nil {
		t.Fatal("probing a client repo wrote .lore/ in it")
	}

	// Another repo named by target is not the client repo lore runs in.
	if r := sp.repoInit(ctx, opssetup.ProbeRequest{Setup: solo, Target: other}); r.Status != onboard.StatusFail {
		t.Fatalf("repo.init in another repo = %+v, want fail (lore init)", r)
	}
	// Not a client repo: the plain lore init fix, as before.
	plain := setupProber{a: &App{opts: Options{RepoDir: repo}}}
	if r := plain.repoInit(ctx, opssetup.ProbeRequest{Setup: solo}); r.Status != onboard.StatusFail {
		t.Fatalf("repo.init in a plain repo = %+v, want fail (lore init)", r)
	}
}
