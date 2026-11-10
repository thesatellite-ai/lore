// Package auditrec is the who-wrote-it pair every noun's show / list
// --json record embeds (INTEG-AUDIT-1): created_by and updated_by read from
// the row envelope (model/row), which the engine write path stamps from the
// calling actor. Records already carry created_at / updated_at from the
// same envelope; a record names the two actor keys (created_by, updated_by) as fields
// beside them, so every noun prints the four audit fields the same way.
// The envelope is the one source: no table column is copied (a table's own
// created_by_actor_id stays its own field).
package auditrec

import (
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/row"
)

// Writers is the record's created_by / updated_by (act_ ids). A row from
// before audit fields shows the reserved unknown actor (auditfill), never
// a guessed writer.
type Writers struct {
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// Of is r's writers; a tombstone (no created_*) gives the zero value.
func Of(r row.Row) Writers {
	if r.Deleted {
		return Writers{UpdatedBy: actorText(r.UpdatedBy.String(), r.UpdatedBy.IsZero())}
	}
	r, _ = auditfill.Defaults(r)
	return Writers{CreatedBy: r.CreatedBy.String(), UpdatedBy: r.UpdatedBy.String()}
}

// ByID is the writers of each row of rows by id, for a list whose
// records are built from a domain value that no longer carries its row.
func ByID(rows []row.Row) map[string]Writers {
	out := make(map[string]Writers, len(rows))
	for _, r := range rows {
		out[r.ID.String()] = Of(r)
	}
	return out
}

func actorText(s string, zero bool) string {
	if zero {
		return ""
	}
	return s
}

// Line is the one writers line every plain-text show prints
// (INTEG-AUDIT-1): "created <time> by <actor> · updated <time> by <actor>\n".
// A side with neither time nor actor is left out; "" when both are empty.
func Line(createdAt, createdBy, updatedAt, updatedBy string) string {
	createdAt, updatedAt = normal(createdAt), normal(updatedAt)
	var parts []string
	if p := side("created", createdAt, createdBy); p != "" {
		parts = append(parts, p)
	}
	if p := side("updated", updatedAt, updatedBy); p != "" {
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + "\n"
}

// LineAt is Line for records that keep the times as time.Time.
func LineAt(createdAt time.Time, createdBy string, updatedAt time.Time, updatedBy string) string {
	return Line(stamp(createdAt), createdBy, stamp(updatedAt), updatedBy)
}

// stamp is t in UTC to the second ("" when zero).
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// normal is an RFC 3339 time as stamp prints it (UTC, seconds), so every
// noun shows the same form whatever its record keeps; other text is kept.
func normal(at string) string {
	if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
		return stamp(t)
	}
	return at
}

func side(verb, at, by string) string {
	switch {
	case at == "" && by == "":
		return ""
	case by == "":
		return verb + " " + at
	case at == "":
		return verb + " by " + by
	}
	return verb + " " + at + " by " + by
}
