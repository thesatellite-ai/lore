package ask

import (
	"cmp"
	"math"
	"slices"
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
// bytes; ok false when body fits whole. A markdown row with headings is
// cut to its best sections (bestSections), anything else to its best
// paragraph with an elision mark on the side where text was left out
// (cutText).
func cutBody(body string, words []string, room int) (string, bool) {
	if len(body) <= room {
		return body, false
	}
	if out, ok := bestSections(body, words, room); ok {
		return out, true
	}
	return cutText(body, words, room), true
}

// cutText is body cut to room bytes: the sentences that hold the
// question's words under their headings, then its opening
// (keepSentences, devco D-970); with no sentence telling them apart its
// excerpt (the start of body when no word matches), with an elision mark
// on the side where text was left out. It is the one cut every printed
// row, section and comment of an answer goes through.
func cutText(body string, words []string, room int) string {
	if len(body) <= room {
		return body
	}
	if out, ok := keepSentences(body, words, room); ok {
		return out
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
	return out
}

// sectionMark leads each section a cut row prints, followed by the
// section's heading path ("Orbit design notes > Rollout plan").
const sectionMark = "§ "

// topPath is the heading path printed for the text before a row's first
// heading.
const topPath = "(top of the row)"

// section is one heading-bounded part of a markdown text: the path of
// headings it sits under (its own last) and its text without its own
// heading line.
type section struct {
	path string
	text string
}

// sections splits text at its markdown headings (ATX "## Title" and
// setext "Title" over a line of = or -, never inside a code fence) into
// the text before the first heading (when it holds any) and one section
// per heading. A heading with no text of its own (a note title right
// above its first sub-heading) is merged into the section after it, so
// no section is a bare heading. Nil when text has no heading.
func sections(text string) []section {
	lines := strings.SplitAfter(text, "\n")
	type head struct {
		level int
		title string
	}
	var (
		out   []section
		stack []head
		cur   = section{path: topPath}
		body  strings.Builder
		fence string
		found bool
	)
	flush := func() {
		cur.text = body.String()
		body.Reset()
		if strings.TrimSpace(cur.text) == "" {
			return // a bare heading: the next section's path holds it
		}
		out = append(out, cur)
	}
	open := func(level int, title string) {
		flush()
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, head{level, title})
		titles := make([]string, len(stack))
		for i, h := range stack {
			titles[i] = h.title
		}
		cur = section{path: strings.Join(titles, " > ")}
		found = true
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if f := fenceOf(trim); f != "" && (fence == "" || strings.HasPrefix(trim, fence)) {
			if fence == "" {
				fence = f
			} else {
				fence = ""
			}
			body.WriteString(line)
			continue
		}
		if fence != "" {
			body.WriteString(line)
			continue
		}
		if level, title, ok := atxHeading(line); ok {
			open(level, title)
			continue
		}
		if i+1 < len(lines) && trim != "" && !strings.HasPrefix(trim, ">") && !strings.HasPrefix(trim, "-") {
			if level := setextLevel(lines[i+1]); level > 0 {
				open(level, trim)
				i++
				continue
			}
		}
		body.WriteString(line)
	}
	flush()
	if !found {
		return nil
	}
	return out
}

// fenceOf is the code fence a trimmed line opens or closes ("```" or
// "~~~"), "" when it is no fence line.
func fenceOf(trim string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trim, f) {
			return f
		}
	}
	return ""
}

// atxHeading reads an ATX heading line ("## Rollout plan", at most three
// spaces in): its level and its title without the #s.
func atxHeading(line string) (int, string, bool) {
	l := strings.TrimRight(line, "\r\n")
	indent := len(l) - len(strings.TrimLeft(l, " "))
	if indent > 3 {
		return 0, "", false
	}
	l = l[indent:]
	level := len(l) - len(strings.TrimLeft(l, "#"))
	if level < 1 || level > 6 || (len(l) > level && l[level] != ' ' && l[level] != '\t') {
		return 0, "", false
	}
	title := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(l[level:]), "#"))
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

// setextLevel is 1 for a line of = and 2 for a line of - (at least three)
// under a heading's text, 0 for any other line.
func setextLevel(line string) int {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return 0
	}
	switch {
	case strings.Trim(t, "=") == "":
		return 1
	case strings.Trim(t, "-") == "":
		return 2
	}
	return 0
}

// bestSections is body's sections that answer words best, best first,
// as many as fit in room bytes, each after a line with sectionMark and
// its heading path; the first one cut to its best part (cutText) when it
// is longer than room. A section's score is the sum, over the words its
// heading path or text holds, of ln(1 + n/df) (n sections, df of them
// holding the word): a word most sections repeat weighs little, a word
// every section holds nothing (a section holding only such words is not
// printed) and a word only one section holds picks it. ok is false when
// body has no heading or no section holds a word the others do not (the
// caller cuts by paragraph then).
//
// Why (devco D-888, the follow-up of public issue #86): a long note's
// filler repeats the question's words in every section, so the paragraph
// holding the most of them was rarely the one with the answer (103 of
// 152 long-note questions answered); its heading ("Rollout plan") and
// the words only one section holds name the right section, and the next
// best sections of the same note follow while they fit.
func bestSections(body string, words []string, room int) (string, bool) {
	secs := sections(body)
	if len(secs) < 2 || len(words) == 0 {
		return "", false
	}
	toks := make([]map[string]bool, len(secs))
	for i, s := range secs {
		toks[i] = tokensOf(s.path + "\n" + s.text)
	}
	score := make([]float64, len(secs))
	n := float64(len(secs))
	for _, w := range words {
		var hold []int
		for i := range secs {
			if hasWord(toks[i], strings.ToLower(w)) {
				hold = append(hold, i)
			}
		}
		if len(hold) == len(secs) {
			continue // held by every section: it picks none of them
		}
		for _, i := range hold {
			score[i] += math.Log(1 + n/float64(len(hold)))
		}
	}
	order := make([]int, 0, len(secs))
	for i := range secs {
		if score[i] > 0 {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		return "", false
	}
	// best first; a tie keeps the order of the note
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(score[b], score[a]) })
	var parts []string
	used := 0
	for _, i := range order {
		head := sectionMark + secs[i].path + "\n"
		text := strings.TrimSpace(secs[i].text)
		sep := 0
		if len(parts) > 0 {
			sep = 2 // the blank line between two sections
		}
		left := room - used - sep - len(head)
		if len(text) > left {
			if len(parts) > 0 {
				continue // a lower section that fits may still follow
			}
			text = cutText(text, words, max(left, 1))
		}
		parts = append(parts, head+text)
		used += sep + len(head) + len(text)
	}
	return strings.Join(parts, "\n\n"), true
}
