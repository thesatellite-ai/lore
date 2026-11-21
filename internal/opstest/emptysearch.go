package opstest

// emptysearch.go — devco D-832 (hunt H-88): since devco D-821 an empty
// search answers with a note under its empty line ("searched:", "read
// as:", 1-3 "try:" lines) and the top-level --json fields searched and try.
// The tests that pin the v0.1.17 empty answer ("(no matches)\n", the
// {"count":0,"data":null} envelope) compared the whole answer and broke.
// Loosening them to a Contains would let the v0.1.17 part drift unseen, so
// these helpers split the answer instead: the note must have exactly its
// shape (nothing missing, nothing extra, in its place), and the part before
// it is returned for the test's exact comparison as before.

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// The line prefixes of the note under an empty search's text answer
// (wire/emptysearch.go emptySearchLines).
const (
	emptySearchedLine = "  searched: "
	emptyReadAsLine   = "  read as: "
	emptyTryLine      = "  try: "
	emptyWordsPart    = "words: "
)

// emptyTryMax is the most next commands the note lists (wire's emptyTryMax).
const emptyTryMax = 3

// EmptyNote is what an empty search's note says: the stores and kinds it
// read, the words the query was read as and the next commands to try.
type EmptyNote struct {
	Stores []string `json:"stores"`
	Kinds  []string `json:"kinds"`
	Words  []string `json:"words"`
	// Type and Dates: how ask read the question, the dates read
	Type  string   `json:"type,omitempty"`
	Dates string   `json:"dates,omitempty"`
	Try   []string `json:"-"`
}

// EmptyText splits text, an empty search's text answer, into the part
// before the note (what v0.1.17 printed: "(no matches)\n" and any hint
// line of the op) and the note. It fails t unless the note is there in
// full and ends the answer: one searched line naming stores and kinds, one
// read-as line, then 1-3 try lines, each a lore command.
func EmptyText(t testing.TB, text string) (string, EmptyNote) {
	t.Helper()
	at := strings.Index(text, "\n"+emptySearchedLine)
	if at < 0 {
		t.Fatalf("empty answer without its note (no %q line):\n%s", strings.TrimSpace(emptySearchedLine), text)
		return text, EmptyNote{}
	}
	base, note := text[:at+1], text[at+1:]
	lines := strings.Split(strings.TrimSuffix(note, "\n"), "\n")
	if !strings.HasSuffix(note, "\n") || len(lines) < 3 || len(lines) > 2+emptyTryMax {
		t.Fatalf("empty answer's note is not searched + read as + 1-%d try lines:\n%s", emptyTryMax, note)
		return base, EmptyNote{}
	}
	var n EmptyNote
	searched, ok := strings.CutPrefix(lines[0], emptySearchedLine)
	stores, kinds, paren := strings.Cut(searched, " (")
	if !ok || !paren || stores == "" || !strings.HasSuffix(kinds, ")") || kinds == ")" {
		t.Fatalf("searched line is not %q<stores> (<kinds>):\n%s", emptySearchedLine, note)
		return base, EmptyNote{}
	}
	n.Stores = strings.Split(stores, ", ")
	n.Kinds = strings.Split(strings.TrimSuffix(kinds, ")"), ", ")
	readAs, ok := strings.CutPrefix(lines[1], emptyReadAsLine)
	if !ok || readAs == "" {
		t.Fatalf("second line of the note is not %q…:\n%s", emptyReadAsLine, note)
		return base, EmptyNote{}
	}
	for _, part := range strings.Split(readAs, "; ") {
		if w, isWords := strings.CutPrefix(part, emptyWordsPart); isWords {
			n.Words = strings.Split(w, ", ")
		}
	}
	for _, l := range lines[2:] {
		cmd, ok := strings.CutPrefix(l, emptyTryLine)
		if !ok || !strings.HasPrefix(cmd, "lore ") {
			t.Fatalf("note line %q is no %q<lore command>:\n%s", l, emptyTryLine, note)
			return base, EmptyNote{}
		}
		n.Try = append(n.Try, cmd)
	}
	return base, n
}

// EmptyJSON splits js, an empty search's --json answer, into the answer
// without the note's top-level fields (what v0.1.17 printed, keys in their
// order) and the note. It fails t unless searched and try are the first
// two fields, searched holds stores, kinds and words lists (never null),
// and try lists 1-3 lore commands.
func EmptyJSON(t testing.TB, js string) (string, EmptyNote) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(js))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("empty answer is no JSON object: %s", js)
		return js, EmptyNote{}
	}
	var n EmptyNote
	for _, field := range []string{wire.SearchedField, wire.TryField} {
		key, err := dec.Token()
		if err != nil || key != field {
			t.Fatalf("empty answer's field %q is not where the note puts it (first searched, then try): %s", field, js)
			return js, EmptyNote{}
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatalf("empty answer's %q: %v: %s", field, err, js)
			return js, EmptyNote{}
		}
		if field == wire.TryField {
			err = json.Unmarshal(raw, &n.Try)
		} else {
			err = strictDecode(raw, &n)
		}
		if err != nil {
			t.Fatalf("empty answer's %q is not the note's shape: %v: %s", field, err, raw)
			return js, EmptyNote{}
		}
	}
	if n.Stores == nil || n.Kinds == nil || n.Words == nil || len(n.Kinds) == 0 {
		t.Fatalf("empty answer's searched lacks stores, kinds or words (or one is null): %s", js)
	}
	if len(n.Try) == 0 || len(n.Try) > emptyTryMax || slices.ContainsFunc(n.Try, func(c string) bool { return !strings.HasPrefix(c, "lore ") }) {
		t.Fatalf("empty answer's try is not 1-%d lore commands: %v", emptyTryMax, n.Try)
	}
	rest := js[dec.InputOffset():]
	return "{" + strings.TrimPrefix(rest, ","), n
}

// strictDecode decodes raw into v refusing a field v does not name, so a
// field the note gains shows up here and in the test that reads it.
func strictDecode(raw json.RawMessage, v *EmptyNote) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// EmptyTextOf is EmptyText's part before the note, failing t unless the
// note read the query as exactly words (in order).
func EmptyTextOf(t testing.TB, text string, words ...string) string {
	t.Helper()
	base, n := EmptyText(t, text)
	if !slices.Equal(n.Words, words) {
		t.Errorf("empty answer read the query as %q, want %q:\n%s", n.Words, words, text)
	}
	return base
}

// EmptyJSONOf is EmptyJSON's answer without the note, failing t unless
// the note read the query as exactly words (in order).
func EmptyJSONOf(t testing.TB, js string, words ...string) string {
	t.Helper()
	rest, n := EmptyJSON(t, js)
	if !slices.Equal(n.Words, words) {
		t.Errorf("empty answer read the query as %q, want %q: %s", n.Words, words, js)
	}
	return rest
}
