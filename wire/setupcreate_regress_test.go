package wire_test

// setupcreate_regress_test.go — "Create a new private repo" with an empty
// url, and every gh call made for a setup going as that setup's account.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// rwToGitHubStep saves kind, identity and github for setup as login.
func rwToGitHubStep(t *testing.T, w *rwWorld, setup, login string) {
	t.Helper()
	a := w.Account(login)
	for _, s := range []struct {
		id      string
		answers rwAnswers
	}{
		{"kind", rwAnswers{"kind": str("solo"), "name": str(setup)}},
		{"identity", rwAnswers{"name": str("Pat Example"), "email": str(a.Email)}},
		{"github", rwAnswers{"github_account": str(login)}},
	} {
		w.Check(setup, s.id, s.answers)
		if err := w.Apply(setup, s.id, s.answers); err != nil {
			t.Fatalf("step %s: %v", s.id, err)
		}
	}
}

// rwCreateEmpty checks the Stores step with Create and an empty url, runs
// the runnable fix lore offers, and saves with the url the page then
// shows (the check's target). It returns that url.
func rwCreateEmpty(t *testing.T, w *rwWorld, setup string) string {
	t.Helper()
	url, err := rwCreate(t, w, setup, "")
	if err != nil {
		t.Fatalf("Save with the created repo %s refused: %v", url, err)
	}
	return url
}

// rwCreate is rwCreateEmpty with the url typed (url "" leaves it empty);
// it returns Save's error instead of failing on it.
func rwCreate(t *testing.T, w *rwWorld, setup, typed string) (string, error) {
	t.Helper()
	ans := rwAnswers{"personal_store": str("create"), "store_url": str(typed)}
	out := w.Check(setup, "personal_store", ans)
	var fix *rwCheck
	for i, c := range out.Checks {
		if c.Fix.Runnable && len(c.Fix.Command) > 2 && c.Fix.Command[0] == "gh" && c.Fix.Command[1] == "repo" {
			fix = &out.Checks[i]
			break
		}
	}
	if fix == nil {
		t.Fatalf("Create with an empty url offers no gh repo create to run: %+v", out.Checks)
	}
	var run rwNone
	rwMust(w, "setup.fix", map[string]string{"setup": setup, "check": fix.Check, "target": fix.Target}, &run)
	url := fix.Target
	if !strings.Contains(url, "/lore-me") {
		t.Fatalf("fix target %q is not the proposed lore-me repo", url)
	}
	ans["store_url"] = str(url)
	w.Check(setup, "personal_store", ans)
	return url, w.Apply(setup, "personal_store", ans)
}

// Finding 0658d25b ITEM 9b (realmac): "Create a new private repo" with
// the url left empty skipped every store check ("pick or paste the repo URL
// first"), offered no Run it, and Save said "create needs a store url" —
// the default choice could never be saved. Lore must propose
// <account>/lore-me (the first free lore-me-N when it exists), create it as
// the setup's account, and the url saved is that fix's target.
func TestRegress_EmptyRepoURLFilledAfterRun(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	cases := []struct {
		name, login, want string
		exists            bool
	}{
		{"free name", "octo-work", "octo-work/lore-me.git", false},
		{"lore-me taken", "octo-personal", "octo-personal/lore-me-2.git", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRegressWorld(t, rwShape{GHAccounts: 2, SSH: "several"})
			if c.exists {
				w.AddRemote(c.login, "lore-me")
			}
			rwToGitHubStep(t, w, "personal", c.login)
			url := rwCreateEmpty(t, w, "personal")
			if !strings.HasSuffix(url, c.want) {
				t.Fatalf("proposed %s, want …%s", url, c.want)
			}
			if !strings.Contains(w.Config(), "personal_store: "+url) {
				t.Fatalf("config.yaml does not record the created repo %s:\n%s", url, w.Config())
			}
		})
	}
}

// Finding 0658d25b ITEM 9 (realmac): for a setup whose account was not
// gh's active login, Run it ran `gh repo create` as the active login
// ("octo-personal cannot create a repository for octo-work") and
// store.private asked GitHub as the active login (404 for the other
// account's private repo). Every gh call made for the setup (fix and
// checks) must carry the setup account's GH_TOKEN.
func TestRegress_GhFixRunsAsSetupAccount(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newRegressWorld(t, rwShape{GHAccounts: 2, SSH: "several"}) // octo-personal is gh's active login
	rwToGitHubStep(t, w, "work", "octo-work")
	w.ResetLogs()
	// The url typed, so this pins the account alone (the empty-url case is
	// TestRegress_EmptyRepoURLFilledAfterRun).
	_, saveErr := rwCreate(t, w, "work", "git@work-gh:octo-work/lore-me.git")
	calls := w.Lines("gh", "")
	sawCreate, sawRepoAPI := false, false
	for _, l := range calls {
		if strings.Contains(l, "args=auth ") { // token lookups, not GitHub calls
			continue
		}
		if !strings.Contains(l, "account=octo-work ") {
			t.Errorf("gh call for setup work (octo-work) ran as another account: %s", l)
		}
		sawCreate = sawCreate || strings.Contains(l, "args=repo create")
		sawRepoAPI = sawRepoAPI || strings.Contains(l, "args=api repos/")
	}
	if saveErr != nil {
		t.Errorf("Save after Run it refused: %v", saveErr)
	}
	if !sawCreate || !sawRepoAPI {
		t.Fatalf("expected gh repo create and a gh api repos/ check, got:\n%s", strings.Join(calls, "\n"))
	}
}
