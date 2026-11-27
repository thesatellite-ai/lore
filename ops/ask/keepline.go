package ask

// keepline.go — a cut row keeps the sentence its candidate line shows
// (devco D-1028, bug hunt H-288, set 9 ask c).
//
// Why: a row listed as a candidate shows the sentence that best matches
// the question (matchLine: the question's rare words weighing most); the
// same row printed as an excerpt was cut by its best sections (cutBody),
// which weigh the words by section, so it could be printed first and
// still leave out the very sentence its candidate line had shown
// (held-out bench: the scheduler note moved from candidate 1 to the first
// row and lost the sentence that answered; set 9: an excerpt matched an
// earlier mention of the asked term and stopped before the asked rules).

import "strings"

// cutKeeping is cutBody of src in room, holding the sentence of src that
// best matches the question's words qwords (bestSentence, matchLine's):
// when the cut leaves it out, src is cut again in the room less that
// sentence, and the sentence follows under its heading path and "…". A
// sentence longer than half the room is not forced in. title is the row's
// title: a heading that only repeats it is not printed.
func cutKeeping(src, title string, words, qwords []string, room int, st *subjectTerms) (string, bool) {
	text, cut := cutBody(src, words, room, st)
	if !cut {
		return text, cut
	}
	best, _, ok := bestSentence(src, qwords, st)
	s := strings.TrimSpace(best.text)
	if !ok || s == "" || strings.Contains(text, s) {
		return text, cut
	}
	part := elision + "\n" + s
	if path := headPath(best.heads, title); path != "" {
		part = sectionMark + path + "\n" + part
	}
	if len(part)+2 > room/2 {
		return text, cut
	}
	narrow, ok := cutBody(src, words, room-len(part)-2, st)
	if !ok {
		return text, cut
	}
	if strings.Contains(narrow, s) {
		return narrow, true
	}
	return strings.TrimRight(narrow, "\n") + "\n\n" + part, true
}

// headPath is a sentence's heading lines as a section path ("Design >
// Request path"), the "#" marks dropped and a first heading that is the
// row's title left out.
func headPath(heads []string, title string) string {
	var out []string
	for i, h := range heads {
		h = strings.TrimSpace(strings.TrimLeft(h, "#"))
		if h == "" || i == 0 && strings.EqualFold(h, strings.TrimSpace(title)) {
			continue
		}
		out = append(out, h)
	}
	return strings.Join(out, " > ")
}
