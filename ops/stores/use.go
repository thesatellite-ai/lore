package stores

// `lore store use <path>`: use my own clone (owner decision D1,
// 2026-11-12). A person git-clones a store anywhere and points lore at
// it; from then on lore reads and writes that clone instead of making its
// own under ~/.lore/stores. `--default <store>` goes back to lore's own.
//
//	store.use <path> [--store <s>] ─▶ the clone's top (lore-store.yaml)
//	   ─▶ its origin matched against the configured stores (--store picks
//	      one when the origin matches several) ─▶ lore's own clone of it
//	      holds no unpushed work ─▶ the path recorded machine-locally
//	      (~/.lore/state/store-clones.json; never synced)
//	store.use --default <store> ─▶ the record dropped; the clone is left
//	      where it is (lore never deletes a clone it did not make)

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpUse points lore at a person's own clone of a store.
const OpUse = "store.use"

// UseIn is the input of store.use.
type UseIn struct {
	Path    string `json:"path,omitempty" arg:"0" desc:"your own git clone of the store (its top folder, or any folder inside it)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"the store it is a clone of (a store name or url); needed only when its origin matches several configured stores"`
	Default string `json:"default,omitempty" flag:"default" desc:"go back to lore's own clone of this store (a store name or url); your clone is left as it is"`
}

// UseOut is the output of store.use.
type UseOut struct {
	// Store names the store (its configured name, else its url).
	Store string `json:"store"`
	// URL is the store's url (no credentials).
	URL string `json:"url"`
	// Branch is the store branch the clone is for ("" = the default branch).
	Branch string `json:"branch,omitempty"`
	// Path is the clone lore uses for the store from now on.
	Path string `json:"path"`
	// Own is true when Path is the person's own clone.
	Own bool `json:"own"`
	// Previous is the clone lore used before.
	Previous string `json:"previous,omitempty"`
	// Changed is false when lore already used Path.
	Changed bool `json:"changed"`
	// Unused is a clone lore made earlier and no longer uses; it is left
	// on disk (remove it once you no longer need it).
	Unused string `json:"unused,omitempty"`
}

// CloneUser is the optional Manager upgrade behind store.use.
type CloneUser interface {
	UseClone(ctx context.Context, in UseIn) (UseOut, error)
}

// Text is the result and what it means.
func (o UseOut) Text() string {
	var b strings.Builder
	switch {
	case !o.Changed:
		fmt.Fprintf(&b, "lore already uses %s for store %s; nothing changed\n", o.Path, o.Store)
	case o.Own:
		fmt.Fprintf(&b, "✓ store %s now uses your clone %s (recorded on this machine only)\n", o.Store, o.Path)
	default:
		fmt.Fprintf(&b, "✓ store %s uses lore's own clone again: %s (the next command that needs it clones it if it is missing)\n", o.Store, o.Path)
	}
	if o.Changed && o.Previous != "" && o.Previous != o.Path {
		fmt.Fprintf(&b, "  before: %s", o.Previous)
		if o.Own {
			b.WriteString("\n")
		} else {
			b.WriteString(" (your clone, left as it is)\n")
		}
	}
	if o.Unused != "" {
		fmt.Fprintf(&b, "  lore's earlier clone %s is no longer used and stays on disk; remove it when you no longer need it\n", o.Unused)
	}
	return b.String()
}

// useErrors are the codes of store.use.
var useErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.BadPath, errs.NotAStore, errs.Refused, errs.Internal}

// useOp is store.use bound to m.
func useOp(m Manager) op.Entry {
	const verb = "store use"
	return op.Define(op.Spec{
		Name: OpUse, Summary: "Use your own git clone of a store (cloned anywhere) instead of lore's clone under ~/.lore/stores; --default goes back",
		Permission: op.PermWrite, Access: op.AccessRead, Errors: useErrors,
		Examples: []op.Example{
			{Title: "use a clone you made yourself", CLI: "git clone git@github.com:acme/notes ~/work/notes && lore store use ~/work/notes", Want: "now uses your clone"},
			{Title: "name the store when its url is configured twice", CLI: "lore store use ~/work/notes-staging --store notes_staging", Want: "now uses your clone"},
			{Title: "go back to lore's own clone", CLI: "lore store use --default notes", Want: "uses lore's own clone again"},
		},
	}, func(ctx context.Context, in UseIn) (UseOut, error) {
		in.Path, in.Store, in.Default = strings.TrimSpace(in.Path), strings.TrimSpace(in.Store), strings.TrimSpace(in.Default)
		switch {
		case in.Default != "" && (in.Path != "" || in.Store != ""):
			return UseOut{}, errs.New(errs.InvalidInput, verb+": --default takes the store alone; give either <path> or --default <store>").
				WithHint("lore store use <path> [--store <store>], or lore store use --default <store>")
		case in.Default == "" && in.Path == "":
			return UseOut{}, errs.New(errs.InvalidInput, verb+": name your clone of the store; usage: lore store use <path> [--store <store>]").
				WithHint("clone the store first (git clone <store url> <folder>), then lore store use <folder>; lore store use --default <store> goes back to lore's own clone")
		}
		u, ok := upgrade.As[CloneUser](m)
		if !ok {
			return UseOut{}, errs.New(errs.Internal, verb+": this build cannot change which clone a store uses")
		}
		return u.UseClone(ctx, in)
	})
}
