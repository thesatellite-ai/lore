package op

import "errors"

// ActorRef is the Go type of an input field whose value names an actor:
// an @handle, a stable key (human:mira) or an act_ id. The operation
// resolves it; the type only tells the registry (and so every surface)
// that the field is an actor.
type ActorRef string

// Flag and command words with one fixed meaning on every command.
const (
	// byFlag always means an actor (XQ8); grouping is --group-by.
	byFlag = "by"
	// groupByFlag is the grouping flag XQ8 points --by users to.
	groupByFlag = "group-by"
)

// rootVerbsRefused are first CLI words no operation may have (XQ9:
// importing and exporting are verbs under the noun they create, `lore task
// import`, `lore task export --format`; there is no `lore import` or `lore
// export`).
var rootVerbsRefused = []string{"import", "export"}

// checkWords applies the cross-command word rules to one entry; NewRegistry
// calls it, so a breaking operation fails registry construction (and every
// test that builds the real registry) instead of relying on review.
func checkWords(e Entry) []error {
	s := e.Spec()
	var p []error
	// XQ8: --by always means an actor, so a `by` flag must be an ActorRef.
	for _, f := range e.InputFields() {
		if f.Flag == byFlag && !f.Actor {
			p = append(p, errors.New(s.Name+": flag --"+byFlag+" must name an actor (Go type op.ActorRef, XQ8); for grouping use --"+groupByFlag))
		}
	}
	// XQ9: no root-level import/export command.
	if path := CLIPath(s.Name); len(path) > 0 {
		for _, w := range rootVerbsRefused {
			if path[0] == w {
				p = append(p, errors.New(s.Name+": no root-level `lore "+w+"` (XQ9): "+w+" is a verb under the noun it creates, e.g. task."+w))
			}
		}
	}
	return p
}
