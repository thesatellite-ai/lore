package memory

import (
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/ops/internal/textcut"
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
func (o WriteOut) Text() string { return o.Human }

// ExternalLine is v0.1.17's human line of a row from outside the checkout
// (stores_reads.go printExternalRows): "<id>  <text><status>  <label>",
// where text is the title, the title + " — " + body when both differ, or
// the body alone, folded onto one line (whitespace runs become one space,
// never cut), status is "  <status>" when the row has one, and label is
// its `_label` ("[northwind/acme]", "[me]").
func ExternalLine(id, title, body, status, label string) string {
	t := title
	if t == "" {
		t = body
	} else if body != "" && body != title {
		t += " — " + body
	}
	if status != "" {
		status = "  " + status
	}
	return id + "  " + strings.Join(strings.Fields(t), " ") + status + "  " + label + "\n"
}

// Text prints one "<id> [<kind>] <body>" line per repo memory, the body
// cut at 80 bytes and " [archived]" after an archived one, then each
// memory of another store as v0.1.17 printed it after the repo's own
// (ExternalLine, with its label); "(no memories)" when there is none.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no memories)\n"
	}
	var b, ext strings.Builder
	for _, m := range o.Data {
		if m.Label != "" {
			ext.WriteString(ExternalLine(m.ID, "", m.Body, "", m.Label))
			continue
		}
		body := m.Body
		body = cutBody(body)
		badge := ""
		if !m.ArchivedAt.IsZero() {
			badge = " [archived]"
		}
		fmt.Fprintf(&b, "%s [%s] %s%s\n", m.ID, m.Kind, body, badge)
	}
	return b.String() + ext.String()
}

// Text prints the id, kind/trust/source (plus superseded-by, archived-at
// and valid-until when set), a blank line and the body.
func (o ShowOut) Text() string {
	r := o.Data
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n  kind:   %s\n  trust:  %.2f\n  source: %s\n", r.ID, r.Kind, r.TrustScore, r.SourceKind)
	if r.SupersededByID != "" {
		fmt.Fprintf(&b, "  superseded-by: %s\n", r.SupersededByID)
	}
	if !r.ArchivedAt.IsZero() {
		fmt.Fprintf(&b, "  archived-at: %s\n", r.ArchivedAt.Format(time.RFC3339))
	}
	if !r.ValidUntil.IsZero() {
		fmt.Fprintf(&b, "  valid-until: %s\n", r.ValidUntil.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "\n%s\n", r.Body)
	return b.String()
}

// Text prints one "<id> <body>" line per hit.
func (o SearchOut) Text() string {
	var b strings.Builder
	for _, h := range o.Data {
		fmt.Fprintf(&b, "%s %s\n", h.ID, h.Row.Body)
	}
	return b.String()
}
