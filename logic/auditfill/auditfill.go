package auditfill

import (
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// unknownActorText is the reserved actor id `unknown` (FORMAT_EVOLUTION
// section 3, audit rule): a fixed, valid act_ id (UUID version 7 with a
// zero clock) that no real actor can get, since real ids carry the time
// they were made. It is never guessed from git blame or the caller.
const unknownActorText = "act_00000000000070008000000000000000"

// UnknownName is how the reserved actor shows ("unknown").
const UnknownName = "unknown"

// Field names of the backfill (FORMAT_EVOLUTION section 3 table).
const (
	// FieldCreatorActor is the existing creator column some tables have.
	FieldCreatorActor = "created_by_actor_id"
	// FieldDeletedAt / FieldDeletedBy are the soft-delete audit fields.
	FieldDeletedAt = "deleted_at"
	FieldDeletedBy = "deleted_by"
)

// UnknownActor returns the reserved `unknown` actor id.
func UnknownActor() ids.ID[ids.Actor] {
	id, err := ids.Parse[ids.Actor](unknownActorText)
	if err != nil {
		panic("auditfill: reserved unknown actor id is invalid: " + err.Error()) // programmer error, pinned by TestUnknownActor
	}
	return id
}

// IsUnknown reports whether id is the reserved `unknown` actor.
func IsUnknown(id ids.ID[ids.Actor]) bool { return id.String() == unknownActorText }

// Defaults fills the missing audit actors of r (auditDefaults of
// FORMAT_EVOLUTION section 3) and reports whether it changed anything. A
// tombstone has no created_* and is returned unchanged. r.Fields is
// copied before a deleted_by is added, so the caller's map is not touched.
func Defaults(r row.Row) (row.Row, bool) {
	if r.Deleted {
		return r, false
	}
	changed := false
	if r.CreatedBy.IsZero() {
		r.CreatedBy = creatorOf(r.Fields)
		changed = true
	}
	if r.UpdatedBy.IsZero() {
		r.UpdatedBy = UnknownActor()
		if !r.CreatedAt.IsZero() && r.UpdatedAt.Equal(r.CreatedAt) {
			r.UpdatedBy = r.CreatedBy // never edited: the creator is the last writer
		}
		changed = true
	}
	if needsDeletedBy(r.Fields) {
		fields := make(map[string]value.Value, len(r.Fields)+1)
		for k, v := range r.Fields {
			fields[k] = v
		}
		unknown, err := value.TextOf(unknownActorText)
		if err != nil {
			panic("auditfill: reserved unknown actor id is not text: " + err.Error()) // ASCII constant
		}
		fields[FieldDeletedBy] = unknown
		r.Fields = fields
		changed = true
	}
	return r, changed
}

// creatorOf is the table's own creator column when it holds an actor id,
// else UnknownActor.
func creatorOf(fields map[string]value.Value) ids.ID[ids.Actor] {
	if v, ok := fields[FieldCreatorActor]; ok && v != nil {
		if id, err := ids.Parse[ids.Actor](v.String()); err == nil {
			return id
		}
	}
	return UnknownActor()
}

// needsDeletedBy: deleted_at is set and deleted_by is missing or empty.
func needsDeletedBy(fields map[string]value.Value) bool {
	at, ok := fields[FieldDeletedAt]
	if !ok || at == nil || at.String() == "" {
		return false
	}
	by, ok := fields[FieldDeletedBy]
	return !ok || by == nil || by.String() == ""
}

// ParseActor reads a writer filter value (INTEG-AUDIT-1: --created-by /
// --updated-by on every list): "" is no filter (the zero id), "unknown" is
// the reserved actor old rows are backfilled with, anything else must be
// an act_ id (E_INVALID_ID naming flag otherwise). The one parser both the
// engine's list filter and ops/internal/metalist use.
func ParseActor(flag, raw string) (ids.ID[ids.Actor], error) {
	switch raw {
	case "":
		return ids.ID[ids.Actor]{}, nil
	case UnknownName:
		return UnknownActor(), nil
	}
	id, err := ids.Parse[ids.Actor](raw)
	if err != nil {
		return id, errs.New(errs.InvalidID, flag+" "+raw+" is not an actor id (act_…) or unknown").WithHint("lore actor list shows the ids")
	}
	return id, nil
}
