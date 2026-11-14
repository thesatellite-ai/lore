package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// TestOrgRepoVerbsOutsideARepoNeedARepo pins folders round 1d: org link,
// unlink, set and use act on the repo they run in; outside any git repo they
// said "no project id", "this repo has no remote" or E_NOT_PROJECT_ROOT. Each
// now refuses E_NOT_A_STORE naming what it does to a repo and that the
// folder is not inside one, and writes nothing there.
func TestOrgRepoVerbsOutsideARepoNeedARepo(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "plain")
	home := filepath.Join(root, "home")
	for _, d := range []string{dir, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	testenv.SetHome(t, home)
	h := orgHostAt(t, dir, filepath.Join(home, ".lore"))
	for verb, call := range map[string]func() error{
		"link":   func() error { _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "acme", Project: "web"}, true); return err },
		"unlink": func() error { _, err := h.Unlink(ctx, opsorgcmd.UnlinkIn{Org: "acme"}, true); return err },
		"set":    func() error { _, err := h.Set(ctx, opsorgcmd.SetIn{Org: "acme", Commit: true}); return err },
		"use":    func() error { _, err := h.Use(ctx, opsorgcmd.UseIn{Entry: "acme"}); return err },
	} {
		err := call()
		if !errs.Has(err, errs.NotAStore) || !strings.Contains(err.Error(), "org "+verb+": it ") || !strings.Contains(err.Error(), "is not inside a git repo") {
			t.Errorf("org %s outside a repo: %v; want E_NOT_A_STORE naming the repo it needs", verb, err)
		}
	}
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Errorf("the folder got files: %v %v", ents, err)
	}
}
