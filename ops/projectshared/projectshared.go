// Package projectshared defines the retired v0.1.17 shared-database pair
// `lore project shared-init|shared-list` (layer L5, CLI_REFERENCE 737,
// Q-MODEB, D17): both always refuse with E_UNSUPPORTED naming the stores
// commands that replace a shared lore.db.
//
//	project.shared_init|shared_list ─▶ E_UNSUPPORTED + replacement hint
//
// They stay operations (PASS3 ONE-PATH) so an old invocation, from the CLI
// or `lore api`, gets the refusal with the new command instead of "unknown
// command". shared-list refuses too rather than listing: a list from a file
// 0.1.18 no longer reads would suggest the mode still works. They touch
// nothing, so they are Local (no repo, no store opened).
package projectshared

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (CLI `lore project shared-init|shared-list`).
const (
	OpSharedInit = "project.shared_init"
	OpSharedList = "project.shared_list"
)

// Hint is the refusal's hint: the stores commands that replace a shared
// database (v0.1.17 saas/cmd/cli/project_shared.go modeBReplacement).
const Hint = "the shared-database mode is retired; use a store instead: " +
	"`lore store init <name> file:///abs/path` once, then `lore store add <name> <url>` in each repo " +
	"(write to it with `--to <name>`); an existing shared project converts with lore fix"

// In is the input of both: the v0.1.17 flags and arguments are still
// accepted so an old invocation gets the refusal, not "unknown flag".
type In struct {
	Args []string `json:"args,omitempty" arg:"0" rest:"true" desc:"ignored (retired)"`
	DB   string   `json:"db,omitempty" flag:"db" desc:"ignored (retired)"`
	Name string   `json:"name,omitempty" flag:"name" desc:"ignored (retired)"`
}

// Out is never returned with a value: both operations always refuse.
type Out struct{}

// Ops returns both retired operations.
func Ops() []op.Entry {
	// The example of a retired verb is its replacement: running the verb
	// itself only prints the refusal.
	spec := func(name, summary string, ex op.Example) op.Spec {
		return op.Spec{Name: name, Summary: summary, Permission: op.PermRead, Access: op.AccessRead, Local: true,
			Errors: []errs.Code{errs.Unsupported}, Examples: []op.Example{ex}}
	}
	return []op.Entry{
		op.Define(spec(OpSharedInit, "Retired (0.1.18): use `lore store init` + `lore store add`",
			op.Example{Title: "instead: a store the team shares", CLI: "lore store init notes file:///srv/lore/notes", Want: "notes"}),
			func(context.Context, In) (Out, error) { return Out{}, Refusal("shared-init") }),
		op.Define(spec(OpSharedList, "Retired (0.1.18): use `lore store list`",
			op.Example{Title: "instead: the stores this repo reads", CLI: "lore store list", Want: "store"}),
			func(context.Context, In) (Out, error) { return Out{}, Refusal("shared-list") }),
	}
}

// Refusal is the error of `lore project <verb>` (verb in CLI spelling).
func Refusal(verb string) error {
	return errs.New(errs.Unsupported, "lore project "+verb+" was removed in 0.1.18").WithHint(Hint)
}
