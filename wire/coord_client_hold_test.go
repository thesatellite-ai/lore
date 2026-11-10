package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestClientRepoHoldsOnTheLinkedStore: in a client repo linked to an org
// store, `hold claim` writes <link folder>/resources/<name>.json on the
// store's signals ref (D8) and nothing in the client repo (no commit, no
// file, no lore ref on the client's remote); `hold release` removes it.
// A client repo with no store link (client by a repos: pattern only) has
// no store for the hold: E_UNSUPPORTED, nothing written anywhere.
func TestClientRepoHoldsOnTheLinkedStore(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	for _, linked := range []bool{true, false} {
		name := "unlinked"
		if linked {
			name = "linked"
		}
		t.Run(name, func(t *testing.T) {
			w := newOrgWorld(t)
			ctx := context.Background()
			mira := w.home(t, "mira")
			h := orgHostAt(t, w.repo, mira)
			in := opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
				Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}
			if !linked {
				in.Repos = []string{"github.com/acme/*"}
			}
			if _, err := h.Create(ctx, in, true); err != nil {
				t.Fatalf("create: %v", err)
			}
			if linked {
				if _, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true); err != nil {
					t.Fatalf("link: %v", err)
				}
			}
			app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = app.Close() }()
			withProject(t, app)
			store := filepath.Join(w.hosts, "agency/notes.git")
			client := filepath.Join(w.hosts, "acme/app.git")
			head := orgGit(t, w.repo, "rev-parse", "HEAD")
			dirty := orgGit(t, w.repo, "status", "--porcelain", "--untracked-files=all")
			noClientChange := func(step string) {
				t.Helper()
				if got := orgGit(t, w.repo, "rev-parse", "HEAD"); got != head {
					t.Fatalf("%s made a client repo commit", step)
				}
				if got := orgGit(t, w.repo, "status", "--porcelain", "--untracked-files=all"); got != dirty {
					t.Fatalf("%s changed client repo files:\n%s", step, got)
				}
				if refs := orgGit(t, client, "for-each-ref", "--format=%(refname)", "refs/lore"); refs != "" {
					t.Fatalf("%s pushed lore refs to the client remote: %s", step, refs)
				}
			}
			signalsTree := func() string {
				t.Helper()
				if orgGit(t, store, "for-each-ref", "--format=%(refname)", "refs/lore/signals/main") == "" {
					return ""
				}
				return orgGit(t, store, "ls-tree", "-r", "--name-only", "refs/lore/signals/main")
			}

			_, err = run(t, app, opscoord.OpHoldClaim, `{"name":"deploy-lock"}`, true)
			if !linked {
				if !errs.Has(err, errs.Unsupported) || !strings.Contains(err.Error(), "no store link") {
					t.Fatalf("unlinked client hold: %v, want E_UNSUPPORTED naming the missing link", err)
				}
				if tree := signalsTree(); tree != "" {
					t.Fatalf("a refused hold wrote the store signals ref: %s", tree)
				}
				noClientChange("refused hold")
				return
			}
			if err != nil {
				t.Fatalf("hold claim: %v", err)
			}
			if got := signalsTree(); got != "acme/resources/deploy-lock.json" {
				t.Fatalf("store signals ref holds %q, want acme/resources/deploy-lock.json", got)
			}
			noClientChange("hold claim")
			if _, err = run(t, app, opscoord.OpHoldRelease, `{"name":"deploy-lock"}`, true); err != nil {
				t.Fatalf("hold release: %v", err)
			}
			if got := signalsTree(); strings.Contains(got, "deploy-lock") {
				t.Fatalf("release left the hold on the store: %q", got)
			}
			noClientChange("hold release")
		})
	}
}
