package wire

// setupcreate.go — the repo lore proposes for "Create a new private repo"
// when the repo url field is empty (realmac item 9):
//
//	answers: create + empty url ─▶ <user>/lore-me free? ── yes ─▶ git@<alias>:<user>/lore-me.git
//	                                 │ exists
//	                                 ▼
//	                              lore-me-2 … lore-me-9, the first free one
//
// The store checks then run against that url: store.read fails (not
// found) and its fix is Run it: gh repo create <user>/<name> --private;
// the page fills the url field from the check's target after the run.

import (
	"context"
	"strconv"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

const (
	// createRepoBase is the proposed personal store repo name (the
	// store.url_suggest prefill uses the same name).
	createRepoBase = "lore-me"
	// createRepoTries bounds the names tried: lore-me, lore-me-2 …
	// lore-me-9. Past that the base name is proposed and store.read
	// says what is wrong with it.
	createRepoTries = 9
)

// repoState is what gh says about one candidate name.
type repoState int

const (
	repoFree    repoState = iota // GitHub answered 404: free to create
	repoTaken                    // the repo exists
	repoUnknown                  // gh could not tell (no gh, offline, …)
)

// createRepoNames is every candidate name, in the order tried.
func createRepoNames() []string {
	out := []string{createRepoBase}
	for i := 2; i <= createRepoTries; i++ {
		out = append(out, createRepoBase+"-"+strconv.Itoa(i))
	}
	return out
}

// pickCreateRepo is the first free name of createRepoNames. A name gh
// cannot answer for stops the search there (proposing a name lore could
// not check beats skipping one that may be free); when every name is
// taken, the base name is proposed and store.read reports it exists.
func pickCreateRepo(state func(name string) repoState) string {
	for _, n := range createRepoNames() {
		switch state(n) {
		case repoFree, repoUnknown:
			return n
		}
	}
	return createRepoBase
}

// createTarget is the url lore proposes for setup's new personal store,
// "" when the setup has no GitHub user yet. gh is asked as the setup's
// own login (GHRepoAs), so the setup's private repos count as taken.
func (sp setupProber) createTarget(ctx context.Context, setup onboard.SetupEntry) string {
	user := setup.GitHub.User
	if user == "" {
		return ""
	}
	alias := setup.GitHub.HostAlias
	if alias == "" {
		alias = probe.GitHubHost
	}
	account := opssetup.GHAccount(setup)
	name := pickCreateRepo(func(n string) repoState {
		g := sp.p.GHRepoAs(ctx, user, n, account)
		switch g.Class {
		case probe.ClassOK:
			return repoTaken
		case probe.ClassNoAccess:
			return repoFree
		}
		return repoUnknown
	})
	return "git@" + alias + ":" + user + "/" + name + gitSuffix
}

// withCreateTarget points every store check of reqs whose answers pick
// create with an empty url at the proposed repo (its answers' url and its
// Target, so the result names it and the page can fill the field). The
// name is worked out once per batch, only when some request needs it.
func (sp setupProber) withCreateTarget(ctx context.Context, reqs []opssetup.ProbeRequest) []opssetup.ProbeRequest {
	target, done := "", false
	out := reqs
	for i, r := range reqs {
		if r.Target != "" || !onboard.IsStoreCheck(r.Check.ID) || !onboard.CreateWithoutURL(r.Answers) {
			continue
		}
		if !done {
			target, done = sp.createTarget(ctx, r.Setup), true
			if target == "" {
				return reqs
			}
			out = append([]opssetup.ProbeRequest(nil), reqs...)
		}
		a := make(onboard.Answers, len(r.Answers)+1)
		for k, v := range r.Answers {
			a[k] = v
		}
		a[onboard.QIDStoreURL] = onboard.Answer{Text: target}
		out[i].Answers, out[i].Target = a, target
	}
	return out
}
