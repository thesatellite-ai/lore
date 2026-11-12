package signals

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
)

// TestOfflineErrorIsOneLine: an unreachable remote is named by the git
// operation and git's first line, never git's whole advice block nor the
// "did not confirm the push" sentinel for a fetch (team docs errors.txt,
// offline claim).
func TestOfflineErrorIsOneLine(t *testing.T) {
	t.Parallel()
	stderr := "fatal: '/srv/notes' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"
	e := &OfflineError{Reason: "remote unreachable", Err: &gitremote.TransportError{Op: "fetch", Err: gitremote.ErrUnconfirmed, Stderr: stderr}}
	got := e.Error()
	want := "could not confirm the coordination write (remote unreachable): git fetch: '/srv/notes' does not appear to be a git repository"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if !errors.Is(e, ErrOffline) || e.AutopushOff() {
		t.Fatalf("errors.Is ErrOffline / AutopushOff wrong for %q", got)
	}
	if off := (&OfflineError{Reason: "autopush is off (live.autopush / LORE_LIVE_AUTOPUSH=0)"}); !off.AutopushOff() {
		t.Fatal("autopush off not recognised")
	}
	long := &OfflineError{Reason: "remote unreachable", Err: errors.New(strings.Repeat("x", 500) + "\nmore")}
	if l := long.Error(); strings.Contains(l, "\n") || len(l) > 300 {
		t.Fatalf("a long cause is not cut to one short line: %d bytes", len(l))
	}
}
