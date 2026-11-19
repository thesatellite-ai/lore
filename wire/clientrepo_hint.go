package wire

// clientrepo_hint.go — I-13: in a client repo (a hosted remote an org
// claims, guards on) no line lore prints may send the person to
// `lore init`. Following it wrote .lore/, .gitattributes, git hooks and
// lore's pointer into CLAUDE.md/AGENTS.md of the client's repository, and
// the write still failed afterwards ("no internal project is linked").
// The step that works there is the org link, kept only in the org's store.
//
//	any refusal ─▶ host.Runner ─▶ App.ClientRepoHints ─▶ hint names `lore init`?
//	                                     │ yes, and Options.ClientRepo says client
//	                                     ▼
//	                       clientRepoHint: lore org link … / --to <org>/<folder>
//
// lore init itself asks first in such a repo (ConfirmClientRepoInit).

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
)

// ClientRepo is Options.ClientRepo's answer: the org a checkout is a
// client repo of, and the store folder it is linked to.
type ClientRepo struct {
	// Org is the org's name; "" when the checkout is no client repo.
	Org string
	// Folder is the linked folder ("" when no client link names the repo).
	Folder string
	// Unlisted is the org of a checkout that is a client repo only by the
	// fail-closed default (Org is "" then): it has a hosted remote, no
	// own: pattern or own link of the org names it, and no repos: pattern
	// or client link does either. Why (devco D-525): an admin who set the
	// team up inside the org's own repo without own: patterns saw "a
	// client repo" from lore fix, setup --agent-hooks and whoami with no
	// cause and no way out; each of them now names both (UnlistedOwnStep).
	Unlisted string
	// Remote is the checkout's hosted remote (host/owner/repo) when
	// Unlisted is set: the own: pattern that would make it an own repo.
	Remote string
}

// UnlistedOwnStep is why a checkout is a client repo only by the
// fail-closed default and the command that declares it the org's own
// repo; "" when c is not such a checkout. lore fix's hooks step, setup
// --agent-hooks' refusal and whoami's repo line say it, so the three agree.
func UnlistedOwnStep(c ClientRepo) string {
	if c.Unlisted == "" || c.Remote == "" {
		return ""
	}
	step := "no own: pattern of " + c.Unlisted + " matches this repo's remote " + c.Remote + ", so lore treats it as a client repo (fail closed); if it is " + c.Unlisted + "'s own repo: lore org config set --org " + c.Unlisted + " --key own --add " + c.Remote + " --yes"
	if host, _, _ := strings.Cut(c.Remote, "/"); !strings.Contains(host, ".") {
		// devco D-706: a server reached by its ssh name (host:/srv/git/x)
		// has no forge host; the pattern names it the way this machine
		// spells it, and a teammate who reaches it under another name
		// needs that spelling too
		step += " (" + host + " is this machine's name for the server; a teammate who reaches it under another name adds that spelling too)"
	}
	return step
}

// UnlistedClientRepo is ClientRepo for a checkout that resolves to role
// client: Unlisted and Remote set when no pattern or link of org claims
// it as a client's (orgresolve.ClientByMatch is false), so the role is
// the fail-closed default alone. Zero for a checkout with no hosted
// remote (lore whoami names that place on its own).
func UnlistedClientRepo(org *orgconf.Org, links []orgresolve.Link, remotes []string) ClientRepo {
	if org == nil || len(remotes) == 0 || orgresolve.ClientByMatch(org, links, remotes) {
		return ClientRepo{}
	}
	// The own: line is a command for the team's shared lore-org.yaml: a
	// remote on a forge host (a dot in it) is named first, the spelling
	// every teammate shares (D-531: an ssh alias is resolved to its host
	// before this). devco D-706: a repo whose only remote is a server
	// reached by its ssh name (scp-style myhost:/srv/git/tools, or
	// ssh://myhost/srv/git/tools; both normalise to myhost/srv/git/tools)
	// was client with no word why and no way out; its remote is named too,
	// with the caveat UnlistedOwnStep adds.
	for _, r := range remotes {
		if host, _, _ := strings.Cut(r, "/"); strings.Contains(host, ".") {
			return ClientRepo{Unlisted: org.Name, Remote: r}
		}
	}
	return ClientRepo{Unlisted: org.Name, Remote: remotes[0]}
}

// initCommandText is how a hint names lore init.
const initCommandText = "lore init"

// initSecondProject marks a hint that warns against lore init (a checkout
// of a lore repo that only lacks .lore/ here) rather than advising it.
const initSecondProject = "would make a second project"

// clientRepo is Options.ClientRepo's answer for this app's repo (zero
// without the hook).
func (a *App) clientRepo(ctx context.Context) ClientRepo {
	if a == nil || a.opts.ClientRepo == nil {
		return ClientRepo{}
	}
	return a.opts.ClientRepo(ctx)
}

// UnlistedOwnHere is UnlistedOwnStep for this app's repo: why it is a
// client repo only by the fail-closed default and how to declare it own,
// "" otherwise. Exported for renderhost's setup --agent-hooks refusal.
func (a *App) UnlistedOwnHere(ctx context.Context) string {
	return UnlistedOwnStep(a.clientRepo(ctx))
}

// clientRepoHint is the step that sets lore up for a client repo: the org
// link (unlinked), or the linked folder's --to (linked).
func clientRepoHint(c ClientRepo) string {
	return clientRepoStep(c) + "; do not run lore init here"
}

// clientRepoStep is clientRepoHint without its last clause (lore init's
// own refusal names --yes instead).
func clientRepoStep(c ClientRepo) string {
	if c.Folder != "" {
		return "this is a client repo of " + c.Org + ", linked to " + c.Org + "/" + c.Folder + ": lore keeps its notes there, never in the client's repository (name it per write with --to " + c.Org + "/" + c.Folder + "; `lore whoami` shows the link)"
	}
	return "this is a client repo of " + c.Org + ": lore keeps its notes in " + c.Org + "'s store, never in the client's repository; link it with `lore org link --org " + c.Org + " --project <folder>` (`lore store folder list --store " + c.Org + "` lists the folders), or name a folder per write with --to " + c.Org + "/<folder> and per read with --store " + c.Org + "/<folder>" // devco:D-591: a read there takes --store
}

// advisesInit reports whether s tells the person to run lore init.
func advisesInit(s string) bool {
	return strings.Contains(s, initCommandText) && !strings.Contains(s, initSecondProject)
}

// ClientRepoHints is the Runner's last word on a refusal: when its hint
// (or, with no hint, its message) advises lore init and this repo is a
// client repo, the hint names the org link instead. Every other error is
// returned as it is. The client check runs only for such an error, so a
// plain refusal never resolves the org.
func (a *App) ClientRepoHints(ctx context.Context, err error) error {
	var e *errs.Error
	if err == nil || !errors.As(err, &e) {
		return err
	}
	if !advisesInit(e.Hint) && !(e.Hint == "" && advisesInit(e.Message)) {
		return err
	}
	c := a.clientRepo(ctx)
	if c.Org == "" {
		return err
	}
	out := *e
	out.Hint = clientRepoHint(c)
	return &out
}

// clientInitWrites names every file lore init writes in a client's
// repository: the tracked ones (.lore/, .gitattributes, a "# lore" block
// of its local-file lines appended to .gitignore, the tool pointer) and
// the clone's own git hooks and merge drivers. devco:N-10: the .gitignore
// block was written but named neither here nor in the docs.
const clientInitWrites = ".lore/, .gitattributes, a \"# lore\" block in .gitignore (lore's local files: lock, state, backups), its pointer into CLAUDE.md/AGENTS.md, and git hooks and merge drivers in .git"

// ConfirmClientRepoInit is I-13's guard of lore init (and its registry
// op repo.init) in a client repo with no .lore/ yet: init writes .lore/,
// .gitattributes, git hooks and its pointer into CLAUDE.md/AGENTS.md, all
// in the client's repository, so it answers E_NEEDS_CONFIRM until the
// caller says yes (--yes, a tty answer, the REST yes field). A clone
// already set up on this machine, and a repo that is no client repo, pass.
//
// devco:T-1: a client repo whose .lore/ came with the checkout (lore
// 0.1.17 data, or a teammate's init) is not set up yet in this clone:
// init would adopt it and still write the git hooks, merge drivers and the
// .gitignore block, so it asks there too. Only a clone this machine set up
// before (its repo cache exists) passes.
func (a *App) ConfirmClientRepoInit(ctx context.Context, yes bool) error {
	if yes {
		return nil
	}
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return nil
	}
	if loreFolderAt(a.loreRootFrom(top)) && a.cloneSetUp(top) {
		return nil
	}
	// asked before init sets the project up: the situation gathered now
	// would be stale for init's own writes, so it is not kept
	// (devco:D-388-init)
	cr := a.clientRepo(withUncachedSituation(ctx))
	if cr.Org == "" {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "this is a client repo of "+cr.Org+": lore init would write "+clientInitWrites+" of the client's repository; nothing written").
		WithQuestion("set lore up inside this client's repository anyway?").
		WithHint(clientRepoStep(cr) + "; to set lore up inside the client's repository anyway: lore init --yes")
}

// cloneSetUp reports whether lore set up the clone at top on this machine
// before: its repo cache under the lore home exists. A checkout that only
// carries a committed .lore/ has none.
func (a *App) cloneSetUp(top string) bool {
	if a.opts.Home == "" {
		return true // no lore home to tell by: keep the old pass
	}
	_, err := os.Lstat(RepoCachePath(a.opts.Home, top))
	return err == nil
}
