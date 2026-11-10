// Package teamprofile holds the pure rules of team profiles
// (TEAM_PROFILES_DESIGN, TP-1..TP-23) shared by every profile op
// (ops/profile, ops/period, ops/member), the migrate step, the TUI view and
// identity matching:
//
//	StatusOf          actor fields ──▶ active | on_leave | left | archived (2.7)
//	Normalize         (kind, value) ──▶ value_norm, or E_CONTACT_TYPE (6.1)
//	BuiltinContactTypes, BuiltinRoles   the compiled-in lists rows overlay (TS-2)
//	PeriodsOf         actor + period rows ──▶ periods, with the synthesised one (8)
//	CheckPeriods      the period invariants, or E_PERIOD_OVERLAP (2.5)
//
// No I/O, no clock: callers pass "today" and the rows they read.
package teamprofile

import (
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// activeFalse is the text form of an explicit active false (a Bool, or a
// row file's plain JSON inferred as text, reads the same).
const activeFalse = "false"

// StatusOf reads an actor's status from its fields (TEAM_PROFILES 2.7):
// status when set; else left when active is explicitly false (a v0.1.17
// `lore actor deactivate`); else active. status is the one source of truth,
// active/left_at only answer for rows written before it existed.
func StatusOf(fields map[string]value.Value) string {
	if s := text(fields, schema.FieldActorStatus); s != "" {
		return s
	}
	if text(fields, schema.FieldActorActive) == activeFalse {
		return schema.ActorStatusLeft
	}
	return schema.ActorStatusActive
}

// Pickable reports whether a person with status s may be picked or
// assigned (TP-15: left and archived are out of pickers and --assign).
func Pickable(s string) bool {
	return s == schema.ActorStatusActive || s == schema.ActorStatusOnLeave
}

// ContactKinds lists the contact kinds (schema.ContactKinds), each with its
// own normalisation (Normalize).
func ContactKinds() []string { return schema.ContactKinds() }

// PeriodTypes lists the company period types (schema.PeriodTypes, TP-14).
func PeriodTypes() []string { return schema.PeriodTypes() }

// text is the text of field name, "" when absent or null.
func text(fields map[string]value.Value, name string) string {
	v, ok := fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}
