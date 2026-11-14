// Package contextfit decides which pinned entries of a generated context
// file (.lore/LORE.md) appear in full and which as one line, so the file
// stays under its byte budget without ever cutting an entry in the middle
// (CONTEXT_BUDGET_DESIGN 6.3, 6.4, 6.6).
//
// Layer L1: pure. The caller renders each entry's full and line text; this
// package ranks, measures and chooses.
//
//	Rank(entries)                      6.6 order: full pin (by pin time) > always > summary > newest > id
//	Fit(Plan) ─► start: must rules as lines, index full
//	          ├─ upgrade must rules to full in rank order while it fits
//	          │    (an entry over EntryMax stays a line: "line form: entry_max")
//	          ├─ still over → collapse the index to one line
//	          └─ still over → Over = true (budget_exceeded: nothing is dropped,
//	             a must rule or a critical/high hotfix never disappears)
package contextfit

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Form is how one entry appears in the file.
type Form string

// Entry forms (6.4). The pointer form (only counted in an index line) is
// for search-only rows, which never reach Fit.
const (
	FormFull Form = "full"
	FormLine Form = "line"
)

// Reason says why an entry is not in full; "" when it is.
type Reason string

// Reasons written in the render report (6.11).
const (
	ReasonNone       Reason = ""
	ReasonBudget     Reason = "line form: budget"
	ReasonEntryMax   Reason = "line form: entry_max"
	ReasonSuperseded Reason = "superseded"
	ReasonManual     Reason = "manual"
	ReasonPinned     Reason = "line form: pinned"
)

// Entry is one must rule a Plan may show in full or as a line.
type Entry struct {
	ID string
	// Full and Line are the rendered bytes of each form.
	Full, Line string
	// Rank inputs (6.6); only synced fields, so the same rows rank the same
	// on every machine.
	Always     bool
	HasSummary bool
	Updated    time.Time
	// Pin is a `lore pin --context` form ("" none, FA-CTX-6): FormFull is
	// always in full and ranks first by PinnedAt; FormLine always a line.
	Pin      Form
	PinnedAt time.Time
}

// Rank sorts entries in place by the 6.6 order: activation always first,
// then entries with a summary (a cheap line form keeps others full), then
// newest updated, then id.
func Rank(es []Entry) {
	slices.SortStableFunc(es, func(a, b Entry) int {
		fa, fb := a.Pin == FormFull, b.Pin == FormFull
		switch {
		case fa != fb:
			return boolFirst(fa)
		case fa && !a.PinnedAt.Equal(b.PinnedAt):
			if a.PinnedAt.Before(b.PinnedAt) {
				return -1 // 6.6: full pins by pin time
			}
			return 1
		case a.Always != b.Always:
			return boolFirst(a.Always)
		case a.HasSummary != b.HasSummary:
			return boolFirst(a.HasSummary)
		case !a.Updated.Equal(b.Updated):
			if a.Updated.After(b.Updated) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func boolFirst(a bool) int {
	if a {
		return -1
	}
	return 1
}

// Plan is one target file to fit.
type Plan struct {
	// Budget is the file's byte budget; 0 means no budget (LORE.full.md).
	Budget int
	// EntryMax: an entry whose full form is longer always stays a line; 0 = none.
	EntryMax int
	// Fixed is the bytes always written: header, standing instruction,
	// directive, critical hotfixes in full, high hotfix lines, footer.
	Fixed int
	// Index and IndexCollapsed are the bytes of the full and collapsed index.
	Index, IndexCollapsed int
	// Must are the must rules, ranked (Rank).
	Must []Entry
}

// Result is what Fit chose.
type Result struct {
	// Forms and Reasons are parallel to Plan.Must.
	Forms   []Form
	Reasons []Reason
	// IndexCollapsed is true when the index became one line.
	IndexCollapsed bool
	// Total is the file's bytes with these choices.
	Total int
	// Over is true when even all lines and the collapsed index exceed the
	// budget (budget_exceeded): the file is still written whole.
	Over bool
}

// Fit chooses forms (6.3): all must rules start as lines; each is upgraded
// to full in rank order while the file stays within budget. The index
// collapses only when the lines alone do not fit.
func Fit(p Plan) Result {
	r := Result{Forms: make([]Form, len(p.Must)), Reasons: make([]Reason, len(p.Must))}
	total := p.Fixed + p.Index
	for i, e := range p.Must {
		r.Forms[i], r.Reasons[i] = FormLine, ReasonBudget
		total += len(e.Line)
	}
	// FA-CTX-6: full pins are in full whatever the budget (like critical
	// hotfixes); line pins never grow.
	for i, e := range p.Must {
		if e.Pin == FormFull {
			total += len(e.Full) - len(e.Line)
			r.Forms[i], r.Reasons[i] = FormFull, ReasonNone
		}
	}
	if p.Budget > 0 && total > p.Budget {
		r.IndexCollapsed = true
		total += p.IndexCollapsed - p.Index
	}
	for i, e := range p.Must {
		if e.Pin == FormFull {
			continue
		}
		if e.Pin == FormLine {
			r.Reasons[i] = ReasonPinned
			continue
		}
		if p.EntryMax > 0 && len(e.Full) > p.EntryMax {
			r.Reasons[i] = ReasonEntryMax
			continue
		}
		grow := len(e.Full) - len(e.Line)
		// A full form no longer than its line always wins (it saves bytes).
		if grow > 0 && p.Budget > 0 && total+grow > p.Budget {
			continue // stays a line; later (smaller) entries may still fit
		}
		total += grow
		r.Forms[i], r.Reasons[i] = FormFull, ReasonNone
	}
	r.Total = total
	r.Over = p.Budget > 0 && total > p.Budget
	return r
}

// maxFirstSentence: a first sentence longer than this is no short form (CQ4).
const maxFirstSentence = 240

// MaxSummary is the longest summary field accepted (6.4: up to 200 chars).
const MaxSummary = 200

// NoShortForm is the line text when an entry has no usable short form.
const NoShortForm = "(no short form; run `lore show --id <id>`)"

// FirstSentence returns the body's first sentence (up to the first ". ",
// "! ", "? " or line break), trimmed, and ok false when it is empty or
// longer than 240 characters (CQ4: a whole sentence, never a cut).
func FirstSentence(body string) (string, bool) {
	s := strings.TrimSpace(body)
	s = strings.TrimLeft(s, "#>*- \t")
	end := len(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		end = i
	}
	for _, sep := range []string{". ", "! ", "? "} {
		if i := strings.Index(s[:end], sep); i >= 0 && i+1 < end {
			end = i + 1
		}
	}
	out := strings.TrimSpace(s[:end])
	if out == "" || utf8.RuneCountInString(out) > maxFirstSentence {
		return "", false
	}
	return out, true
}

// ShortForm picks an entry's one-line text: the summary, else the title
// (hotfixes), else the first sentence of body (rules), else the start of
// the body cut at a word to LineCut characters, else NoShortForm with the
// id filled in (6.4, CQ3). A must rule written without --summary as one
// long sentence still says what it requires (bug hunt G-6: it used to be
// a bare id that nobody reads).
func ShortForm(id, summary, title, body string) string {
	return ShortFormMax(id, summary, title, body, 0)
}

// LineCut is the default length of a line cut from a body with no short
// sentence: long enough to say what the rule is about.
const LineCut = 120

// ShortFormMax is ShortForm with the text cut at a word to at most max
// characters (0: no extra cut). A render over its budget shortens every
// line this way before it gives up (bug hunt G-6).
func ShortFormMax(id, summary, title, body string, max int) string {
	s := oneLine(summary)
	if s == "" {
		s = oneLine(title)
	}
	if s == "" {
		if fs, ok := FirstSentence(body); ok {
			s = oneLine(fs)
		} else {
			s = Cut(oneLine(body), LineCut)
		}
	}
	if s == "" {
		return strings.Replace(NoShortForm, "<id>", id, 1)
	}
	if max > 0 {
		s = Cut(s, max)
	}
	return s
}

// ellipsis ends a cut text, so a reader sees it is not the whole entry.
const ellipsis = "…"

// Cut shortens s to at most max characters (ellipsis included), ending at
// a word boundary when one is in the second half; s itself when it fits.
func Cut(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	all := []rune(s)
	r := all[:max-1]
	// The last word is whole when a space follows it; otherwise go back
	// to the last space so no word is split.
	if all[max-1] != ' ' {
		if i := lastSpace(r); i > len(r)/2 {
			r = r[:i]
		}
	}
	return strings.TrimRight(string(r), " ,;:.-") + ellipsis
}

// lastSpace is the index of r's last space, -1 when none.
func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == ' ' {
			return i
		}
	}
	return -1
}

// oneLine folds line breaks so a line form stays one markdown bullet.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// BriefLine is a line form without the per-line `lore show` pointer, for
// a file that says once, in its footer, how to see the full text (LORE.md
// and the folder files; bug hunt G-6: the pointer repeated on every line
// cost more bytes than the rule text).
func BriefLine(id, short string) string {
	return "- `" + id + "` " + short + "\n"
}

// LineForm is the 6.4 line: - `<id>` <short> Full: `lore show <id>`.
func LineForm(id, short string) string {
	return "- `" + id + "` " + short + " Full: `lore show --id " + id + "`\n"
}
