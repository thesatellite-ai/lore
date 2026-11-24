package ask

// Comments are history (devco D-942, hunt H-191, feature F-3): a comment
// (cmt_…) is what someone said about a row at one time. ask never answers
// with a comment: a row whose comment holds the question's words answers
// as the row (its own words count more), and the comments that hold them
// are printed under it as history and cited after it. Before this, ask
// read no comment at all, so "why did we add backoff" found nothing when
// only the comment on the decision said it.

import (
	"context"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
)

// Comment is a comment on an answer row that holds a word of the
// question, printed under the row as history.
type Comment struct {
	ID   string `json:"id"`
	At   string `json:"at,omitempty" desc:"the day it was written"`
	Body string `json:"body"`
}

// rowComment is a comment as the lens reads it: lower is its lowered
// body, for the rowFilter's patterns; words is its word set (tokensOf).
type rowComment struct {
	id, body, lower string
	at              time.Time
	words           map[string]bool
}

// commentsSQL reads the comments on the rows of the text tables. It names
// nothing of the question, so the read server keeps its answer like
// textSQL's (FileOnly).
var commentsSQL = func() string {
	tables := make([]string, len(textTables))
	for i, t := range textTables {
		tables[i] = sqlText(t.table)
	}
	return "SELECT c.id, c.entity_id, COALESCE(c.body, ''), COALESCE(c.created_at, '') FROM comments_all c WHERE c.entity_table IN (" + strings.Join(tables, ", ") + ") ORDER BY c.created_at, c.id"
}()

// loadComments reads the comments on text rows, by the id of the row
// they are on, oldest first.
func loadComments(ctx context.Context, run Query) (map[string][]rowComment, error) {
	res, err := run(ctx, commentsSQL)
	if err != nil {
		return nil, err
	}
	out := map[string][]rowComment{}
	for _, line := range res.Rows {
		if len(line) < 4 {
			continue
		}
		body := cell(line[2])
		at, _ := time.Parse(time.RFC3339Nano, cell(line[3]))
		on := cell(line[1])
		out[on] = append(out[on], rowComment{id: cell(line[0]), body: body, lower: likeText(body), at: at, words: tokensOf(body)})
	}
	return out, nil
}

// commentsHold reports whether one of cs holds one of pats (the
// rowFilter's lowered words): its row can answer through it.
func commentsHold(cs []rowComment, pats []string) bool {
	for _, c := range cs {
		for _, p := range pats {
			if strings.Contains(c.lower, p) {
				return true
			}
		}
	}
	return false
}

// commentWord reports whether a comment of r holds w (a form of it), and
// marks each comment that does in hit.
func commentWord(r *textRow, w string, hit map[string]bool) bool {
	held := false
	for _, c := range r.comments {
		if hasWord(c.words, w) {
			hit[c.id], held = true, true
		}
	}
	return held
}

// commentsOf is the comments of r that hit marks, oldest first, as the
// answer prints them.
func commentsOf(r *textRow, hit map[string]bool) []Comment {
	var out []Comment
	for _, c := range r.comments {
		if !hit[c.id] {
			continue
		}
		cm := Comment{ID: c.id, Body: c.body}
		if !c.at.IsZero() {
			cm.At = c.at.UTC().Format(dates.DayLayout)
		}
		out = append(out, cm)
	}
	return out
}

// commentMax caps a comment's text under its row.
const commentMax = 200

// commentLines are r's comments as the text answer prints them under the
// row: one line each, marked as history.
func (r Row) commentLines() string {
	var b strings.Builder
	for _, c := range r.Comments {
		text := strings.Join(strings.Fields(c.Body), " ")
		if len(text) > commentMax {
			// the sentences holding the question's words first (D-970),
			// on one line
			text = strings.Join(strings.Fields(cutText(text, r.Matched, commentMax)), " ")
		}
		day := ""
		if c.At != "" {
			day = " " + c.At
		}
		b.WriteString("   comment " + c.ID + day + " (history): " + text + "\n")
	}
	return b.String()
}
