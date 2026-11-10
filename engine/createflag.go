package engine

// CreateFlag is the shared --create flag (PENDING-60, PL-CREATE-1): an add
// input embeds it (no json tag) so `--to <store>/<folder>` may create a
// folder the store does not have yet. The Runner's target step (wire,
// prepareTarget) reads it from any input by its json name, InputCreate;
// the operation itself never reads it. Without it (or --yes, the answer
// to the folder question) a missing folder is refused naming the folders
// the store has, so a typo never makes a new store project (Q-stores-2).
type CreateFlag struct {
	Create bool `json:"create,omitempty" flag:"create" desc:"with --to <store>/<folder>: create the folder when the store does not have it yet"`
}

// InputCreate is the json name of CreateFlag's field.
const InputCreate = "create"
