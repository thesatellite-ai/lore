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
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ClientRepo is Options.ClientRepo's answer: the org a checkout is a
// client repo of, and the store folder it is linked to.
type ClientRepo struct {
	// Org is the org's name; "" when the checkout is no client repo.
	Org string
	// Folder is the linked folder ("" when no client link names the repo).
	Folder string
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
	return "this is a client repo of " + c.Org + ": lore keeps its notes in " + c.Org + "'s store, never in the client's repository; link it with `lore org link --org " + c.Org + " --project <folder>` (`lore store folder list --store " + c.Org + "` lists the folders), or name a folder per write with --to " + c.Org + "/<folder>"
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

// ConfirmClientRepoInit is I-13's guard of lore init (and its registry
// op repo.init) in a client repo with no .lore/ yet: init writes .lore/,
// .gitattributes, git hooks and its pointer into CLAUDE.md/AGENTS.md, all
// in the client's repository, so it answers E_NEEDS_CONFIRM until the
// caller says yes (--yes, a tty answer, the REST yes field). A repo already
// set up, and a repo that is no client repo, pass.
func (a *App) ConfirmClientRepoInit(ctx context.Context, yes bool) error {
	if yes {
		return nil
	}
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" || loreFolderAt(a.loreRootFrom(top)) {
		return nil
	}
	cr := a.clientRepo(ctx)
	if cr.Org == "" {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "this is a client repo of "+cr.Org+": lore init would write .lore/, .gitattributes, git hooks and its pointer into CLAUDE.md/AGENTS.md of the client's repository; nothing written").
		WithQuestion("set lore up inside this client's repository anyway?").
		WithHint(clientRepoStep(cr) + "; to set lore up inside the client's repository anyway: lore init --yes")
}
