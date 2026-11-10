// Package selfupgrade defines `lore upgrade` (layer L5, CLI_REFERENCE 3.5,
// FORMAT_EVOLUTION 8): install the newest lore release, the project's
// pinned one, or exactly --version, however lore was installed.
//
//	upgrade.run ─▶ Env.Upgrader ─▶ Upgrader.Upgrade(flags, confirmed)
//	            ─▶ lines (what was found, downloaded, swapped)
//	            ─▶ a downgrade into a project the older binary could not
//	               write: E_NEEDS_CONFIRM (the surface asks [y/N] and runs
//	               again with --yes; without a terminal the error names it)
//
// It is a Local operation (op.Spec.Local, P3-CFG-1): it never opens a store
// and works outside a repo; it writes the binary (an atomic swap), the
// agent skill bundle and ~/.lore/state/upgrade.lock, never a project file.
// The mechanism lives in adapters/selfupdate behind the Upgrader port, so
// this package stays free of the network and the file system (PASS3
// ONE-PATH, P3-UPG-1..3).
package selfupgrade

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore upgrade`; the bare noun runs upgrade.run).
const (
	Noun  = "upgrade"
	OpRun = "upgrade.run"
	// RunVerb is the verb bare `lore upgrade` runs (the shortcut of
	// cmd/lore and the old binary's bare-verb list).
	RunVerb = "run"
)

// restNoun keeps the REST collection "upgrade" (the default plural would
// be "upgrades").
const restNoun = "upgrade"

// Flags are the choices of one upgrade (CLI_REFERENCE 3.5).
type Flags struct {
	// Check only reports whether a newer release exists.
	Check bool
	// Version installs exactly this release (also a downgrade).
	Version string
	// IgnorePin installs past the project pin.
	IgnorePin bool
}

// Upgrader replaces the running binary; adapters/selfupdate implements it.
type Upgrader interface {
	// Upgrade runs one upgrade and returns the lines it reports, in order.
	// confirmed is the caller's yes for a downgrade into a project the
	// older binary could not write; without it that case is
	// E_NEEDS_CONFIRM.
	Upgrade(ctx context.Context, f Flags, confirmed bool) ([]string, error)
}

// Env is what upgrade.run needs; *wire.App implements it.
type Env interface {
	// Upgrader is this binary's upgrader (E_UNSUPPORTED when it has none).
	Upgrader(ctx context.Context) (Upgrader, error)
}

// RunIn is the input of upgrade.run. --yes is the surface's common flag
// (op.Caller.Yes), read through op.Confirmed.
type RunIn struct {
	Check     bool   `json:"check,omitempty" flag:"check" desc:"report whether a newer release exists; change nothing"`
	Version   string `json:"version,omitempty" flag:"version" desc:"install exactly this release (vX.Y.Z); also downgrades; refused for Homebrew installs"`
	IgnorePin bool   `json:"ignore_pin,omitempty" flag:"ignore-pin" desc:"install past this project's lore_version_pin"`
}

// RunOut is the output of upgrade.run.
type RunOut struct {
	// Lines are what the upgrade reported, in order (CLI_REFERENCE 3.5).
	Lines []string `json:"lines" desc:"what the upgrade found and did, one line each"`
}

// Text prints the lines as v0.1.17 did.
func (o RunOut) Text() string {
	if len(o.Lines) == 0 {
		return ""
	}
	return strings.Join(o.Lines, "\n") + "\n"
}

// Ops returns the upgrade operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Install the newest lore release (or this project's pinned version, or exactly --version)",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{
				errs.UpgradePermission, errs.UpgradeChecksum, errs.UpgradePinned, errs.Offline,
				errs.NotFound, errs.NeedsConfirm, errs.SchemaOld, errs.LockHeld, errs.OldLore,
				errs.Unsupported, errs.Internal,
			},
			// MayAsk: only a downgrade into a project the older binary
			// could not write asks (FORMAT_EVOLUTION 8.9).
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "check for a newer release", CLI: "lore upgrade --check", Want: ""}},
		}, func(ctx context.Context, in RunIn) (RunOut, error) {
			u, err := env.Upgrader(ctx)
			if err != nil {
				return RunOut{}, err
			}
			return Run(ctx, u, in, op.Confirmed(ctx))
		}),
	}
}

// Run is upgrade.run over an Upgrader.
func Run(ctx context.Context, u Upgrader, in RunIn, confirmed bool) (RunOut, error) {
	lines, err := u.Upgrade(ctx, Flags{Check: in.Check, Version: strings.TrimSpace(in.Version), IgnorePin: in.IgnorePin}, confirmed)
	if err != nil {
		return RunOut{}, err
	}
	if lines == nil {
		lines = []string{}
	}
	return RunOut{Lines: lines}, nil
}
