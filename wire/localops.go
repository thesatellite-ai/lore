package wire

// Local operations (op.Spec.Local, P3-CFG-1): `lore config …`,
// `lore check leaks|pr-body` and `lore store show|open|forget|doctor` work
// on this machine's own files and never on rows, so App.Run hands them to
// runLocal before anything is opened:
//
//	App.Run ─► Spec.Local ─► runLocal: decode ─► invoke (no repo, no store,
//	                         no engine; works outside a repo and while the
//	                         config is invalid or a store is unreachable)
//
// The rules behind them (the config schema, the leak index, the v0.1.17
// clone layer) are supplied by the binary through Options.Local; a part
// left nil answers E_UNSUPPORTED naming the command, never a silent pass
// (a leak check that cannot run must refuse, not succeed).

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opscheck "github.com/thesatellite-ai/lore/ops/check"
	opsconfig "github.com/thesatellite-ai/lore/ops/config"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// ConfigSchema is the config.yaml schema a binary supplies (validation of
// `config set`, the whole `config check`).
type ConfigSchema interface {
	ValidateConfig(ctx context.Context, raw []byte) (opsconfig.Verdict, error)
	CheckConfig(ctx context.Context) (opsconfig.CheckOut, error)
}

// LocalPorts are the implementations of the Local operations' rules a
// binary supplies (Options.Local). Each nil part is E_UNSUPPORTED.
type LocalPorts struct {
	Config ConfigSchema
	Leaks  opscheck.Checker
	Clones opsstores.Cloner
}

// unsupportedHint says where a missing port comes from.
const unsupportedHint = "this build does not carry the org configuration and leak index yet; use the release binary"

// unsupported is the error of a Local operation whose port is nil.
func unsupported(cmd string) error {
	return errs.New(errs.Unsupported, cmd+": not available in this build").WithHint(unsupportedHint)
}

// runLocal runs a Local operation without opening the repo or any store.
func (a *App) runLocal(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	// P3-MISC-2: a Local MayAsk operation reads the caller's yes itself.
	return e.Invoke(op.WithConfirmed(ctx, c.Yes), in)
}

// ReadConfigFile reads <home>/config.yaml (opsconfig.Port).
func (a *App) ReadConfigFile(_ context.Context) (opsconfig.File, error) {
	path := configPath(a.opts.Home)
	f := opsconfig.File{Path: path, Mode: configFileMode}
	raw, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return f, nil
	case err != nil:
		return f, errs.Wrap(errs.Internal, "read "+path, err)
	}
	f.Raw, f.Exists = raw, true
	if info, serr := os.Stat(path); serr == nil {
		f.Mode = uint32(info.Mode().Perm())
	}
	return f, nil
}

// WriteConfigFile writes f atomically with its mode (opsconfig.Port).
func (a *App) WriteConfigFile(_ context.Context, f opsconfig.File) error {
	return writeFileAtomic(f.Path, f.Raw, fs.FileMode(f.Mode))
}

// ValidateConfig forwards to Options.Local.Config (opsconfig.Port).
func (a *App) ValidateConfig(ctx context.Context, raw []byte) (opsconfig.Verdict, error) {
	if a.opts.Local.Config == nil {
		// Fail closed: a set that cannot be validated writes nothing.
		return opsconfig.Verdict{}, unsupported("config set")
	}
	return a.opts.Local.Config.ValidateConfig(ctx, raw)
}

// CheckConfig forwards to Options.Local.Config (opsconfig.Port).
func (a *App) CheckConfig(ctx context.Context) (opsconfig.CheckOut, error) {
	if a.opts.Local.Config == nil {
		return opsconfig.CheckOut{}, unsupported("config check")
	}
	return a.opts.Local.Config.CheckConfig(ctx)
}

// CheckLeaks forwards to Options.Local.Leaks (opscheck.Checker); --rows is
// the core's every-row re-scan (wire/rescan.go, SQ12), the same in every
// binary.
func (a *App) CheckLeaks(ctx context.Context, in opscheck.LeaksIn) (opscheck.LeaksOut, error) {
	if in.Rows {
		msg, err := a.RescanRows(ctx)
		return opscheck.LeaksOut{Message: msg}, err
	}
	if a.opts.Local.Leaks == nil {
		return opscheck.LeaksOut{}, unsupported("check leaks")
	}
	return a.opts.Local.Leaks.CheckLeaks(ctx, in)
}

// CheckText forwards to Options.Local.Leaks (opscheck.Checker).
func (a *App) CheckText(ctx context.Context, in opscheck.TextIn) error {
	if a.opts.Local.Leaks == nil {
		return unsupported("check pr-body")
	}
	return a.opts.Local.Leaks.CheckText(ctx, in)
}

// ShowClones forwards to Options.Local.Clones (opsstores.Cloner).
func (a *App) ShowClones(ctx context.Context, in opsstores.ShowIn) (opsstores.ClonesOut, error) {
	if a.opts.Local.Clones == nil {
		return opsstores.ClonesOut{}, unsupported("store show")
	}
	return a.opts.Local.Clones.ShowClones(ctx, in)
}

// OpenClone forwards to Options.Local.Clones (opsstores.Cloner).
func (a *App) OpenClone(ctx context.Context, in opsstores.OpenIn) (opsstores.OpenOut, error) {
	if a.opts.Local.Clones == nil {
		return opsstores.OpenOut{}, unsupported("store open")
	}
	return a.opts.Local.Clones.OpenClone(ctx, in)
}

// ForgetClone forwards to Options.Local.Clones (opsstores.Cloner).
func (a *App) ForgetClone(ctx context.Context, in opsstores.ForgetIn) (opsstores.ForgetOut, error) {
	if a.opts.Local.Clones == nil {
		return opsstores.ForgetOut{}, unsupported("store forget")
	}
	return a.opts.Local.Clones.ForgetClone(ctx, in)
}

// DoctorClones forwards to Options.Local.Clones (opsstores.Cloner).
func (a *App) DoctorClones(ctx context.Context) (opsstores.ClonesOut, error) {
	if a.opts.Local.Clones == nil {
		return opsstores.ClonesOut{}, unsupported("store doctor")
	}
	return a.opts.Local.Clones.DoctorClones(ctx)
}
