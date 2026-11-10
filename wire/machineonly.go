package wire

// machineonly.go — the machine-local rows of the guided setup outside a
// repo (ONBOARDING_DESIGN 1.2, 4.5, Q1): `lore setup`, `lore setup join`
// and `lore doctor --all` are run from ~ on a fresh machine, and their
// bookkeeping (setup_progress, known_repos) lives in the machine store
// (~/.lore/state/machine.db), which needs no repo. App.state opens the
// repo first and so refuses outside one (E_NOT_A_STORE); machineApp opens
// the machine store alone there.
//
//	machineApp ── inside a lore repo whose full open works ─▶ a (its full open attaches the machine store)
//	           ├▶ inside one whose attached store is offline/broken ─▶ the machine-only App below
//	           └▶ outside ─▶ openDetached(nil): machine store only, default target
//	                         (opened once per App, closed with it)

import (
	"context"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"

	"github.com/thesatellite-ai/lore/model/errs"
)

// machineApp is the App whose engine serves this machine's machine-local
// rows: a inside a git work tree, else a machine-only App opened once and
// kept (a.Close closes it). Only the setup store and known_repos use it;
// every other command keeps requiring a repo (fail closed).
//
// Inside a lore repo the repo's full open also attaches every store the
// repo names; one offline or broken store (E_STORE_UNREACHABLE,
// E_NOT_A_STORE) must not take the setup bookkeeping with it, or the
// setup page that repairs it cannot even load its plan. Then the
// machine-only App serves the rows. Never both at once: the fallback is
// opened only after the repo open failed (open closes what it had
// opened; App.state keeps the failure), and if a later open of the repo
// succeeds (doctor's NoCreate retry) the fallback is closed first.
func (a *App) machineApp(ctx context.Context) (*App, error) {
	if insideWorkTree(ctx, a.opts.RepoDir) && opssetup.LoreRepoAt(a.opts.RepoDir) {
		_, err := a.Engine(ctx)
		if err == nil {
			return a, a.closeMachineOnly()
		}
		if !errs.Has(err, errs.NotAStore) && !errs.Has(err, errs.StoreUnreachable) {
			return a, nil // the command's own open reports it, as before
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.machOpened {
		a.machOpened = true
		a.mach, a.machErr = openMachineOnly(ctx, a.opts)
	}
	return a.mach, a.machErr
}

// closeMachineOnly closes the machine-only App if one is open (the repo's
// own engine now serves the rows) and forgets it, so a later fallback
// opens a fresh one.
func (a *App) closeMachineOnly() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mach == nil {
		a.machOpened, a.machErr = false, nil
		return nil
	}
	err := a.mach.Close()
	a.mach, a.machOpened, a.machErr = nil, false, nil
	return err
}

// openMachineOnly is an App over the machine store alone (openDetached
// with no store). Its repo-only state refuses as StoreOnlyRoster's does.
func openMachineOnly(ctx context.Context, opts Options) (*App, error) {
	m, err := New(detachedOpts(opts))
	if err != nil {
		return nil, err
	}
	st, err := openDetached(ctx, m.opts, nil) // m.opts: New filled the defaults (Wall)
	if err != nil {
		return nil, err
	}
	st.eng.SetRedactHook(m.redactTask) // D21, as App.state does
	m.mu.Lock()
	m.opened, m.st = true, st
	m.cfgOpened = true
	m.cfgErr = errs.New(errs.NotAStore, "wire: this command needs a repo; only the setup bookkeeping runs outside one").
		WithHint("run it inside a git repository")
	m.mu.Unlock()
	return m, nil
}

// detachedOpts is opts for a second App opened beside the command's own
// (machine-only, one org folder): without the repo audit sink. New binds
// an appBinder sink to the App it builds, so sharing the command's sink
// would rebind it to the second App, and once that App is closed the
// command's own writes fail reading the tool session ids through it
// ("the app is closed"). A detached open records into its stores'
// machine-local trails (routedAudit) and never uses opts.Audit.
func detachedOpts(opts Options) Options {
	opts.Audit = nil
	return opts
}
