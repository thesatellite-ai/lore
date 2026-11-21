package memory

import (
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/morehits"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/schema"
)

// Human (op.Texter) forms of the memory outputs: v0.1.17's lines from
// saas/cmd/cli/memory.go and archive_commands.go at 4e6ed1c, without the
// colours (style.* wrapped them; colour is a later item).

// listBodyCut is how many bytes of a too-long body v0.1.17's list kept
// before the ellipsis (listBodyMax minus its length).
const listBodyCut = listBodyMax - len(ellipsis)

// cutBody is v0.1.17's list cut (a body over listBodyMax bytes keeps
// listBodyCut bytes plus the ellipsis), moved back to the last rune
// boundary so a multi-byte character is never split (v0.1.17 could emit
// invalid UTF-8 there; that was a bug, not parity).
func cutBody(body string) string {
	return textcut.Cut(body, listBodyMax, listBodyCut)
}

// Text is the line(s) the op set in Human ("✓ <id> <verb>", and for add
// the scope line).
func (o WriteOut) Text() string {
	// OV10: the metadata notes of an add/edit --meta, as meta set prints them
	var b strings.Builder
	b.WriteString(o.Human)
	for _, w := range o.Warnings {
		b.WriteString(metalist.NoteLine(w.Code, w.Message))
	}
	return b.String()
}

// LabelSuffix is the " [<label>]" end of a human list line, from a
// record's `_label` ("" for a repo row). Every noun's list ends a row of
// another store with it, so a list keeps one layout per row whatever
// store the row comes from and the label is the only extra column
// (v0.1.17 printed rows of other stores in a second layout).
func LabelSuffix(label string) string {
	if label == "" {
		return ""
	}
	return " " + label
}

// Text prints one "<id> [<kind>] <body>" line per memory, the body
// folded onto one line and cut at 80 bytes, " [archived]" after an archived one and the store label after
// a memory of another store (LabelSuffix); "(no memories)" when there is
// none.
func (o ListOut) Text() string {
	notes := ""
	if len(o.Notes) > 0 {
		notes = strings.Join(o.Notes, "\n") + "\n"
	}
	return o.rowsText() + notes
}

// rowsText is the list lines: the repo's memories first, then those of
// the other stores (v0.1.17's order), every one in the same layout.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no memories)\n"
	}
	var b, ext strings.Builder
	for _, m := range o.Data {
		w := &b
		if m.Label != "" {
			w = &ext
		}
		badge := ""
		if !m.ArchivedAt.IsZero() {
			badge = " [archived]"
		}
		if h := schema.HistoryNote(m.SupersededByID, m.ValidUntil, o.now); h != "" {
			badge += " [" + h + "]" // devco D-820: an old fact reads as history
		}
		fmt.Fprintf(w, "%s [%s] %s%s%s\n", m.ID, m.Kind, cutBody(textcut.OneLine(m.Body)), badge, LabelSuffix(m.Label))
	}
	return b.String() + ext.String()
}

// baseText prints the id, kind/trust/source (plus superseded-by, archived-at,
// valid-at and valid-until when set), a blank line and the body.
func (o ShowOut) baseText() string {
	r := o.Data
	var b strings.Builder
	// the store label after the id: which store/folder holds the row (devco D-553)
	fmt.Fprintf(&b, "%s%s\n  kind:   %s\n  trust:  %.2f\n  source: %s\n", r.ID, LabelSuffix(r.Label), r.Kind, r.TrustScore, r.SourceKind)
	if r.SupersededByID != "" {
		fmt.Fprintf(&b, "  superseded-by: %s\n", r.SupersededByID)
	}
	if !r.ArchivedAt.IsZero() {
		fmt.Fprintf(&b, "  archived-at: %s\n", r.ArchivedAt.Format(time.RFC3339))
	}
	if !r.ValidAt.IsZero() {
		fmt.Fprintf(&b, "  valid-at: %s\n", dateText(r.ValidAt))
	}
	if !r.ValidUntil.IsZero() {
		fmt.Fprintf(&b, "  valid-until: %s\n", r.ValidUntil.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "\n%s\n", r.Body)
	return b.String()
}

// Text prints one "<id> <body>" line per hit, or "(no matches)" with
// where else to look, like every other search: an empty output could not
// be told from a command that did not run (G-4).
func (o SearchOut) Text() string {
	if len(o.Data) == 0 {
		return "(no matches)\n  hint: lore search --query <words> looks in every kind of row, not only memories\n"
	}
	var b strings.Builder
	for _, h := range o.Data {
		if h.history != "" {
			fmt.Fprintf(&b, "%s %s [%s]\n", h.ID, h.Row.Body, h.history)
			continue
		}
		fmt.Fprintf(&b, "%s %s\n", h.ID, h.Row.Body)
	}
	if o.More {
		b.WriteString(morehits.Note(len(o.Data), o.limit))
	}
	return b.String()
}

// dateText is a stored date as the user gave it: a calendar day as
// YYYY-MM-DD (memory add --at 2025-03-14), an instant as RFC3339 UTC.
func dateText(t time.Time) string {
	if dates.IsDay(t) {
		return t.UTC().Format(dates.DayLayout)
	}
	return t.UTC().Format(time.RFC3339)
}
