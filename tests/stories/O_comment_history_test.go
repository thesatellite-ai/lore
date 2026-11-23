//go:build stories && !windows

package stories

// O_comment_history_test.go — story O, devco D-942 (hunt H-191, feature
// F-3): on the owner-shaped laptop a decision sits in a store folder and a
// teammate's later comment on it holds more of a search's words than the
// decision does. A comment is history (what someone said then), yet
// search ranked it above the decision with no label, and ask never read
// comments at all. Now search lists a comment directly under the row it
// is on, labelled with that row and as history (the row listed first when
// only the comment matched), and ask answers with the row, the comment
// printed under it as history and cited after it.

import (
	"path/filepath"
	"strings"
	"testing"
)

// printedID is the id printed after `"id": "<prefix>` in out.
func printedID(t *testing.T, out, prefix string) string {
	t.Helper()
	i := strings.Index(out, `"id": "`+prefix)
	if i < 0 {
		t.Fatalf("no %s id in:\n%s", prefix, out)
	}
	return out[i+len(`"id": "`) : i+len(`"id": "`)+36]
}

func TestStoryOCommentIsHistoryUnderItsRow(t *testing.T) {
	s := New(t, "O")
	ledger := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/ledger-app"), func(dir string) { PlainRepo(t, dir) })
	kb := s.Remote("github.com/" + oAdminPersonal.Login + "/kb")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	lc := ada.CloneTo(ledger, filepath.Join(ada.Home, "src", "ledger-app"))
	var dec, cmt string

	s.Step("D-942/decision-and-a-later-comment", func() {
		ada.Lore(lc.Dir, "init", "--non-interactive", "--name=ledger-app").OK(t)
		ada.Lore(lc.Dir, "store", "init", "--name", "kb", "--url", kb.SSHURL(), "--yes").OK(t)
		ada.Lore(lc.Dir, "memory", "add", "--to", "kb/app", "--create", "--body", "Ledger exports run nightly.", "--yes").OK(t)
		dec = printedID(t, ada.Lore(lc.Dir, "decision", "add", "--to", "kb/app", "--title", "Payments go through the billing gateway", "--body", "We route every charge via the gateway service.", "--json", "--yes").OK(t).Stdout, "dec_")
		cmt = printedID(t, ada.Lore(lc.Dir, "comment", "add", "--to", "kb/app", "--on-id", dec, "--body", "Retry storms on the gateway in March: we added backoff for charges.", "--json", "--yes").OK(t).Stdout, "cmt_")
	})

	s.Step("D-942/search-lists-the-comment-under-its-decision", func() {
		out := ada.Lore(lc.Dir, "search", "--query", "gateway backoff").OK(t).Says(t, dec, cmt, `on decision `+dec+` "Payments go through the billing gateway"`, "[comment: history]").Stdout
		if strings.Index(out, dec+"  ") > strings.Index(out, cmt) {
			t.Fatalf("the comment is listed above the decision it is on:\n%s", out)
		}
		ada.Lore(lc.Dir, "search", "--query", "gateway backoff").OK(t).Never(t, "No row holds every word")
	})

	s.Step("D-942/only-the-comment-matches-the-decision-comes-first", func() {
		out := ada.Lore(lc.Dir, "search", "--query", "backoff").OK(t).Says(t, "(listed for its comment "+cmt+")").Stdout
		if strings.Index(out, dec) > strings.Index(out, cmt) {
			t.Fatalf("the decision is not first:\n%s", out)
		}
		ada.Lore(lc.Dir, "search", "--query", "backoff", "--json").OK(t).Says(t, `"via_comment": "`+cmt+`"`, `"comment_on": {`, `"entity": "decision"`)
		// only comments searched: the decision is not listed, the comment still names it
		ada.Lore(lc.Dir, "search", "--query", "backoff", "--tables", "comment").OK(t).Says(t, cmt, `on decision `+dec).Never(t, "listed for its comment")
	})

	s.Step("D-942/ask-answers-with-the-decision-the-comment-as-history", func() {
		out := ada.Lore(lc.Dir, "ask", "--query", "why did we add backoff").OK(t).Says(t, "Payments go through the billing gateway", "comment "+cmt, "(history): Retry storms").Stdout
		if strings.Index(out, "Payments go through") > strings.Index(out, "comment "+cmt) {
			t.Fatalf("ask printed the comment above its row:\n%s", out)
		}
		ada.Lore(lc.Dir, "ask", "--query", "why did we add backoff", "--json").OK(t).Says(t, `"comments": [`, `"id": "`+cmt+`"`)
	})
}
