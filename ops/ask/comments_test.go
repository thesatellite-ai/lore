package ask

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// txtCell is s as a lens cell.
func txtCell(s string) value.Value { v, _ := value.TextOf(s); return v }

// textLine is a text statement line of a row (textCols cells).
func textLine(table, id, title, body, at string) []value.Value {
	return []value.Value{txtCell("row"), txtCell(table), txtCell(id), txtCell("me/work"), txtCell(title), txtCell(body), txtCell(at), txtCell("0"), txtCell(""), txtCell(""), txtCell(strings.ToLower(title + " " + body))}
}

// commentLine is a comments statement line.
func commentLine(id, on, body, at string) []value.Value {
	return []value.Value{txtCell(id), txtCell(on), txtCell(body), txtCell(at)}
}

// TestCommentAnswersAsItsRow (devco D-942, H-191): a question only a
// comment holds the words of is answered by the row the comment is on;
// the comment is printed under it as history and cited after it, never
// as a row of its own.
func TestCommentAnswersAsItsRow(t *testing.T) {
	t.Parallel()
	env := &fakeEnv{
		rows: [][]value.Value{
			textLine("decisions", "dec_1", "Payments go through the billing gateway", "We route every charge via the gateway service.", "2026-11-01T00:00:00Z"),
		},
		comments: [][]value.Value{
			commentLine("cmt_1", "dec_1", "Retry storms in March: we added backoff for charges.", "2026-11-03T00:00:00Z"),
			commentLine("cmt_2", "dec_1", "Reviewed by the payments team.", "2026-11-04T00:00:00Z"),
		},
	}
	out, err := run(context.Background(), env, RunIn{Query: "why did we add backoff"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].ID != "dec_1" {
		t.Fatalf("rows: %+v", out.Rows)
	}
	if got := out.Rows[0].Comments; len(got) != 1 || got[0].ID != "cmt_1" || got[0].At != "2026-11-03" {
		t.Fatalf("comments under the row: %+v (only the comment holding a word, cmt_2 holds none)", got)
	}
	if !slices.Equal(out.Cited, []string{"dec_1", "cmt_1"}) {
		t.Fatalf("cited: %v", out.Cited)
	}
	text := out.Text()
	if !strings.Contains(text, "comment cmt_1 2026-11-03 (history): Retry storms") || strings.Contains(text, "cmt_2") {
		t.Fatalf("text:\n%s", text)
	}
	if strings.Index(text, "Payments go through") > strings.Index(text, "cmt_1") {
		t.Fatalf("the comment is printed above its row:\n%s", text)
	}
	b, err := json.Marshal(out.Rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"comments":[{"id":"cmt_1"`) {
		t.Fatalf("json: %s", b)
	}
}

// TestCommentWordsRankBelowOwnWords (D-942): a row holding the
// question's word itself ranks above a row only a comment on it makes
// answer.
func TestCommentWordsRankBelowOwnWords(t *testing.T) {
	t.Parallel()
	env := &fakeEnv{
		rows: [][]value.Value{
			textLine("decisions", "dec_old", "Payments go through the billing gateway", "We route every charge via the gateway.", "2026-11-05T00:00:00Z"),
			textLine("decisions", "dec_new", "Charges retry with backoff", "Every failed charge retries with backoff.", "2026-10-01T00:00:00Z"),
		},
		comments: [][]value.Value{
			commentLine("cmt_1", "dec_old", "backoff backoff backoff", "2026-11-06T00:00:00Z"),
		},
	}
	out, err := run(context.Background(), env, RunIn{Query: "backoff"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 2 || out.Rows[0].ID != "dec_new" || out.Rows[1].ID != "dec_old" {
		t.Fatalf("rows: %+v", out.Rows)
	}
	if len(out.Rows[0].Comments) != 0 || len(out.Rows[1].Comments) != 1 {
		t.Fatalf("comments: %+v", out.Rows)
	}
}
