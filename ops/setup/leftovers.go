package setup

// leftovers.go — setup.leftovers.clear (owner report 2026-11-08): clear
// the top-level identity: or personal.store of ~/.lore/config.yaml once no
// setup uses it (onboard.Leftovers). setup.list reports the leftovers;
// Home shows each with a Clear button that calls this op.
//
// The op is narrow on purpose: the page allowlist gets one op that can
// remove exactly these two keys, and only while they are leftovers, never
// the general `lore config unset` (which can remove any key).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// OpLeftoversClear is setup.leftovers.clear.
const OpLeftoversClear = "setup.leftovers.clear"

// leftoversRESTNoun is the op's REST collection under /v1/setup: two
// segments, so no setup name (one segment) can equal it (see
// trashRESTNoun).
const leftoversRESTNoun = "machine/leftovers"

// LeftoverClearer is the optional Store upgrade that removes one leftover
// key from config.yaml (wire's store has it). It removes the key whether
// or not a setup uses it; the op checks first.
type LeftoverClearer interface {
	// ClearLeftover removes the key of kind k; found=false when it was
	// already gone.
	ClearLeftover(ctx context.Context, k onboard.LeftoverKind) (found bool, err error)
}

// LeftoversClearIn is setup.leftovers.clear's input.
type LeftoversClearIn struct {
	What   onboard.LeftoverKind `json:"what" arg:"0" desc:"identity or personal_store"`
	DryRun bool                 `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be removed; write nothing"`
}

// LeftoversClearOut is what was (or would be) removed.
type LeftoversClearOut struct {
	Kind onboard.LeftoverKind `json:"kind"`
	// Key is the dotted config key removed.
	Key   string `json:"key"`
	Value string `json:"value"`
	// File is config.yaml's path as the person reads it (~/.lore/config.yaml).
	File   string `json:"file"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// leftoversFile is how the op names config.yaml (the page and the CLI).
const leftoversFile = "~/.lore/config.yaml"

// Text implements op.Texter.
func (o LeftoversClearOut) Text() string {
	if o.DryRun {
		return "would remove " + o.Key + " (" + o.Value + ") from " + o.File + "; nothing written (--dry-run)\n"
	}
	return "✓ removed " + o.Key + " (" + o.Value + ") from " + o.File + "\n"
}

// LeftoversClear is setup.leftovers.clear: refuses a kind no longer left
// over (absent, or a setup uses it), so a stale page never clears what a
// setup made since needs.
func (e engine) LeftoversClear(ctx context.Context, in LeftoversClearIn) (LeftoversClearOut, error) {
	k := onboard.LeftoverKind(strings.TrimSpace(string(in.What)))
	if !k.Valid() {
		return LeftoversClearOut{}, errs.New(errs.InvalidInput, "setup leftovers clear: what must be identity or personal_store, got "+quote(string(in.What))).
			WithHint("lore setup list shows the leftovers on this machine")
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return LeftoversClearOut{}, err
	}
	lc, ok := st.(LeftoverClearer)
	if !ok {
		return LeftoversClearOut{}, errs.New(errs.Unsupported, "this lore cannot clear config.yaml keys").WithHint("edit ~/.lore/config.yaml by hand")
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return LeftoversClearOut{}, err
	}
	l, ok := onboard.LeftoverOf(cfg, k)
	if !ok {
		return LeftoversClearOut{}, errs.New(errs.NotFound, k.ConfigKey()+" is not a leftover: "+leftoverWhyNot(cfg, k)).
			WithHint("nothing written; lore setup list shows the leftovers on this machine")
	}
	out := LeftoversClearOut{Kind: k, Key: l.Key, Value: l.Value, File: leftoversFile, DryRun: in.DryRun}
	if in.DryRun {
		return out, nil
	}
	if _, err := lc.ClearLeftover(ctx, k); err != nil {
		return LeftoversClearOut{}, err
	}
	return out, nil
}

// leftoverWhyNot says why k is not a leftover of cfg: absent, or used.
func leftoverWhyNot(cfg onboard.Config, k onboard.LeftoverKind) string {
	absent := cfg.TopIdentity.IsZero()
	if k == onboard.LeftoverPersonalStore {
		absent = strings.TrimSpace(cfg.SharedStore) == ""
	}
	if absent {
		return "config.yaml does not set it"
	}
	return "a setup uses it"
}

// leftoverOps is setup.leftovers.clear over e (registered by guidedOps).
func leftoverOps(e engine, local func(op.Spec) op.Spec) []op.Entry {
	s := local(op.Spec{Name: OpLeftoversClear, Summary: "Clear the identity or personal.store of ~/.lore/config.yaml once no setup uses it (setup list shows them)",
		Permission: op.PermWrite, Errors: with(errs.NotFound),
		Examples: []op.Example{{Title: "preview clearing a leftover personal store", CLI: "lore setup leftovers clear personal_store --dry-run", Want: ""}}})
	s.RESTNoun = leftoversRESTNoun
	return []op.Entry{op.Define(s, e.LeftoversClear)}
}
