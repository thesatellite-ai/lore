package wire

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestMigrateTeamLockClientRepoOnStore (b0da3d98, D8): in a client repo
// linked to an org store, `lore migrate run`'s team lock is the hold
// <link folder>/resources/lore-migrate.json on the STORE's signals ref,
// and nothing reaches the client repo (no commit, no file, no lore ref on
// the client's remote). While mira holds it, bob in his own clone of the
// same client repo gets E_MIGRATE_LOCKED naming @mira, and his `unlock
// --team` cannot clear her live lock; once she releases it the hold is
// gone and bob takes it. A client repo with no store link (client by a
// repos: pattern only) has no store for the lock: E_UNSUPPORTED naming
// `lore org link`, nothing written anywhere.
func TestMigrateTeamLockClientRepoOnStore(t *testing.T) {
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
			// the checkout's project id and store list travel with the code,
			// so a teammate's clone resolves the same link
			orgGit(t, w.repo, "add", ".lore/data/_meta.json", ".lore/stores.yaml")
			orgGit(t, w.repo, "commit", "-q", "-m", "lore project")
			orgGit(t, w.repo, "push", "-q", "origin", "main")

			appM, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = appM.Close() }()
			store := filepath.Join(w.hosts, "agency/notes.git")
			client := filepath.Join(w.hosts, "acme/app.git")
			head := orgGit(t, w.repo, "rev-parse", "HEAD")
			// the local cache files lore init gitignores in a real checkout
			// (this world has no .gitignore) are not client repo content
			status := func() string {
				t.Helper()
				var keep []string
				for _, l := range strings.Split(orgGit(t, w.repo, "status", "--porcelain", "--untracked-files=all"), "\n") {
					if !strings.Contains(l, ".lore/lore-cache.db") {
						keep = append(keep, l)
					}
				}
				return strings.Join(keep, "\n")
			}
			dirty := status()
			noClientChange := func(step string) {
				t.Helper()
				if got := orgGit(t, w.repo, "rev-parse", "HEAD"); got != head {
					t.Fatalf("%s made a client repo commit", step)
				}
				if got := status(); got != dirty {
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

			release, line, err := appM.MigrateTeamLock().Acquire(ctx)
			if !linked {
				if !errs.Has(err, errs.Unsupported) || !strings.Contains(err.Error(), "no store link") {
					t.Fatalf("unlinked client team lock: %v, want E_UNSUPPORTED naming the missing link", err)
				}
				var ee *errs.Error
				if !errors.As(err, &ee) || !strings.Contains(ee.Hint, "lore org link agency --project "+filepath.Base(w.repo)) {
					t.Fatalf("unlinked client team lock: %v, want the link command naming the org", err)
				}
				// lore fix (owner item 25): no lock, no error, a line with
				// the exact link command; nothing written anywhere
				frel, fline, ferr := appM.fixTeamLock().Acquire(ctx)
				if ferr != nil || frel != nil || !strings.Contains(fline, "team lock: skipped") || !strings.Contains(fline, "lore org link agency --project "+filepath.Base(w.repo)) {
					t.Fatalf("lore fix's lock in an unlinked client repo: release %v line %q err %v", frel != nil, fline, ferr)
				}
				if tree := signalsTree(); tree != "" {
					t.Fatalf("a refused lock wrote the store signals ref: %s", tree)
				}
				noClientChange("refused lock")
				return
			}
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			if !strings.Contains(line, "refs/lore/signals/main of the linked store (folder acme)") {
				t.Fatalf("lock line %q does not name the store ref and folder", line)
			}
			if got := signalsTree(); got != "acme/resources/"+migrateLockName+".json" {
				t.Fatalf("store signals ref holds %q, want acme/resources/%s.json", got, migrateLockName)
			}
			noClientChange("acquire")

			// bob: another person, his own home and his own clone
			bob := w.home(t, "bob")
			hb := orgHostAt(t, w.root, bob)
			if _, err = hb.Import(ctx, opsorgcmd.ImportIn{URL: orgTestImport, Key: "human:bob@agency.example", Name: "Bob", Handle: "bob"}, true); err != nil {
				t.Fatalf("bob import: %v", err)
			}
			bobRepo := filepath.Join(w.root, "bob-app")
			orgGit(t, w.root, "clone", "-q", "https://github.com/acme/app", bobRepo)
			appB, err := New(StandaloneOptions(bobRepo, bob, bobRepo, "test"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = appB.Close() }()
			if _, _, berr := appB.MigrateTeamLock().Acquire(ctx); !errs.Has(berr, errs.MigrateLocked) || !strings.Contains(berr.Error(), "@mira is migrating") {
				t.Fatalf("bob while mira migrates: %v, want E_MIGRATE_LOCKED naming @mira", berr)
			}
			if _, uerr := appB.MigrateTeamLock().Unlock(ctx); !errs.Has(uerr, errs.MigrateLocked) {
				t.Fatalf("bob's unlock --team of mira's live lock: %v, want E_MIGRATE_LOCKED", uerr)
			}
			if got := signalsTree(); got != "acme/resources/"+migrateLockName+".json" {
				t.Fatalf("bob's refused calls changed the hold: %q", got)
			}

			if err = release(); err != nil {
				t.Fatalf("release: %v", err)
			}
			if got := signalsTree(); strings.Contains(got, migrateLockName) {
				t.Fatalf("release left the lock on the store: %q", got)
			}
			noClientChange("release")
			relB, _, err := appB.MigrateTeamLock().Acquire(ctx)
			if err != nil {
				t.Fatalf("bob after mira released: %v", err)
			}
			if err = relB(); err != nil {
				t.Fatal(err)
			}
			if refs := orgGit(t, client, "for-each-ref", "--format=%(refname)", "refs/lore"); refs != "" {
				t.Fatalf("bob's lock pushed lore refs to the client remote: %s", refs)
			}
		})
	}
}
