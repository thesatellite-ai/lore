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

// memoryID parses a memory id argument (E_INVALID_ID otherwise).
func memoryID(s string) (ids.ID[ids.Memory], error) {
	id, err := ids.Parse[ids.Memory](strings.TrimSpace(s))
	if err != nil {
		return ids.ID[ids.Memory]{}, errs.Wrap(errs.InvalidID, "memory: "+quoteID(s)+" is not a memory id (mem_…)", err)
	}
	return id, nil
}

// maxQuotedID bounds how much of a bad id an error repeats.
const maxQuotedID = 48

// quoteID renders a user-given id for an error, truncated.
func quoteID(s string) string {
	if len(s) > maxQuotedID {
		s = s[:maxQuotedID] + ellipsis
	}
	return `"` + s + `"`
}

// field returns the text form of a row field ("" when absent or null).
func field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// itemOf is the list form of a memory row.
func itemOf(src store.Source, r row.Row) Item {
	body := field(r, fieldBody)
	if rs := []rune(body); len(rs) > listBodyMax {
		body = string(rs[:listBodyMax-len(ellipsis)]) + ellipsis
	}
	return Item{
		ID: r.ID.String(), Store: string(src), Kind: field(r, fieldKind), Body: body,
		Archived: field(r, fieldArchivedAt) != "",
	}
}

// memoryOf is the full form of a memory row.
func memoryOf(src store.Source, r row.Row) Memory {
	return Memory{
		ID: r.ID.String(), Store: string(src), Kind: field(r, fieldKind), Body: field(r, fieldBody),
		ProjectID: field(r, fieldProject), RepoID: field(r, fieldRepo),
		SourceKind: field(r, fieldSourceKind), SourceRef: field(r, fieldSourceRef),
		ArchivedAt: field(r, fieldArchivedAt), ValidUntil: field(r, fieldValidUntil), Superseded: field(r, fieldSuperseded),
		CreatedAt: timeText(r.CreatedAt), CreatedBy: r.CreatedBy.String(),
		UpdatedAt: timeText(r.UpdatedAt), UpdatedBy: r.UpdatedBy.String(),
		HLC: r.HLC.String(),
	}
}

// timeText renders an audit time like a datetime field ("" when unset).
func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return value.DateTimeFrom(t).String()
}

// writeOut is the output of a write.
func writeOut(src store.Source, w engine.Written) WriteOut {
	out := WriteOut{ID: w.Row.ID.String(), Store: string(src)}
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning{Code: string(x.Code), Field: x.Field, Message: x.Message})
	}
	return out
}
