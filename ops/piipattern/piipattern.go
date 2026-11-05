// Package piipattern defines `lore pii-pattern list|add|enable|disable`
// (layer L5), ported from the v0.1.17 cobra commands (saas/cmd/cli
// entity_commands.go) onto the op registry (PASS3 ONE-PATH).
//
// Custom secret patterns are settings rows now (SETTINGS ARE DATA,
// FORMAT_EVOLUTION step 17), written by `lore secrets rule add|on|off`. The
// v0.1.17 pii_patterns table was machine-local in lore.db and no scanner
// ever read it, so:
//
//	pii-pattern list            ─► Env.LegacyPIIPatterns (what is left in
//	                               the checkout's v0.1.17 lore.db, read-only)
//	pii-pattern add|enable|disable ─► E_INVALID_INPUT naming the secrets
//	                               rule command that replaced it
//
// The operations are machine-wide (op.Spec.Local): they open no store.
package piipattern

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore pii-pattern …`).
const (
	Noun      = "pii_pattern"
	OpList    = "pii_pattern.list"
	OpAdd     = "pii_pattern.add"
	OpEnable  = "pii_pattern.enable"
	OpDisable = "pii_pattern.disable"
)

// Output words of the v0.1.17 list.
const (
	// listKind is the v0.1.17 --json envelope kind (constants.KindPiiPatternList).
	listKind      = "pii-pattern.list"
	schemaVersion = 1
	stateOn       = "on"
	stateOff      = "off"
	emptyList     = "(no patterns)\n"
)

// Verbs of the moved commands, and the secrets rule command that replaced
// each (v0.1.17 errPIIPatternMoved).
const (
	verbAdd     = "add"
	verbEnable  = "enable"
	verbDisable = "disable"
)

// replacement maps a moved verb to its new command.
var replacement = map[string]string{
	verbAdd:     "lore secrets rule add",
	verbEnable:  "lore secrets rule on",
	verbDisable: "lore secrets rule off",
}

// Pattern is one v0.1.17 pii_patterns row (the ent JSON shape).
type Pattern struct {
	ID         string `json:"id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Name       string `json:"name,omitempty"`
	Regex      string `json:"regex,omitempty"`
	Enabled    bool   `json:"enabled,omitempty"`
	SourceKind string `json:"source_kind,omitempty"`
}

// Env is what the list reads; *wire.App implements it.
type Env interface {
	// LegacyPIIPatterns returns the rows of the v0.1.17 pii_patterns table
	// of this checkout's lore.db, by name; none when there is no lore.db or
	// no such table.
	LegacyPIIPatterns(ctx context.Context) ([]Pattern, error)
}

// ListIn is the (empty) input of pii_pattern.list.
type ListIn struct{}

// ListOut is the v0.1.17 --json envelope of the list.
type ListOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count"`
	Data          []Pattern `json:"data"`
}

// Text is the v0.1.17 human list: `[on|off] name  (source_kind)`.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return emptyList
	}
	var b strings.Builder
	for _, p := range o.Data {
		state := stateOff
		if p.Enabled {
			state = stateOn
		}
		b.WriteString("[" + state + "] " + p.Name + "  (" + p.SourceKind + ")\n")
	}
	return b.String()
}

// AddIn keeps the v0.1.17 flags of the moved `add`, so a script passing
// them gets the pointer to the new command, not a flag error.
type AddIn struct {
	Name   string `json:"name,omitempty" flag:"name" desc:"pattern name"`
	Regex  string `json:"regex,omitempty" flag:"regex" desc:"regex pattern"`
	Source string `json:"source,omitempty" flag:"source" desc:"source_kind"`
}

// ToggleIn is the input of the moved enable and disable.
type ToggleIn struct {
	ID string `json:"id,omitempty" arg:"0" desc:"pattern id"`
}

// MovedOut is never returned: the moved verbs always refuse.
type MovedOut struct{}

// Moved is the refusal of a moved verb (v0.1.17 errPIIPatternMoved).
func Moved(verb string) error {
	repl := replacement[verb]
	return errs.New(errs.InvalidInput, "lore pii-pattern "+verb+" was replaced by "+repl+" (secret rules are settings rows)").
		WithHint("use `" + repl + "`; `lore secrets rules` lists every rule")
}

// Ops returns the pii-pattern operations bound to env.
func Ops(env Env) []op.Entry {
	moved := func(verb string) func(context.Context, ToggleIn) (MovedOut, error) {
		return func(context.Context, ToggleIn) (MovedOut, error) { return MovedOut{}, Moved(verb) }
	}
	refused := []errs.Code{errs.InvalidInput}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpList, Summary: "List PII patterns", Local: true,
			Permission: op.PermRead, Access: op.AccessRead, Errors: []errs.Code{errs.Internal},
		}, func(ctx context.Context, _ ListIn) (ListOut, error) {
			got, err := env.LegacyPIIPatterns(ctx)
			if err != nil {
				return ListOut{}, err
			}
			if got == nil {
				got = []Pattern{}
			}
			return ListOut{SchemaVersion: schemaVersion, Kind: listKind, Count: len(got), Data: got}, nil
		}),
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Replaced by `lore secrets rule add`", Local: true,
			Permission: op.PermRead, Access: op.AccessRead, Errors: refused,
		}, func(context.Context, AddIn) (MovedOut, error) { return MovedOut{}, Moved(verbAdd) }),
		op.Define(op.Spec{
			Name: OpEnable, Summary: "Replaced by `lore secrets rule on`", Local: true,
			Permission: op.PermRead, Access: op.AccessRead, Errors: refused,
		}, moved(verbEnable)),
		op.Define(op.Spec{
			Name: OpDisable, Summary: "Replaced by `lore secrets rule off`", Local: true,
			Permission: op.PermRead, Access: op.AccessRead, Errors: refused,
		}, moved(verbDisable)),
	}
}
