package ask

// spans.go — a command or name the question writes between backticks
// (devco D-1028, bug hunt H-288, set 9 ask a).
//
// Why: "what does `task deps-bump` change" asks about one command; read
// as words, task, deps and bump are common words that every long note
// about tasks and dependencies holds, and on the private benchmark the
// short memory whose first words were the command ranked fortieth of
// search's hits while a search for the command alone listed it first. A row holding
// the span as written names the asked thing, so it comes before the
// rows that only hold its words.

import (
	"regexp"
	"strings"
)

// reSpan is a markdown code span: text between single backticks.
var reSpan = regexp.MustCompile("`([^`\n]+)`")

// spansOf is the code spans of text of two or more words, lower case,
// runs of white space as one space. A one-word span (an identifier) is
// a question word already, read as written (idents.go).
func spansOf(text string) []string {
	var out []string
	for _, m := range reSpan.FindAllStringSubmatch(text, -1) {
		s := strings.Join(strings.Fields(strings.ToLower(m[1])), " ")
		if strings.Contains(s, " ") {
			out = append(out, s)
		}
	}
	return out
}

// holdsSpan reports whether row r's title or body writes one of spans
// (spansOf) as written, case and runs of white space aside.
func holdsSpan(r *textRow, spans []string) bool {
	if len(spans) == 0 {
		return false
	}
	text := strings.Join(strings.Fields(strings.ToLower(docText(r))), " ")
	for _, s := range spans {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}
