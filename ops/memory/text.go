package memory

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
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
	if len(body) <= listBodyMax {
		return body
	}
	n := listBodyCut
	for n > 0 && !utf8.RuneStart(body[n]) {
		n--
	}
	return body[:n] + ellipsis
}

// Text is the line(s) the op set in Human ("✓ <id> <verb>", and for add
// the scope line).
func (o WriteOut) Text() string { return o.Human }

// Text prints one "<id> [<kind>] <body>" line per memory, the body cut at
// 80 bytes and " [archived]" after an archived one; "(no memories)" when
// there is none.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no memories)\n"
	}
	var b strings.Builder
	for _, m := range o.Data {
		body := m.Body
		body = cutBody(body)
		badge := ""
		if !m.ArchivedAt.IsZero() {
			badge = " [archived]"
		}
		fmt.Fprintf(&b, "%s [%s] %s%s\n", m.ID, m.Kind, body, badge)
	}
	return b.String()
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
