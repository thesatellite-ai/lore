package wire

// solesetup.go — the one routing of a command run where no org applies
// (the home folder, a plain folder) to the only org setup of this machine
// (devco:D-692, D-683).
//
//	member add | team … | task next, here no repo, store clone or setup's folder
//	   no --setup, no LORE_SETUP
//	   exactly one enabled org setup ─▶ ctx carries it (as --setup does),
//	                                    a notice names it
//	   a roster op, the default setup is an org setup ─▶ that setup, named
//	   a roster change, exactly one admin org setup   ─▶ that setup, named
//	   none, or two or more          ─▶ ctx unchanged: the command's own
//	                                    refusal names --setup <setup>, the
//	                                    full command per setup and how to
//	                                    make one the default (D-704)
//
// Why: from the home folder `member add` refused "no org applies to this
// folder" although the machine had one org setup and every write naming
// `--to <org>` routed by itself (withSetupOfTarget), and the member's plain
// `task next` in their home folder said "--store does not apply" while
// `task next --store <org>` ranked the store's tasks. A guess between two
// setups is never made: two orgs keep the refusal and its hint.

import (
	"context"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// soleOrgSetup is the name of the only enabled setup on this machine
// that works in an org, "" when there is none or more than one.
func (a *App) soleOrgSetup() string {
	if s := a.orgSetups(); len(s) == 1 {
		return s[0]
	}
	return ""
}

// setupNamed reports whether the caller named a setup (--setup or
// LORE_SETUP): routing then never overrides it.
func setupNamed(ctx context.Context) bool {
	return strings.TrimSpace(op.SetupOf(ctx)) != "" || strings.TrimSpace(os.Getenv(opssetup.EnvSetup)) != ""
}

// withSoleOrgSetup is ctx with the only org setup of this machine as the
// --setup choice, when the caller named none and one exists; the notice
// says which setup the command used and why, so the output names it.
// ok=false: ctx is unchanged (a setup was named, or none or two exist).
func (a *App) withSoleOrgSetup(ctx context.Context) (context.Context, bool) {
	if setupNamed(ctx) {
		return ctx, false
	}
	name := a.soleOrgSetup()
	if name == "" {
		return ctx, false
	}
	a.sayRoutedSetup(name)
	return op.WithSetup(ctx, name), true
}

// sayRoutedSetup is the notice of a command routed to setup name by
// withSoleOrgSetup: the output names the setup it used.
func (a *App) sayRoutedSetup(name string) {
	a.sayRouted(name, "the only org setup on this machine")
}

// sayRouted is the notice of a command run where no org applies and
// routed to setup name; why says which rule chose it.
func (a *App) sayRouted(name, why string) {
	if a.opts.Notice != nil {
		a.opts.Notice("setup " + name + " (" + why + ")")
	}
}

// soleAdminSetup is the name of the only enabled org setup of kind admin
// on this machine, "" when there is none or more than one (devco:D-704).
func (a *App) soleAdminSetup() string {
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return ""
	}
	var found []string
	for _, s := range cfg.Setups {
		if !s.Disabled && strings.TrimSpace(s.Org) != "" && s.Kind == onboard.KindAdmin {
			found = append(found, s.Name)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

// outsideEveryPlace reports whether dir is no code repo, no store clone:
// the folder where no org applies unless a setup's folders cover it.
func outsideEveryPlace(ctx context.Context, dir string) bool {
	return !inStore(dir) && !insideWorkTree(ctx, dir)
}

// routeSoleSetup is the one routing of the commands that need an org and
// name none, run where no org applies (see the file comment): plain task
// next (no --store) and the direct roster ops (a read with no --store).
// It runs at the top of Run, before anything resolves the folder's org:
// that answer is kept for the run, so a setup put in ctx later is not
// seen. A folder whose setup works in an org (its folders, a repo pin, a
// default) keeps it: task next ranks that setup's store. Task next with
// no setup to rank refuses naming the ways that work from here (it is no
// repo-only read: --store and --setup reach a queue from anywhere); a
// roster op keeps its own refusal.
func (a *App) routeSoleSetup(ctx context.Context, e op.Entry, d op.Decoder) (context.Context, error) {
	name := e.Spec().Name
	next, roster := name == opscoord.OpTaskNext, profilestore.IsDirect(name)
	if (!next && !roster) || !outsideEveryPlace(ctx, a.opts.RepoDir) {
		return ctx, nil
	}
	if env := strings.TrimSpace(os.Getenv(opssetup.EnvSetup)); next && env != "" && strings.TrimSpace(op.SetupOf(ctx)) == "" {
		return op.WithSetup(ctx, env), nil // LORE_SETUP names the queue as --setup does
	}
	if setupNamed(ctx) {
		return ctx, nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return ctx, nil // the op reports its own decode error
	}
	if s, ok := in.String("store"); ok && s != "" {
		return ctx, nil
	}
	res, rerr := a.SetupResolution(ctx, "")
	switch {
	case rerr == nil && res.Entry.Org != "" && next:
		return op.WithSetup(ctx, res.Active.Name), nil // this folder's org setup names the queue
	case rerr == nil && res.Entry.Org != "" && roster && res.Active.Why == opssetup.WhyDefault:
		// devco:D-704: the default setup answers here; say so, as the
		// other routings do, so the person sees whose roster changed
		a.sayRouted(res.Active.Name, "the default setup")
		return op.WithSetup(ctx, res.Active.Name), nil
	case (rerr != nil || res.Entry.Org != "") && roster:
		return ctx, nil // an org applies to this folder, or its own refusal comes
	}
	if routed, ok := a.withSoleOrgSetup(ctx); ok {
		return routed, nil
	}
	if roster && !e.Spec().OnlyReads() {
		if name := a.soleAdminSetup(); name != "" {
			// devco:D-704: only an admin changes the roster, so of two
			// org setups the one where this person is an admin is the
			// only one the change can be meant for
			a.sayRouted(name, "the only org setup where you are an admin")
			return op.WithSetup(ctx, name), nil
		}
	}
	if !next {
		return ctx, nil // rosterNowhereErr names --setup <setup> and the default
	}
	where := a.opts.RepoDir
	if where == "" {
		where = "this folder"
	}
	return ctx, errs.New(errs.Unsupported, "task next ranks one queue: a code repo's tasks in the repo, an org store's tasks named by --store or --setup; "+where+" is not inside a code repo and names neither").
		WithHint("rank an org store's tasks from here with `lore task next --store <org>`" + knownList("orgs", a.knownOrgs()) + " or `lore task next --setup <setup>`" + knownList("setups", a.orgSetups()) + "; or cd into the code repo whose tasks you want")
}
