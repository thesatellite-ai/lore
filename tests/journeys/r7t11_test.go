//go:build journeys && !windows

package journeys

// r7t11_test.go — devco:T-11: a read writes nothing anywhere. Lee keeps a
// personal store in git (lore-me); in a clone of a repo with no lore data
// that the org owns (own:, not linked, not inited) `lore search` answers "no matches" and the
// personal store's clone is identical before and after: no commit, no new
// file (the search used to commit a nameless project row there, which the
// next `lore sync` pushed).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	r7t11MeSlug    = "github.com/lee-gh/lore-me"
	r7t11PlainSlug = "github.com/acme-co/appdir"
)

func TestJourneyR7T11ReadWritesNothing(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	app := tt.repo[tlLee.Email]
	me := w.Remote(r7t11MeSlug)
	parent := filepath.Join(lee.Home, "code")

	w.Step("day1/setup-with-a-git-personal-store", func() {
		tlApply(t, lee, app.Dir, tlLeadSetup, "kind", map[string]any{"kind": "admin", "name": tlLeadSetup})
		tlApply(t, lee, app.Dir, tlLeadSetup, "identity", map[string]any{"name": tlLee.Name, "email": tlLee.Email})
		tlApply(t, lee, app.Dir, tlLeadSetup, "github", map[string]any{"github_account": tlLee.Login, "host_alias": "github.com"})
		tlApply(t, lee, app.Dir, tlLeadSetup, "personal_store", map[string]any{"personal_store": "create", "store_url": me.SSHURL()})
		tlApply(t, lee, app.Dir, tlLeadSetup, "team", map[string]any{"org": tlOrg, "store_url": tt.store.SSHURL()})
		tlLastSteps(t, lee, app.Dir, tlLeadSetup, []string{tlFolders})
		lee.Lore(app.Dir, "sync").OK(t) // the new team store reaches the remote
		// the org owns every acme-co repo (own:), so appdir is an own repo
		// of the org with no lore data, as in the finding
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\nrepos:\n    - github.com/client-co/*\n")
		s.CommitAll("the org's own and client repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(app.Dir, "sync").OK(t)
		lee.Lore(app.Dir, "memory", "add", "--to", "me", "--body", "a personal note so the store has a commit").OK(t)
		lee.Lore(app.Dir, "sync").OK(t)
	})

	w.Step("day2/search-in-a-repo-with-no-lore-data", func() {
		clone := r7t11PersonalClone(t, lee.LorePath("stores"), me.SSHURL())
		snap := func() string {
			return w.TryGit(clone, "rev-parse", "HEAD").Stdout + w.TryGit(clone, "status", "--porcelain", "--ignored", "--untracked-files=all").Stdout +
				w.TryGit(clone, "ls-files").Stdout
		}
		remoteBefore := me.Rev("refs/heads/main")
		before := snap()
		appdir := jPlainRepo(t, lee, w.Remote(r7t11PlainSlug), filepath.Join(parent, "appdir"))
		lee.LoreIn(appdir, "search", "billing").OK(t).Says(t, "no matches").Never(t, "committed")
		lee.LoreIn(appdir, "search", "billing", "webhook").OK(t).Never(t, "committed")
		if after := snap(); after != before {
			t.Fatalf("a search changed the personal store's clone:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if st := appdir.Git("status", "--porcelain", "--ignored"); st != "" {
			t.Fatalf("a search left files in a repo with no lore data:\n%s", st)
		}
		lee.Lore(app.Dir, "sync").OK(t)
		if got := me.Rev("refs/heads/main"); got != remoteBefore {
			t.Fatalf("the sync after a search pushed to the personal store: %s → %s", remoteBefore, got)
		}
	})
}

// r7t11PersonalClone is the store clone under stores whose origin is url.
func r7t11PersonalClone(t *testing.T, stores, url string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(stores, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" || !d.IsDir() || d.Name() != ".git" {
			return nil
		}
		b, rerr := os.ReadFile(filepath.Join(p, "config"))
		if rerr == nil && strings.Contains(string(b), url) {
			found = filepath.Dir(p)
		}
		return filepath.SkipDir
	})
	if found == "" {
		t.Fatalf("no store clone of %s under %s", url, stores)
	}
	return found
}
