package ask

import "slices"

// Long rows rank by their best section (devco D-969, bug hunt H-228, Sync
// benchmark set 5 asks 2 and 4). A row longer than longRow characters
// with two or more markdown sections (sections: heading-bounded parts)
// is printed by section (chunkLong), so it is ranked by section too: a
// question word counts fully only when its best section or its title
// holds it (devco D-972; one its other sections hold counts a point), and
// its score gains headWord for each question word the heading path of its
// best section holds beyond the row's title, the way a short row's title
// already names what it is about, unless more than a quarter of the
// scored rows hold that word (distinctBonus). The best section is the one holding
// the most of the question's words and topics in its heading path and
// text, a heading word counting twice. The threshold is chunkLong's: a
// row that prints whole ranks whole, one that prints by section ranks by
// section, and the recall gate's rows are all shorter, so they rank as
// before.
//
// Why: an as-built architecture note of several KB answers one question
// per section; its whole text holds the question's words no more often
// than a short decision or memory that only shares more of them, and the
// short row ranked first (S5-29, S5-33, S5-39: the note with the catalog
// row fields, the self-decision error or the boot order section was not
// shown). A heading that names what the question asks is what says the
// section answers it.

// headWord is what each question word or topic held by a long row's best
// section heading adds to the row's score: one more question word (3),
// so a section titled with the asked words outranks a short row holding
// one or two more words anywhere in its text.
const headWord = 3

// rowSection is one section of a long row as ask ranks it: the word set
// of its heading path beyond the row's title, and of its heading path and
// text together.
type rowSection struct {
	head, words map[string]bool
}

// sectionsOf is r's sections as word sets, nil when r is no longer than
// longRow or has fewer than two sections; built once per row and shared
// with every later call's copy of a kept corpus row (rowSplit), so a warm
// ask splits no row again. The kept index grows by one word set per
// section of the long rows only.
func (r *textRow) sectionsOf() []rowSection {
	if len(r.Body) <= longRow {
		return nil
	}
	if !r.sectioned {
		r.secs = shareSplit(r.split, func(s *rowSplit) *splitSlot[[]rowSection] { return &s.secs }, func() []rowSection {
			return splitSections(r.Title, r.Body)
		})
		r.sectioned = true
	}
	return r.secs
}

// splitSections is body's sections (sections) as rowSections; a heading
// word that title holds too is not a heading word (the note's own title,
// often its first heading, says nothing about one section).
func splitSections(title, body string) []rowSection {
	secs := sections(body)
	if len(secs) < 2 {
		return nil
	}
	own := tokensOf(title)
	out := make([]rowSection, 0, len(secs))
	for _, s := range secs {
		head := tokensOf(s.path)
		for w := range own {
			delete(head, w)
		}
		out = append(out, rowSection{head: head, words: tokensOf(s.path + "\n" + s.text)})
	}
	return out
}

// bestSec is the section a long row ranks by for one question
// (bestSection): its word set, and the question words and topic names
// its heading path holds.
type bestSec struct {
	words     map[string]bool
	heldHeads []string
}

// holds reports whether the best section, or r's title and first line,
// hold question word w (or spell out the identifier q wrote for it).
func (b *bestSec) holds(q question, r *textRow, w string) bool {
	if hasWord(b.words, w) || identWord(q, b.words, w) {
		return true
	}
	first := r.firstTokens()
	return hasWord(first, w) || identWord(q, first, w)
}

// bestSection is the section r ranks by for q, nil for a row that is not
// long (sectionsOf): the one holding the most of the question's words and
// topics in its heading path and text, a heading word counting twice;
// the first such section on a tie. Its heading words add headWord each
// (score); distinctBonus takes back the ones most scored rows hold.
//
// Why only the best section's words count (devco D-972, bug hunt H-231,
// Sync benchmark set 6 item 1): a several-KB note holds most of a
// question's common words somewhere (file, worker, command, run) and
// ranked by all of them, plus its heading, above the short memory naming
// the asked command (files resync, vault-rekey) that lore search
// ranks first. A section answers by what it holds itself.
func bestSection(q question, ms []match, r *textRow) *bestSec {
	secs := r.sectionsOf()
	if len(secs) == 0 {
		return nil
	}
	best, at := -1, 0
	var heads, bestHeads []string
	for k, s := range secs {
		held := 0
		heads = heads[:0]
		hold := func(name string, ws []string) {
			if slices.ContainsFunc(ws, func(w string) bool { return hasWord(s.words, w) }) {
				held++
				if slices.ContainsFunc(ws, func(w string) bool { return hasWord(s.head, w) }) {
					heads = append(heads, name)
				}
			}
		}
		for _, t := range q.topics {
			hold(t.name, t.row)
		}
		for _, w := range q.words {
			if !namesResource(w, ms) {
				hold(w, []string{w})
			}
		}
		if n := held + len(heads); n > best {
			best, at, bestHeads = n, k, slices.Clone(heads)
		}
	}
	return &bestSec{words: secs[at].words, heldHeads: bestHeads}
}

// matchingMark follows the heading path of the section a long row prints
// first (bestSections): the one that answers the question best, so an
// agent reading a long note starts at the part that holds the answer.
const matchingMark = " (matching section)"
