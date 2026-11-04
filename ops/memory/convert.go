package memory

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/store"
)

// listBodyMax is how much of a body a list or search row shows (v0.1.17
// cut list bodies at 80 characters).
const listBodyMax = 80

// ellipsis ends a cut body.
const ellipsis = "..."

// oneBody returns the memory text from --body or the positional argument;
// exactly one must be given, and it may not be blank (E_EMPTY_BODY).
func oneBody(flag, arg string) (string, error) {
	if flag != "" && arg != "" {
		return "", errs.New(errs.InvalidInput, "memory add: give the text once, as an argument or with --body, not both")
	}
	body := flag + arg
	if strings.TrimSpace(body) == "" {
		return "", errs.New(errs.EmptyBody, "memory add: the memory text is empty").
			WithHint(`lore memory add "<text>"`)
	}
	return body, nil
}

// orDefault returns s, or def when s is empty.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// texts turns the non-empty strings of m into text values (an empty string
// means "not given"), in a stable order so an error names the same field
// every run.
func texts(m map[string]string) (map[string]value.Value, error) {
	out := map[string]value.Value{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if m[k] == "" {
			continue
		}
		t, err := value.TextOf(m[k])
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "memory: field "+k, err)
		}
		out[k] = t
	}
	return out, nil
}

// memoryID parses a memory id argument (E_NOT_FOUND otherwise: ops/internal/lookup).
func memoryID(s string) (ids.ID[ids.Memory], error) {
	id, err := ids.Parse[ids.Memory](strings.TrimSpace(s))
	if err != nil {
		return ids.ID[ids.Memory]{}, lookup.BadID("memory: "+quoteID(s)+" is not a memory id (mem_…)", err)
	}
	return id, nil
}

// maxQuotedID bounds how much of a bad id an error repeats.
const maxQuotedID = 48

// quoteID renders a user-given id for an error, truncated.
func quoteID(s string) string {
	return `"` + textcut.Cut(s, maxQuotedID, maxQuotedID) + `"`
}

// field returns the text form of a row field ("" when absent or null).
func field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// recordOf is the v0.1.17 --json form of a memory row (Record). A v0.1.17
// row always had created_by_actor_id; a 0.1.18 row that does not set it
// shows the envelope's created_by, which names the same actor.
func recordOf(lab Labeler, src store.Source, r row.Row) Record {
	rec := Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TrustScore: numberOr(r, fieldTrust), Confidence: numberOr(r, fieldConfidence),
		LastValidatedAt: timePtr(r, fieldValidated), ArchivedAt: timePtr(r, fieldArchivedAt),
		SourceKind: field(r, fieldSourceKind), SourceRef: textPtr(r, fieldSourceRef),
		ProjectID: field(r, fieldProject), RepoID: textPtr(r, fieldRepo),
		Kind: field(r, fieldKind), Body: field(r, fieldBody),
		ValidAt: timePtr(r, fieldValidAt), ValidUntil: timePtr(r, fieldValidUntil),
		SupersededByID:   textPtr(r, fieldSuperseded),
		CreatedByActorID: textPtr(r, fieldCreatedBy), ValidatedByActorID: textPtr(r, fieldValidBy),
		TagIDs: tagsOf(r),
	}
	rec.TxAt = timePtr(r, fieldTxAt)
	if rec.CreatedByActorID == "" && !r.CreatedBy.IsZero() {
		rec.CreatedByActorID = r.CreatedBy.String()
	}
	if src != store.SourceRepo {
		rec.Store = string(src)
		rec.Label = Label(lab, src)
	}
	return rec
}

// hitOf is the v0.1.17 search hit of a memory row.
func hitOf(lab Labeler, src store.Source, h Hit, r row.Row) SearchHit {
	out := SearchHit{
		ID: r.ID.String(), Score: h.Score, Snippet: h.Snippet,
		Row: HitRow{Body: field(r, fieldBody), Kind: field(r, fieldKind), SourceKind: field(r, fieldSourceKind), TrustScore: numberOr(r, fieldTrust)},
	}
	if src != store.SourceRepo {
		out.Store = string(src)
		out.Label = Label(lab, src)
	}
	return out
}

// textPtr is a text field ("" when absent: omitted like ent's nil).
func textPtr(r row.Row, name string) string { return field(r, name) }

// numberPtr is a number field (nil when absent or not a number).
func numberPtr(r row.Row, name string) *float64 {
	n, ok := r.Fields[name].(value.Number)
	if !ok {
		return nil
	}
	f, err := n.Float64()
	if err != nil {
		return nil
	}
	return &f
}

// numberOr is a number field, 0 when absent.
func numberOr(r row.Row, name string) float64 {
	if p := numberPtr(r, name); p != nil {
		return *p
	}
	return 0
}

// timePtr is a datetime field (zero when absent or not a datetime).
func timePtr(r row.Row, name string) time.Time {
	switch v := r.Fields[name].(type) {
	case value.DateTime:
		return v.Time()
	case value.Text:
		if dt, err := value.DateTimeOf(v.String()); err == nil {
			return dt.Time()
		}
	}
	return time.Time{}
}

// writeOut is the output of a write.
func writeOut(src store.Source, w engine.Written) WriteOut {
	out := WriteOut{ID: w.Row.ID.String(), Store: string(src)}
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning{Code: string(x.Code), Field: x.Field, Message: x.Message})
	}
	return out
}
