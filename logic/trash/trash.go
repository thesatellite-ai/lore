// Package trash holds the pure rules of the soft delete (DELETION_DESIGN
// 3.1-3.5): the delete fields every synced row may carry, marking a row as
// in the trash, clearing those fields on restore, and the retention check.
//
// Flow:
//
//	engine.Delete ──► Mark(row, Stamp{at, by, reason, batch}) ──► backend
//	engine.Restore ─► Clear(row) ─────────────────────────────► backend
//	every engine read ─► Is(row)? → hidden from list/show/cache (3.2)
//	trash purge --expired ─► Expired(info, now, retention_days) (DQ3, DQ7)
//
// D1: the delete fields live on the row itself (no batch table): the batch
// is exactly the set of rows whose deleted_batch holds its id.
package trash

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// The delete fields (DELETION_DESIGN 3.1). Names are part of the file
// format: never renamed.
const (
	FieldDeletedAt       = "deleted_at"
	FieldDeletedBy       = "deleted_by"
	FieldDeletedReason   = "deleted_reason"
	FieldDeletedBatch    = "deleted_batch"
	FieldDeleteOverruled = "delete_overruled"
)

// Fields lists every delete field, in the order Clear removes them.
func Fields() []string {
	return []string{FieldDeletedAt, FieldDeletedBy, FieldDeletedReason, FieldDeletedBatch, FieldDeleteOverruled}
}

// MaxReason bounds deleted_reason (3.1: at most 200 characters, one line).
const MaxReason = 200

// dayHours converts retention days to a duration.
const dayHours = 24

// Stamp is what one delete writes onto a row.
type Stamp struct {
	At     time.Time
	By     ids.ID[ids.Actor]
	Reason string
	Batch  ids.ID[ids.Batch]
}

// Info is the delete fields read back from a trashed row.
type Info struct {
	At     time.Time
	By     string // an act_ id as stored ("" when absent)
	Reason string
	Batch  string // a bat_ id as stored
}

// Reason validates a --reason value: trimmed, one line, at most MaxReason
// characters ("" = no reason).
func Reason(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, "\r\n") {
		return "", errs.New(errs.InvalidInput, "trash: --reason must be one line")
	}
	if utf8.RuneCountInString(s) > MaxReason {
		return "", errs.New(errs.InvalidInput, "trash: --reason is longer than 200 characters")
	}
	return s, nil
}

// Is reports whether r is a live row file that sits in the trash.
func Is(r row.Row) bool {
	if r.Deleted {
		return false
	}
	v, ok := r.Fields[FieldDeletedAt]
	return ok && v != nil && v.Kind() != value.KindNull && v.String() != ""
}

// day prints a delete date in InTrashErr.
const day = "2006-01-02 15:04"

// InTrashErr is E_IN_TRASH for the trashed row r (DELETION 3.2: "is in
// the trash (deleted by mira on …)" with `lore trash show <id>; bring it
// back with lore trash restore <id>`). It is the one wording: the engine
// answers it for a show, edit or delete of a trashed row, and the roster
// seam (profilestore.Roster.InTrash) for a trashed profile row, so a
// trashed contact reads exactly like a trashed task.
func InTrashErr(r row.Row) error {
	msg := "" + r.ID.String() + " is in the trash"
	if in, ok := Read(r); ok {
		var parts []string
		if in.By != "" {
			parts = append(parts, "deleted by "+in.By)
		}
		if !in.At.IsZero() {
			parts = append(parts, "on "+in.At.Format(day))
		}
		if len(parts) > 0 {
			msg += " (" + strings.Join(parts, " ") + ")"
		}
	}
	return errs.New(errs.InTrash, msg).WithHint("lore trash show --id " + r.ID.String() + "; bring it back with lore trash restore --target " + r.ID.String())
}

// Read returns r's delete fields (ok false when r is not in the trash).
func Read(r row.Row) (Info, bool) {
	if !Is(r) {
		return Info{}, false
	}
	in := Info{By: text(r, FieldDeletedBy), Reason: text(r, FieldDeletedReason), Batch: text(r, FieldDeletedBatch)}
	if t, err := time.Parse(time.RFC3339Nano, text(r, FieldDeletedAt)); err == nil {
		in.At = t.UTC()
	}
	return in, true
}

// Mark returns r with the delete fields of s set (a previous
// delete_overruled is cleared: deleting again settles it, 3.7).
func Mark(r row.Row, s Stamp) (row.Row, error) {
	f := make(map[string]value.Value, len(r.Fields)+len(Fields()))
	for k, v := range r.Fields {
		f[k] = v
	}
	delete(f, FieldDeleteOverruled)
	delete(f, FieldDeletedReason)
	f[FieldDeletedAt] = value.DateTimeFrom(s.At)
	if !s.By.IsZero() {
		by, err := value.RefOf(s.By.Any())
		if err != nil {
			return row.Row{}, err
		}
		f[FieldDeletedBy] = by
	}
	if s.Reason != "" {
		t, err := value.TextOf(s.Reason)
		if err != nil {
			return row.Row{}, err
		}
		f[FieldDeletedReason] = t
	}
	b, err := value.RefOf(s.Batch.Any())
	if err != nil {
		return row.Row{}, err
	}
	f[FieldDeletedBatch] = b
	r.Fields = f
	return r, nil
}

// Clear returns r without any delete field (3.1 restore).
func Clear(r row.Row) row.Row {
	f := make(map[string]value.Value, len(r.Fields))
	for k, v := range r.Fields {
		f[k] = v
	}
	for _, k := range Fields() {
		delete(f, k)
	}
	r.Fields = f
	return r
}

// Expired reports whether a row deleted at `at` is past a retention of
// days (DQ7: a maximum; days <= 0 = keep forever, never expired).
func Expired(at, now time.Time, days int64) bool {
	if days <= 0 || at.IsZero() {
		return false
	}
	return now.Sub(at) > time.Duration(days)*dayHours*time.Hour
}

// text reads a field as text ("" when absent).
func text(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}
