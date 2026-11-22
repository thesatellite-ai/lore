package ask

// updates.go — a fact that changed is answered with what it was right
// after it (devco D-897, bug hunt H-150).

// wasSecond moves the history row the first answer row replaced (the
// newest, best-scored one of hist: markReplaced's replacedBy, else a
// row superseded by it) to the second place of pick, marked history as
// before, and drops it from hist; pick stays at most top long. Why: a
// question about a change ("what is the budget for orbit now, and what
// was it") needs both sides, and the older row waited until every other
// current row was listed, usually past --top (LongMemEval knowledge
// updates: the newest fact found, the older one lost, recall_all@5 0.750
// -> 0.972 with the "was" second). Only the first row's: listing the
// "was" of every row pushed the rows about other things down. Not for a
// file question (its rows must share two words) or an everything-about
// answer (its rows are the newest of each folder).
func wasSecond(pick, hist []scored, top int) ([]scored, []scored) {
	if len(pick) == 0 || top < 2 {
		return pick, hist
	}
	first := pick[0].row.ID
	for i, h := range hist {
		by := h.replacedBy
		if by == "" {
			by = h.row.SupersededBy
		}
		if by != first {
			continue
		}
		out := make([]scored, 0, len(pick)+1)
		out = append(out, pick[0], h)
		out = append(out, pick[1:]...)
		if len(out) > top {
			out = out[:top]
		}
		rest := make([]scored, 0, len(hist)-1)
		rest = append(rest, hist[:i]...)
		return out, append(rest, hist[i+1:]...)
	}
	return pick, hist
}
