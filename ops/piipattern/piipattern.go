// Package piipattern defines `lore pii-pattern list|add|enable|disable`
// (layer L5), ported from the v0.1.17 cobra commands (saas/cmd/cli
// entity_commands.go) onto the op registry (PASS3 ONE-PATH).
//
// Custom secret patterns are settings rows now (SETTINGS ARE DATA,
// FORMAT_EVOLUTION step 17), written by `lore secrets rule add|on|off`. The
// v0.1.17 pii_patterns table was machine-local in lore.db and no scanner
// ever read it, so it is a machine-local core table (schema.MachineLocal)
// filled only by the machine_local copy of `lore migrate run` (P3-PII-1):
//
//	pii-pattern list            ─► Env.List(machine store, pii_patterns),
//	                               by name (read-only leftovers)
//	pii-pattern add|enable|disable ─► E_INVALID_INPUT naming the secrets
//	                               rule command that replaced it
//
// The moved verbs are Local (they refuse before opening anything); list
// reads rows, so it runs on the opened engine like every row read.
package piipattern

import (
	"context"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// Table is the machine-local table the list reads; it must equal
// schema.TablePIIPatterns (the built-in definition, P3-PII-1), and its
// field names are the v0.1.17 columns.
const Table row.Table = "pii_patterns"

// Fields of Table (schema.FieldPII*).
const (
	fieldName       = "name"
	fieldRegex      = "regex"
	fieldEnabled    = "enabled"
	fieldSourceKind = "source_kind"
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
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// Env is what the list reads; *wire.App implements it (engine.Engine's
// List: the engine routes a machine-local table to the machine store).
type Env interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// patternOf maps one pii_patterns row to the v0.1.17 JSON shape. created_at
// and updated_at are the envelope's: the migrate copy stamps them at copy
// time (wire/machinecopy.go, lossy on purpose).
func patternOf(r row.Row) Pattern {
	text := func(k string) string {
		if v, ok := r.Fields[k]; ok {
			return v.String()
		}
		return ""
	}
	p := Pattern{ID: r.ID.String(), Name: text(fieldName), Regex: text(fieldRegex), SourceKind: text(fieldSourceKind), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy}
	if b, ok := r.Fields[fieldEnabled].(value.Bool); ok {
		p.Enabled = b.Bool()
	}
	if !r.CreatedAt.IsZero() {
		p.CreatedAt = r.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !r.UpdatedAt.IsZero() {
		p.UpdatedAt = r.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return p
}

// ListIn is the (empty) input of pii_pattern.list.
type ListIn struct {
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
}

// ListOut is the v0.1.17 --json envelope of the list.
type ListOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count,omitempty"`
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
	// Positional is the name typed v0.1.17-style without --name (`lore
	// pii-pattern add invoice --regex x`): taken so that form also gets
	// the replacement refusal, not "too many arguments" (INTEG-QUESTIONS-10).
	Positional string `json:"positional,omitempty" arg:"0" desc:"pattern name (moved: see lore secrets rule add)"`
	Name       string `json:"name,omitempty" flag:"name" desc:"pattern name"`
	Regex      string `json:"regex,omitempty" flag:"regex" desc:"regex pattern"`
	Source     string `json:"source,omitempty" flag:"source" desc:"source_kind"`
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
			Name: OpList, Summary: "List PII patterns",
			Examples:   []op.Example{{Title: "the PII patterns", CLI: "lore pii-pattern list", Want: ""}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: []errs.Code{errs.Internal},
		}, func(ctx context.Context, _ ListIn) (ListOut, error) {
			rows, err := env.List(ctx, store.SourceMachine, Table)
			if err != nil {
				return ListOut{}, err
			}
			got := make([]Pattern, 0, len(rows))
			for _, r := range rows {
				got = append(got, patternOf(r))
			}
			// v0.1.17 ordered by name; id breaks ties so the order is stable
			slices.SortFunc(got, func(a, b Pattern) int {
				if c := strings.Compare(a.Name, b.Name); c != 0 {
					return c
				}
				return strings.Compare(a.ID, b.ID)
			})
			return ListOut{SchemaVersion: schemaVersion, Kind: listKind, Count: len(got), Data: got}, nil
		}),
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Replaced by `lore secrets rule add`", Local: true,
			Examples:   []op.Example{{Title: "the command that replaced it", CLI: "lore secrets rule add --help", Want: ""}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: refused,
		}, func(context.Context, AddIn) (MovedOut, error) { return MovedOut{}, Moved(verbAdd) }),
		op.Define(op.Spec{
			Name: OpEnable, Summary: "Replaced by `lore secrets rule on`", Local: true,
			Examples:   []op.Example{{Title: "the command that replaced it", CLI: "lore secrets rule on --help", Want: ""}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: refused,
		}, moved(verbEnable)),
		op.Define(op.Spec{
			Name: OpDisable, Summary: "Replaced by `lore secrets rule off`", Local: true,
			Examples:   []op.Example{{Title: "the command that replaced it", CLI: "lore secrets rule off --help", Want: ""}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: refused,
		}, moved(verbDisable)),
	}
}
