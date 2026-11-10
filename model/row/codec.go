package row

import (
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Encode renders r as its canonical document (the bytes of a row file).
// It refuses a row that breaks an envelope invariant rather than writing
// something a reader would reject.
func Encode(r Row) ([]byte, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	ms := []canon.Member{
		{Key: KeyVersion, Raw: []byte(strconv.Itoa(writeVersion(r)))},
		{Key: KeyTable, Raw: canon.String(string(r.Table))},
		{Key: KeyID, Raw: canon.String(r.ID.String())},
		{Key: KeyHLC, Raw: canon.String(r.HLC.String())},
	}
	if r.Deleted {
		ms = append(ms, canon.Member{Key: KeyDeleted, Raw: []byte("true")})
	}
	ms = appendTime(ms, KeyCreatedAt, r.CreatedAt)
	ms = appendActor(ms, KeyCreatedBy, r.CreatedBy)
	ms = appendTime(ms, KeyUpdatedAt, r.UpdatedAt)
	ms = appendActor(ms, KeyUpdatedBy, r.UpdatedBy)
	if !r.Origin.IsZero() {
		ms = append(ms, canon.Member{Key: KeyOrigin, Raw: r.Origin.Raw()})
	}
	if len(r.Meta) > 0 {
		meta := make([]canon.Member, 0, len(r.Meta))
		for k, v := range r.Meta {
			meta = append(meta, canon.Member{Key: k.String(), Raw: v.Raw()})
		}
		obj, err := canon.Object(meta)
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "row: meta", err)
		}
		ms = append(ms, canon.Member{Key: KeyMeta, Raw: obj})
	}
	for _, group := range []map[string]value.Value{r.Fields, r.Reserved} {
		for k, v := range group {
			b, err := v.MarshalJSON()
			if err != nil {
				return nil, errs.Wrap(errs.InvalidInput, "row: field "+quote(k), err)
			}
			ms = append(ms, canon.Member{Key: k, Raw: b})
		}
	}
	doc, err := canon.Object(ms)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "row: encode", err)
	}
	return doc, nil
}

// check enforces the envelope invariants Encode relies on.
func (r Row) check() error {
	switch {
	case !r.Table.Valid():
		return bad("table name " + quote(string(r.Table)))
	case r.ID.IsZero():
		return bad("missing id")
	case r.HLC.IsZero():
		return bad("missing _hlc")
	case r.Deleted && (len(r.Fields) > 0 || len(r.Meta) > 0 || !r.Origin.IsZero()):
		return bad("a tombstone carries no fields, meta or origin")
	case !r.Origin.IsZero() && !r.Origin.IsObject():
		return bad("_o is not an object")
	}
	for k, v := range r.Meta {
		if k.IsZero() || !v.IsObject() {
			return bad("meta entries are objects keyed by mky_ id")
		}
	}
	for k, v := range r.Fields {
		if !ValidName(k) || ReservedName(k) {
			return bad("field name " + quote(k))
		}
		if v == nil {
			return bad("field " + quote(k) + " has no value")
		}
	}
	for k, v := range r.Reserved {
		if !strings.HasPrefix(k, "_") || ReservedName(k) || v == nil {
			return bad("reserved key " + quote(k))
		}
	}
	return nil
}

// Decode parses a row document. It refuses a "_v" newer than FormatVersion
// with E_OLD_LORE (writes must never round-trip data this binary cannot
// represent) and upcasts older versions in memory through the step
// registry (steps.go): v1 gets "_hlc" from updated_at, counter 0 and
// updated_by (spec 12).
func Decode(b []byte) (Row, error) {
	ms, parsed, err := canon.MembersInspected(b)
	if err != nil {
		return Row{}, errs.Wrap(errs.InvalidInput, "row: not a JSON object", err)
	}
	by := make(map[string][]byte, len(ms))
	for _, m := range ms {
		by[m.Key] = m.Raw
	}
	var r Row
	if r.SourceVersion, err = decodeVersion(by[KeyVersion]); err != nil {
		return Row{}, err
	}
	if err = r.decodeHeader(by); err != nil {
		return Row{}, err
	}
	if err = r.decodeAudit(by); err != nil {
		return Row{}, err
	}
	if err = r.decodeBody(ms, parsed); err != nil {
		return Row{}, err
	}
	if r, err = upcast(r, r.SourceVersion); err != nil {
		return Row{}, err
	}
	if err := r.check(); err != nil {
		return Row{}, err
	}
	return r, nil
}

// decodeVersion reads and range-checks "_v".
func decodeVersion(raw []byte) (int, error) {
	if raw == nil {
		return 0, bad("missing _v")
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < formatV1 {
		return 0, bad("bad _v " + quote(string(raw)))
	}
	if n > FormatVersion {
		return 0, errs.New(errs.OldLore, "row: written by a newer lore (_v "+strconv.Itoa(n)+", this lore reads up to "+strconv.Itoa(FormatVersion)+")").
			WithHint("upgrade lore: lore upgrade")
	}
	return n, nil
}

// decodeHeader reads _table, id, _hlc and _deleted.
func (r *Row) decodeHeader(by map[string][]byte) error {
	s, err := str(by, KeyTable, true)
	if err != nil {
		return err
	}
	if r.Table, err = ParseTable(s); err != nil {
		return err
	}
	if s, err = str(by, KeyID, true); err != nil {
		return err
	}
	if r.ID, err = ids.ParseAny(s); err != nil {
		return err
	}
	if s, err = str(by, KeyHLC, r.SourceVersion >= hlcVersion); err != nil {
		return err
	}
	if r.HLC, err = hlc.Parse(s); err != nil {
		return err
	}
	if raw, ok := by[KeyDeleted]; ok {
		if string(raw) != "true" {
			return bad("_deleted must be true when present")
		}
		r.Deleted = true
	}
	return nil
}

// decodeAudit reads the four audit fields.
func (r *Row) decodeAudit(by map[string][]byte) error {
	var err error
	if r.CreatedAt, err = timeField(by, KeyCreatedAt); err != nil {
		return err
	}
	if r.UpdatedAt, err = timeField(by, KeyUpdatedAt); err != nil {
		return err
	}
	if r.CreatedBy, err = actorField(by, KeyCreatedBy); err != nil {
		return err
	}
	r.UpdatedBy, err = actorField(by, KeyUpdatedBy)
	return err
}

// decodeBody reads _o, meta, the table fields and unknown underscore keys;
// parsed[i] is ms[i]'s value from the same parse (canon.MembersInspected).
func (r *Row) decodeBody(ms []canon.Member, parsed []canon.Parsed) error {
	for i, m := range ms {
		switch {
		case m.Key == KeyOrigin:
			o, err := value.JSONOf(m.Raw)
			if err != nil || !o.IsObject() {
				return bad("_o is not an object")
			}
			r.Origin = o
		case m.Key == KeyMeta:
			if err := r.decodeMeta(m.Raw); err != nil {
				return err
			}
		case ReservedName(m.Key):
			// handled by decodeHeader / decodeAudit
		case strings.HasPrefix(m.Key, "_"):
			v, err := value.InferInspected(m.Raw, parsed[i])
			if err != nil {
				return err
			}
			if r.Reserved == nil {
				r.Reserved = map[string]value.Value{}
			}
			r.Reserved[m.Key] = v
		default:
			if !ValidName(m.Key) {
				return bad("field name " + quote(m.Key))
			}
			v, err := value.InferInspected(m.Raw, parsed[i])
			if err != nil {
				return err
			}
			if r.Fields == nil {
				r.Fields = map[string]value.Value{}
			}
			r.Fields[m.Key] = v
		}
	}
	return nil
}

// decodeMeta reads the "meta" object.
func (r *Row) decodeMeta(raw []byte) error {
	entries, err := canon.Members(raw)
	if err != nil {
		return bad("meta is not an object")
	}
	r.Meta = make(map[ids.ID[ids.MetaKey]]value.JSON, len(entries))
	for _, e := range entries {
		k, err := ids.Parse[ids.MetaKey](e.Key)
		if err != nil {
			return err
		}
		v, err := value.JSONOf(e.Raw)
		if err != nil || !v.IsObject() {
			return bad("meta entry " + quote(e.Key) + " is not an object")
		}
		r.Meta[k] = v
	}
	return nil
}

// str reads a JSON string member; absent is "" unless required.
func str(by map[string][]byte, key string, required bool) (string, error) {
	raw, ok := by[key]
	if !ok {
		if required {
			return "", bad("missing " + key)
		}
		return "", nil
	}
	v, err := value.Parse(value.KindText, raw)
	if err != nil {
		return "", bad(key + " is not a string")
	}
	t, ok := v.(value.Text)
	if !ok {
		return "", bad(key + " is not a string")
	}
	return t.String(), nil
}

// timeField reads an optional RFC 3339 time member, in UTC.
func timeField(by map[string][]byte, key string) (time.Time, error) {
	s, err := str(by, key, false)
	if err != nil || s == "" {
		return time.Time{}, err
	}
	t, err := value.DateTimeOf(s)
	if err != nil {
		return time.Time{}, bad(key + " is not an RFC 3339 time")
	}
	return t.Time(), nil
}

// actorField reads an optional actor id member.
func actorField(by map[string][]byte, key string) (ids.ID[ids.Actor], error) {
	s, err := str(by, key, false)
	if err != nil || s == "" {
		return ids.ID[ids.Actor]{}, err
	}
	return ids.Parse[ids.Actor](s)
}

// appendTime adds a time member when t is set.
func appendTime(ms []canon.Member, key string, t time.Time) []canon.Member {
	if t.IsZero() {
		return ms
	}
	return append(ms, canon.Member{Key: key, Raw: canon.String(value.DateTimeFrom(t).String())})
}

// appendActor adds an actor member when a is set.
func appendActor(ms []canon.Member, key string, a ids.ID[ids.Actor]) []canon.Member {
	if a.IsZero() {
		return ms
	}
	return append(ms, canon.Member{Key: key, Raw: canon.String(a.String())})
}

// bad builds a malformed-envelope error.
func bad(why string) error { return errs.New(errs.InvalidInput, "row: "+why) }

// v1Floor is the stamp given to a v1 row that carries neither updated_at nor
// created_at (some v0.1.17 tables, e.g. actors, never wrote them): the Unix
// epoch, the lowest stamp lore can issue, so any real edit of the row sorts
// after it and wins the merge.
var v1Floor = time.Unix(0, 0).UTC()

// upcastHLC is the v1 upcast rule (spec 12) with fallbacks: updated_at and
// updated_by first, then created_at and created_by, then v1Floor, so a v1
// row is always readable instead of failing with "missing _hlc".
func upcastHLC(r Row) hlc.HLC {
	switch {
	case !r.UpdatedAt.IsZero():
		return hlc.FromUpdate(r.UpdatedAt, r.UpdatedBy)
	case !r.CreatedAt.IsZero():
		return hlc.FromUpdate(r.CreatedAt, r.CreatedBy)
	default:
		return hlc.FromUpdate(v1Floor, r.UpdatedBy)
	}
}
