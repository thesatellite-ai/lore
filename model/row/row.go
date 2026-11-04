// Package row is the envelope every stored row has, whatever its table
// (STORE_SERVER_SPEC 4.1): format version, table, id, hlc, audit fields,
// origin, tombstone flag, metadata and the table's own fields. The same
// canonical JSON is a git row file, a folder row file and a REST body, so
// moving a row between backends never converts anything.
//
// Why it lives in model: backends only see the envelope, never a table's
// shape, so they never migrate data; the shape checks live above them
// (schema + logic/validate).
//
// Invariant: Encode is deterministic (sorted keys, the canon document form)
// and Decode followed by Encode of a v2 document reproduces its bytes
// exactly; a v1 document is upcast in memory and re-encodes as v2.
package row

import (
	"regexp"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// FormatVersion is the "_v" this code writes. v0.1.17 wrote 1. A reader
// refuses a higher number (E_OLD_LORE) and upcasts lower ones in memory.
const FormatVersion = 2

// formatV1 is the v0.1.17 row format, the only older one.
const formatV1 = 1

// Envelope keys. Table fields are snake_case without a leading underscore,
// so they can never collide with the underscore keys; the audit keys and
// id/meta are reserved by name (ReservedName).
const (
	KeyVersion   = "_v"
	KeyTable     = "_table"
	KeyID        = "id"
	KeyHLC       = "_hlc"
	KeyDeleted   = "_deleted"
	KeyOrigin    = "_o"
	KeyMeta      = "meta"
	KeyCreatedAt = "created_at"
	KeyCreatedBy = "created_by"
	KeyUpdatedAt = "updated_at"
	KeyUpdatedBy = "updated_by"
)

// envelopeKeys are the keys Row models as struct fields.
var envelopeKeys = map[string]struct{}{
	KeyVersion: {}, KeyTable: {}, KeyID: {}, KeyHLC: {}, KeyDeleted: {}, KeyOrigin: {},
	KeyMeta: {}, KeyCreatedAt: {}, KeyCreatedBy: {}, KeyUpdatedAt: {}, KeyUpdatedBy: {},
}

// ReservedName reports whether name is an envelope key, so no table may
// declare a field of that name.
func ReservedName(name string) bool {
	_, ok := envelopeKeys[name]
	return ok
}

// maxNameLen bounds table and field names (SQLite identifiers stay short
// and readable; the cache prefixes kind tables with "res_").
const maxNameLen = 63

// namePattern is snake_case ASCII starting with a letter.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidName reports whether s is a valid table or field name.
func ValidName(s string) bool { return len(s) <= maxNameLen && namePattern.MatchString(s) }

// Table is a table name ("memories", "kind_fields", "res_server").
type Table string

// ParseTable validates a table name.
func ParseTable(s string) (Table, error) {
	if !ValidName(s) {
		return "", errs.New(errs.InvalidInput, "row: "+quote(s)+" is not a snake_case table name")
	}
	return Table(s), nil
}

// Valid reports whether t is a well-formed table name.
func (t Table) Valid() bool { return ValidName(string(t)) }

// String returns the name.
func (t Table) String() string { return string(t) }

// Row is one decoded envelope.
type Row struct {
	Table Table
	ID    ids.AnyID
	// HLC is the stamp of the last edit; required on every stored row.
	HLC hlc.HLC
	// Audit fields; zero means absent (a tombstone has no created_*; a
	// v0.1.17 row may lack the actors).
	CreatedAt time.Time
	CreatedBy ids.ID[ids.Actor]
	UpdatedAt time.Time
	UpdatedBy ids.ID[ids.Actor]
	// Origin is "_o" (PROVENANCE_DESIGN): a JSON object written once at
	// create, extended by `lore move`. Its inner shape belongs to the
	// provenance logic, so the envelope keeps it as validated canonical JSON.
	// Zero means absent.
	Origin value.JSON
	// Deleted marks a tombstone: kept forever so an old copy can never bring
	// the row back. A tombstone has no Fields, Meta or Origin.
	Deleted bool
	// Meta is "meta": metadata values keyed by meta key id. Each value is the
	// JSON object of METADATA_DESIGN ({name, scope, v, and optional strict,
	// v_name, …}), kept as canonical JSON for the metadata logic to read.
	Meta map[ids.ID[ids.MetaKey]]value.JSON
	// Fields are the table's own columns. Decode infers their kinds from
	// plain JSON (value.Infer); a schema-aware pass re-types them.
	Fields map[string]value.Value
	// Reserved holds underscore keys this version does not model (a future
	// "_w" for instance), kept so decode → encode loses nothing.
	Reserved map[string]value.Value
	// SourceVersion is the "_v" the decoded bytes had (0 for a Row built in
	// code). Encode always writes FormatVersion; this only tells the caller
	// that a v1 row was upcast and should be rewritten.
	SourceVersion int
}

// maxQuoted bounds how much of a bad name an error repeats.
const maxQuoted = 48

// quote renders a name for an error message, truncated.
func quote(s string) string {
	r := []rune(s)
	if len(r) > maxQuoted {
		r = r[:maxQuoted]
	}
	return strconv.Quote(string(r))
}
