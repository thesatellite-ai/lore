package ask

// leftout.go — a cut row says which of the question's words its cut
// left out (devco D-974, bug hunt H-233, Sync benchmark set 6 S6-01).
//
//	"what does Queue.Enqueue return when the DedupKey matches a run
//	 that is not terminal" ─▶ the runq note (4,000 characters) cut to
//	 its best sections, the Deduplication one not among them ─▶
//	   (cut to the part that answers the question; the whole row is
//	    4012 characters and the part left out holds dedup, terminal;
//	    lore show --id <id> prints it whole)
//
// Why: the excerpt picker keeps a row's best sections, and when it
// keeps the wrong ones the sentence asked about is in the row but not on
// the page; an agent reading only the page then answered "not in
// memory". Naming the question's words the cut left out says the answer
// may be in the part not printed and where to read it. Read from the
// words the whole row matched (Row.Matched, as score read them) against
// the printed title and excerpt, so it holds whatever the excerpt picker
// keeps.

// maxLeftOut is the most words the cut note names: enough to say what
// the cut text is about, short enough to stay one line.
const maxLeftOut = 6

// leftOutOf is the question words of matched (the words the whole row
// holds, a topic name left out) that title, kept (the printed excerpt)
// and the row's printed comments do not hold, in the question's order,
// at most maxLeftOut; nil when the excerpt holds them all.
func leftOutOf(q question, matched []string, title, kept string, comments []Comment) []string {
	if len(matched) == 0 {
		return nil
	}
	held := map[string]bool{}
	for _, w := range matched {
		held[w] = true
	}
	var toks map[string]bool // the printed text's words, built at the first question word the row holds
	var out []string
	for _, w := range q.words {
		if !held[w] {
			continue
		}
		if toks == nil {
			text := title + "\n" + kept
			for _, c := range comments {
				text += "\n" + c.Body
			}
			toks = tokensOf(text)
		}
		if hasWord(toks, w) || identWord(q, toks, w) {
			continue
		}
		out = append(out, w)
		if len(out) == maxLeftOut {
			break
		}
	}
	return out
}
