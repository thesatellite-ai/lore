package ask

import (
	"strings"
	"unicode/utf8"
)

// minCut is the fewest characters of a cut row's text an answer prints:
// the --max-chars floor, so a row cut to fit next to a file's context or
// a resource still says something.
const minCut = 200

// cutSeps are the boundaries an excerpt is cut along, coarsest first: a
// paragraph (a markdown section's parts), a line, a sentence.
var cutSeps = []string{"\n\n", "\n", ". "}

// elision marks text left out before or after an excerpt.
const elision = "…"

// excerpt is the part of text, at most room bytes, that holds the most of
// words: the best paragraph, grown with its neighbours while they fit but
// never past its section (from its markdown heading to the next one); a paragraph longer than room is cut the
// same way into lines, then sentences, then to room bytes around the
// first word it holds. With no word
// matching anywhere it is the start of text. ok is false when text fits
// whole (out is text then). The excerpt is always one contiguous part of
// text, so the caller can say what came before and after it.
//
// Why (devco D-859, public issue #86): ask kept items whole, so a row
// longer than --max-chars (a design doc, meeting notes) printed nothing
// and the answer was "(no answer)"; its part that matches the question
// is what an agent needs, and lore show --id prints the rest.
func excerpt(text string, words []string, room int) (out string, ok bool) {
	if len(text) <= room {
		return text, false
	}
	return part(text, words, room, cutSeps), true
}

// part is excerpt's step for one boundary: text split along seps[0], the
// best piece and as many of its neighbours as fit in room.
func part(text string, words []string, room int, seps []string) string {
	if len(text) <= room {
		return text
	}
	if len(seps) == 0 {
		return window(text, words, room)
	}
	sep := seps[0]
	ps := strings.Split(text, sep)
	if len(ps) == 1 {
		return part(text, words, room, seps[1:])
	}
	best := bestPiece(ps, words)
	if len(ps[best]) > room {
		return part(ps[best], words, room, seps[1:])
	}
	a, b, size := best, best+1, len(ps[best])
	fits := func(p string) bool { return size+len(sep)+len(p) <= room }
	// grow within the best piece's section: back up to and including
	// its heading, forward up to the next heading
	for grew := true; grew; {
		grew = false
		if b < len(ps) && !isHeading(ps[b]) && fits(ps[b]) {
			size += len(sep) + len(ps[b])
			b++
			grew = true
		}
		if a > 0 && !isHeading(ps[a]) && fits(ps[a-1]) {
			a--
			size += len(sep) + len(ps[a])
			grew = true
		}
	}
	return strings.Join(ps[a:b], sep)
}

// isHeading reports a markdown heading line ("## Operations").
func isHeading(p string) bool { return strings.HasPrefix(strings.TrimSpace(p), "#") }

// bestPiece is the index of the piece of ps holding the most of words,
// the first one on a tie (0 when none holds any).
func bestPiece(ps []string, words []string) int {
	best, top := 0, 0
	for i, p := range ps {
		toks, n := tokensOf(p), 0
		for _, w := range words {
			if hasWord(toks, w) {
				n++
			}
		}
		if n > top {
			best, top = i, n
		}
	}
	return best
}

// window is room bytes of text starting a little before the first of
// words it holds (at a space), the start of text when it holds none.
func window(text string, words []string, room int) string {
	lower, at := strings.ToLower(text), -1
	for _, w := range words {
		if i := strings.Index(lower, strings.ToLower(w)); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at > room/4 {
		start := at - room/4
		if sp := strings.IndexByte(text[start:at], ' '); sp >= 0 {
			start += sp + 1
		}
		for start < at && !utf8.RuneStart(text[start]) {
			start++
		}
		text = text[start:]
	}
	return cutAt(text, room)
}

// cutAt is text's first room bytes, cut back to the last space when one
// is in the second half, never inside a character.
func cutAt(text string, room int) string {
	if len(text) <= room {
		return text
	}
	if room <= 0 {
		return ""
	}
	out := text[:room]
	for len(out) > 0 && !utf8.ValidString(out) {
		out = out[:len(out)-1]
	}
	if i := strings.LastIndexByte(out, ' '); i > len(out)/2 {
		out = out[:i]
	}
	return out
}

// cutWords are the words an excerpt of a row looks for: the topics and
// words of the question the row holds, else the question's longer words.
func cutWords(matched, asked []string) []string {
	if len(matched) > 0 {
		return matched
	}
	var out []string
	for _, w := range asked {
		if len(w) >= 4 {
			out = append(out, w)
		}
	}
	return out
}

// cutBody returns body cut to the part that best answers words in room
// bytes, with an elision mark on the side where text was left out; ok
// false when body fits whole.
func cutBody(body string, words []string, room int) (string, bool) {
	if len(body) <= room {
		return body, false
	}
	marks := 2 * (len(elision) + 1)
	ex, _ := excerpt(body, words, max(room-marks, 1))
	core := strings.TrimSpace(ex)
	i := strings.Index(body, core)
	out := core
	if i > 0 && strings.TrimSpace(body[:i]) != "" {
		out = elision + "\n" + out
	}
	if i >= 0 && strings.TrimSpace(body[i+len(core):]) != "" {
		out += "\n" + elision
	}
	return out, true
}
