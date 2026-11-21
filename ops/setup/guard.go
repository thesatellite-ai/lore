package setup

// guard.go — setup.guard.install / setup.guard.remove (devco H-119,
// org-setup item 12): lore's client leak guard for every clone on this
// machine, through an opt-in global core.hooksPath entry
// (adapters/machineguard). Never on by default.
//
//	lore setup guard install [--dry-run]
//	   ─▶ machineguard.Install: dispatchers in <lore home>/git-hooks that
//	      run each repo's own hooks (or the previous global hooks folder's)
//	      first, then lore's commit and push checks; the global
//	      core.hooksPath before is recorded
//	lore setup guard remove [--dry-run]
//	   ─▶ machineguard.Remove: the global core.hooksPath exactly as it was
//
// Why machine-wide: the link and `lore doctor --fix` guard the clones they
// run in, so a fresh `git clone` of a client repo where lore never ran
// committed and pushed internal notes unchecked.

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/adapters/machineguard"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names of the machine guard.
const (
	OpGuardInstall = "setup.guard.install"
	OpGuardRemove  = "setup.guard.remove"
)

// guardRESTNoun is the guard ops' REST collection under /v1/setup: two
// segments, so no setup name (one segment) can equal it (see
// leftoversRESTNoun).
const guardRESTNoun = "machine/guard"

// GuardIn is the input of both guard ops.
type GuardIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would change; write nothing"`
}

// GuardOut is what a guard op did (or would do): the machine guard's
// folder, the hooks written, and the global core.hooksPath before.
type GuardOut struct {
	// Removed: setup.guard.remove's result.
	Removed bool `json:"removed,omitempty"`
	// Dir is the guard's folder (<lore home>/git-hooks).
	Dir string `json:"dir"`
	// Wrote are the dispatcher files (install).
	Wrote []string `json:"wrote,omitempty"`
	// PrevOn and Prev: the global core.hooksPath before the install.
	PrevOn bool   `json:"previous_set"`
	Prev   string `json:"previous,omitempty"`
	// Chain is the folder the dispatchers run first ("" = each repo's
	// own .git/hooks).
	Chain string `json:"chain,omitempty"`
	// Already: on before install (hooks rewritten), off before remove.
	Already bool `json:"already,omitempty"`
	// Left: remove found the global core.hooksPath changed since the
	// install and left it (its value).
	Left   string `json:"left,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// guardOut is r as an op output.
func guardOut(r machineguard.Result, removed bool) GuardOut {
	return GuardOut{Removed: removed, Dir: r.Dir, Wrote: r.Wrote, PrevOn: r.PrevOn, Prev: r.Prev, Chain: r.Chain, Already: r.Already, Left: r.Left, DryRun: r.DryRun}
}

// Text implements op.Texter.
func (o GuardOut) Text() string {
	prev := "no global core.hooksPath was set"
	if o.PrevOn {
		prev = "the global core.hooksPath was " + quote(o.Prev)
	}
	chain := "each repo's own hooks (.git/hooks) run first"
	if o.Chain != "" {
		chain = "the hooks in " + o.Chain + " run first, as before"
	}
	if o.Removed {
		var b strings.Builder
		switch {
		case o.Already:
			return "the machine guard is not installed: nothing to remove (`lore setup guard install` turns it on)\n"
		case o.DryRun:
			b.WriteString("would remove the machine guard in " + o.Dir)
		default:
			b.WriteString("✓ removed the machine guard in " + o.Dir)
		}
		switch {
		case o.Left != "":
			b.WriteString("; the global core.hooksPath is now " + quote(o.Left) + ", not lore's folder: left as it is")
		case o.PrevOn:
			b.WriteString("; the global core.hooksPath is " + quote(o.Prev) + " again")
		default:
			b.WriteString("; the global core.hooksPath is unset again")
		}
		if o.DryRun {
			b.WriteString("; nothing written (--dry-run)")
		}
		return b.String() + "\n"
	}
	head := "✓ machine guard installed: git runs every repo's hooks from " + o.Dir
	switch {
	case o.DryRun:
		head = "would install the machine guard: git would run every repo's hooks from " + o.Dir
	case o.Already:
		head = "✓ machine guard already installed in " + o.Dir + ": its hooks rewritten"
	}
	tail := "\n"
	if o.DryRun {
		tail = "\nnothing written (--dry-run)\n"
	}
	return head + " (" + strconv.Itoa(len(o.Wrote)) + " hooks; " + chain + "), then commits and pushes in a client repo are checked for internal notes, also in clones where lore never ran; " + prev + ": `lore setup guard remove` restores it exactly" + tail
}

// guardHome is the lore home the guard's folder goes under.
func guardHome() (string, error) {
	h, err := lorehome.Resolve()
	if err != nil {
		return "", err
	}
	return h.Dir, nil
}

// GuardInstall is setup.guard.install.
func (e engine) GuardInstall(ctx context.Context, in GuardIn) (GuardOut, error) {
	home, err := guardHome()
	if err != nil {
		return GuardOut{}, err
	}
	res, err := machineguard.Install(ctx, gitref.Exec{}, home, in.DryRun)
	return guardOut(res, false), err
}

// GuardRemove is setup.guard.remove.
func (e engine) GuardRemove(ctx context.Context, in GuardIn) (GuardOut, error) {
	home, err := guardHome()
	if err != nil {
		return GuardOut{}, err
	}
	res, err := machineguard.Remove(ctx, gitref.Exec{}, home, in.DryRun)
	return guardOut(res, true), err
}

// guardOps are the machine guard ops over e (registered by guidedOps).
func guardOps(e engine, local func(op.Spec) op.Spec) []op.Entry {
	install := local(op.Spec{Name: OpGuardInstall, Summary: "Opt in: check commits and pushes in every client repo on this machine for internal notes, also clones where lore never ran (a global core.hooksPath entry that runs each repo's own hooks first)",
		Permission: op.PermWrite, Errors: with(),
		Examples: []op.Example{{Title: "preview the machine-wide guard", CLI: "lore setup guard install --dry-run", Want: ""}}})
	install.RESTNoun = guardRESTNoun
	remove := local(op.Spec{Name: OpGuardRemove, Summary: "Remove the machine guard: the global core.hooksPath goes back to exactly what it was before the install",
		Permission: op.PermWrite, Errors: with(),
		Examples: []op.Example{{Title: "preview removing the machine guard", CLI: "lore setup guard remove --dry-run", Want: ""}}})
	remove.RESTNoun = guardRESTNoun
	return []op.Entry{op.Define(install, e.GuardInstall), op.Define(remove, e.GuardRemove)}
}
