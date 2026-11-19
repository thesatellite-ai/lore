package wire

import (
	"errors"
	"strings"
	"testing"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// TestOrgPushReportSentCountsRows (devco:D-527): a waiting org store clone
// that sync pushed reports the rows it sent, not the push's commits, so a
// push of store link commits with no note rows says "sent 0" with the
// "pushed local commit(s) with no note rows" note, as every store line of
// a sync does; store push alone counts commits (D-517).
func TestOrgPushReportSentCountsRows(t *testing.T) {
	t.Parallel()
	r := orgPushReport("client-store", opsstores.PushOut{Store: "client-store", Sent: 2, Rows: 0}, storeNowPush{}, nil)
	if r.Sent != 0 || !r.Pushed || r.Offline {
		t.Fatalf("store link push: sent %d pushed %v offline %v, want sent 0 pushed true", r.Sent, r.Pushed, r.Offline)
	}
	line := storeCountsLine(r.Store, storeStatusOf(r.Sent, 0, 0, 0, r.Pushed), syncCounts{sent: r.Sent})
	if want := "store client-store: pushed (sent 0, received 0, merged 0, refused 0)"; line != want {
		t.Fatalf("line %q, want %q", line, want)
	}
	if r = orgPushReport("acme/org", opsstores.PushOut{Sent: 1, Rows: 3}, storeNowPush{}, nil); r.Sent != 3 {
		t.Fatalf("rows pushed: sent %d, want the 3 row files", r.Sent)
	}
	if r = orgPushReport("acme/org", opsstores.PushOut{Sent: 1, Offline: true}, storeNowPush{}, nil); r.Pushed || !r.Offline {
		t.Fatalf("offline push: pushed %v offline %v", r.Pushed, r.Offline)
	}
	if r = orgPushReport("acme/org", opsstores.PushOut{}, storeNowPush{}, errors.New("refused")); r.Pushed || len(r.Problems) != 1 {
		t.Fatalf("failed push: pushed %v problems %v", r.Pushed, r.Problems)
	}
}

// TestOrgPushReportCountsCommitsD684: a sync that pushes a waiting org
// store clone (a client repo outside every setup's folders) says the
// commits git moved: the push merged a teammate's 2 commits first and sent
// 8 (7 and the merge), never "sent 4, received 0" (the rows sent).
func TestOrgPushReportCountsCommitsD684(t *testing.T) {
	r := orgPushReport("acme", opsstores.PushOut{Store: "acme", Sent: 8, Rows: 4}, storeNowPush{sentCommits: 8, receivedCommits: 2, received: 2}, nil)
	if r.SentCommits != 8 || r.ReceivedCommits != 2 || r.Received != 2 {
		t.Fatalf("[devco:D-684] org push report = %+v, want 8 commits sent and 2 received", r)
	}
	line := storeCountsLine("acme", storeStatusOf(r.Sent, r.Received, r.Merged, r.Refused, r.Pushed), syncCounts{r.Sent, r.SentCommits, r.Received, r.ReceivedCommits, r.Merged, r.Refused})
	if !strings.Contains(line, "pulled and pushed (sent 8, received 2,") {
		t.Fatalf("[devco:D-684] line = %q, want the commits git moved", line)
	}
}

// TestStoreLineNoNoteRowsNamesCommitsD698: a sync that pushes commits with
// no note row (a waiting store link, a set-url move of lore-org.yaml) says
// how many commits went out, as the stderr push line "(2 commits)" does:
// "sent 2, …; pushed 2 local commits with no note rows". Owner use pass 5
// weak 4: after set-url, "pushed store …: commit … (1 commit)" was followed
// by "pushed (sent 0, …); pushed local commit(s) with no note rows" while
// every other line's "sent" counts commits. The D-527 note stays; the
// bare "rows: 0 sent" never shows (D-684-g5sync); a push with note rows
// keeps its commits and no note.
func TestStoreLineNoNoteRowsNamesCommitsD698(t *testing.T) {
	t.Parallel()
	const url = "git@example.test:acme-co/client-store.git"
	p := orgPushReport("client-store", opsstores.PushOut{Store: "client-store", Sent: 2, Rows: 0}, storeNowPush{sentCommits: 2}, nil)
	r, _ := waitingPushReport(url, p)
	if r.SentCommits != 2 {
		t.Fatalf("[devco:D-684] report sent_commits = %d, want the 2 commits git sent", r.SentCommits)
	}
	want := "store client-store: pushed (sent 2, received 0, merged 0, refused 0); pushed 2 local commits with no note rows (store links or org settings)"
	if got := syncStoreLines(r, "")[0]; got != want {
		t.Fatalf("[devco:D-698] line = %q, want %q", got, want)
	}
	if got := storeSyncLine("acme/org", opssync.StoreStatusPushed, syncCounts{sent: 0, sentCommits: 1}, true); got != "store acme/org: pushed (sent 1, received 0, merged 0, refused 0); pushed 1 local commit with no note rows (store links or org settings)" {
		t.Fatalf("[devco:D-698] code repo line = %q", got)
	}
	// no commit count (a backend without commits): the D-527 note alone
	if got := storeSyncLine("acme/org", opssync.StoreStatusPushed, syncCounts{}, true); got != "store acme/org: pushed (sent 0, received 0, merged 0, refused 0)"+syncPushedNoRows {
		t.Fatalf("[devco:D-527] uncounted line = %q", got)
	}
	p = orgPushReport("acme", opsstores.PushOut{Store: "acme", Sent: 8, Rows: 4}, storeNowPush{sentCommits: 8, receivedCommits: 2, received: 2}, nil)
	r, _ = waitingPushReport(url, p)
	if got := syncStoreLines(r, "")[0]; !strings.HasPrefix(got, "store acme: pulled and pushed (sent 8, received 2,") || strings.Contains(got, "no note rows") {
		t.Fatalf("[devco:D-684] line = %q, want the commits git moved and no no-rows note", got)
	}
}
