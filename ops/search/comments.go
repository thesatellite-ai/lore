package search

// Comment hits are history (devco D-942, hunt H-191, feature F-3): a
// comment (cmt_…) says what someone said about a row at one time, not
// what is true now. Before this, a comment holding more of the query's
// words than the decision it commented on ranked above that decision with
// no label, so an agent took a past remark for the answer.
//
//	hits, best first ─▶ GroupComments
//	  a comment whose row is listed     ─▶ moved directly under that row (the
//	                                       row rises to the comment's place)
//	  a comment whose row is not listed ─▶ that row is listed first (ViaComment),
//	                                       the comment under it
//	  a comment whose row is gone or out of scope ─▶ keeps its place, labelled

import (
	"cmp"
	"slices"
	"strings"
)

// CommentOn names the row a comment hit comments on: its table, kind,
// id and title (or the start of its body). Title is "" when the row could
// not be read (deleted, or a table this binary does not know).
type CommentOn struct {
	Table  string `json:"table" desc:"the table of the row the comment is on (decisions, memories, …)"`
	Entity string `json:"entity" desc:"the kind of that row (decision, memory, …)"`
	ID     string `json:"id" desc:"the id of that row"`
	Title  string `json:"title,omitempty" desc:"that row's title, or the start of its body; empty when the row could not be read"`
}

// CommentParent reads the row comment c is on, as a hit of its own kind.
// found is false when the row cannot be read (gone, unknown table);
// inScope is false when the search's scope (--store, --repo, the
// project, archived) leaves the row out: its title still labels the
// comment, but it is not listed.
type CommentParent func(c CommentOn) (h Hit, found, inScope bool)

// GroupComments labels every comment hit with the row it is on and
// places it directly under that row (D-942): a comment never ranks above
// the row it comments on. The row and its comments stand where the first
// of them stood (a current row rises to its best comment's place; a row
// that is history keeps its own place after the current hits). When only
// the comment matched, the row it is on is listed in the comment's place
// (ViaComment set, the comment's score) with the comment under it. hits
// are best first; the order of everything else is kept, except that a
// stale row raised by its comment ranks as stale (devco:D-941). parent
// is asked once per commented row.
func GroupComments(hits []Hit, parent CommentParent) []Hit {
	rows := map[string]Hit{} // the rows listed, by id
	for _, h := range hits {
		if h.CommentOn.ID == "" {
			rows[h.ID] = h
		}
	}
	type read struct {
		hit            Hit
		found, inScope bool
	}
	parents := map[string]read{}
	for i := range hits {
		c := &hits[i].CommentOn
		if c.ID == "" {
			continue
		}
		p, ok := parents[c.ID]
		if !ok {
			p.hit, p.found, p.inScope = parent(*c)
			parents[c.ID] = p
		}
		if p.found {
			c.Entity, c.Title = p.hit.Entity, parentTitle(p.hit)
		}
		if _, listed := rows[c.ID]; !listed && p.found && p.inScope {
			ph := p.hit
			ph.ViaComment, ph.Score, ph.Partial = hits[i].ID, hits[i].Score, hits[i].Partial
			rows[c.ID] = ph
		}
	}
	under := map[string][]Hit{} // a listed row's comments, best first
	for _, h := range hits {
		if _, listed := rows[h.CommentOn.ID]; h.CommentOn.ID != "" && listed {
			under[h.CommentOn.ID] = append(under[h.CommentOn.ID], h)
		}
	}
	var groups []hitGroup
	done := map[string]bool{}
	emit := func(id string) {
		if !done[id] {
			done[id] = true
			groups = append(groups, groupOf(rows[id], under[id]))
		}
	}
	for _, h := range hits {
		switch r, listed := rows[h.CommentOn.ID]; {
		case h.CommentOn.ID == "":
			emit(h.ID)
		case !listed:
			groups = append(groups, groupOf(h, nil)) // its row is gone or out of scope: labelled, in place
		case r.History == "" || r.ViaComment != "":
			emit(h.CommentOn.ID) // the row comes up to its comment's place
		}
		// else: a row that is history keeps its own place; its comments follow it there
	}
	// devco:D-941 with D-942: a row raised to its comment's place still
	// ranks as a stale fact when it is one, so a stale row never stands
	// above a fresh row with the same match because a comment on it
	// matched. Without a stale row this keeps the order above (each group
	// already stands at its best member's place).
	slices.SortStableFunc(groups, func(a, b hitGroup) int {
		if a.history != b.history {
			if b.history {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.rank, a.rank)
	})
	out := make([]Hit, 0, len(hits)+len(rows))
	for _, g := range groups {
		out = append(out, g.hits...)
	}
	return out
}

// hitGroup is a row and the comments listed under it (or one comment
// whose row is not listed), placed together.
type hitGroup struct {
	hits    []Hit
	history bool    // the group stands after the current hits
	rank    float64 // the group's place among groups of its kind, best first
}

// groupOf ranks head and its comments as one: a row that is history
// (not listed for its comment) keeps its own score after the current
// hits; otherwise the group takes its best match (the row rises to its
// best comment), cut by staleRank when head is a stale fact (RankScore).
func groupOf(head Hit, comments []Hit) hitGroup {
	g := hitGroup{hits: append([]Hit{head}, comments...)}
	if head.History != "" && head.ViaComment == "" {
		g.history, g.rank = true, RankScore(head)
		return g
	}
	best := head.Score
	for _, c := range comments {
		best = max(best, c.Score)
	}
	g.rank = best
	if head.Stale != "" {
		g.rank = best * staleRank
	}
	return g
}

// parentTitle is what names a commented row on its comment's line: its
// title, else the start of its label or body.
func parentTitle(h Hit) string {
	for _, s := range []string{h.Title, h.Body, h.Fallback} {
		if s = strings.Join(strings.Fields(s), " "); s != "" {
			return TruncBody(s, titleMax)
		}
	}
	return ""
}

// commentLabel is the start of a comment hit's line: the row it is on
// (`on decision dec_… "Payments go through the gateway"`).
func commentLabel(c CommentOn) string {
	kind := c.Entity
	if kind == "" {
		kind = c.Table
	}
	s := "on " + kind + " " + c.ID
	if c.Title != "" {
		s += " " + `"` + c.Title + `"`
	}
	return s
}

// commentNote marks a comment hit on its line: it is history, read the
// row above it for what holds now.
const commentNote = "[comment: history]"
