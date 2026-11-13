package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
)

// offlineSession is a live session with only what reportOffline reads:
// a state folder, a named remote and a stderr to capture.
func offlineSession(t *testing.T, url string) (*liveSession, *strings.Builder) {
	t.Helper()
	dir := t.TempDir()
	out := &strings.Builder{}
	s := &liveSession{
		h:         &liveHost{cfg: LiveConfig{Stderr: out}},
		stateDir:  dir,
		remoteURL: url,
		ref:       &live.Ref{RemoteName: "origin", StateDir: dir},
	}
	return s, out
}

// gitNoAnswer and gitNoRepo are git's stderr for a host that does not
// resolve and for a path with no repository.
const (
	gitAdviceTail = "fatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"
	gitNoAnswer   = "ssh: Could not resolve hostname git.example.invalid: Name or service not known\n" + gitAdviceTail
	gitNoRepo     = "fatal: '/srv/none/site.git' does not appear to be a git repository\n" + gitAdviceTail
)

// B3-13: the not-pushed line is said once per state, not on every command
// and twice per git commit; it says what happens next.
func TestLiveOfflineLineSaidOnce(t *testing.T) {
	s, out := offlineSession(t, "ssh://git.example.invalid/site.git")
	for range 3 { // three commands (or two hooks and a read) while offline
		s.reportOffline(gitNoAnswer)
	}
	got := out.String()
	if n := strings.Count(got, "not pushed yet"); n != 1 {
		t.Fatalf("said %d times, want once:\n%s", n, got)
	}
	if !strings.Contains(got, "offline: the next lore command sends them once origin (ssh://git.example.invalid/site.git) answers, or run `lore sync`") {
		t.Fatalf("the line does not say what happens next:\n%s", got)
	}
	if s.noteText(liveNoteFault) != "" {
		t.Fatal("no answer is not a fault for doctor")
	}

	s.clearWaitNotes() // a push went through: the next wait is said again
	s.reportOffline(gitNoAnswer)
	if n := strings.Count(out.String(), "not pushed yet"); n != 2 {
		t.Fatalf("after a delivered push the next wait must be said again:\n%s", out.String())
	}
}

// B3-14: a remote with no repository is named with its fix, never
// "offline", and doctor gets the reason; credentials never show.
func TestLiveMissingRemoteNamed(t *testing.T) {
	secret := "pw" + "98765"
	scheme := "https://" // built apart so the line holds no url-with-password shape
	s, out := offlineSession(t, scheme+"mira:"+secret+"@git.example.test/none/site.git")
	s.reportOffline(gitNoRepo)
	got := out.String()
	if strings.Contains(got, "offline") || !strings.Contains(got, "no git repository at origin ("+scheme+"mira:<redacted>@git.example.test/none/site.git): fix the address with `git remote set-url origin <url>`") {
		t.Fatalf("missing remote line:\n%s", got)
	}
	fault := s.noteText(liveNoteFault)
	if !strings.Contains(fault, "does not appear to be a git repository") || strings.Contains(fault+got, secret) {
		t.Fatalf("doctor note %q (line %q)", fault, got)
	}
	if s.waitWhy == "offline" {
		t.Fatal("sync would call a missing remote offline")
	}
}

// Under `lore sync` the line is recorded, not printed (sync prints its own).
func TestLiveOfflineSyncQuiet(t *testing.T) {
	s, out := offlineSession(t, "ssh://git.example.invalid/site.git")
	s.syncReport = true
	s.reportOffline(gitNoAnswer)
	s.syncReport = false
	s.reportOffline(gitNoAnswer) // finish's push after the sync
	if out.Len() != 0 || s.waitWhy != "offline" {
		t.Fatalf("sync printed the live line: %q (why %q)", out.String(), s.waitWhy)
	}
}

// B3-22: an origin folder that exists but cannot be read is named as such
// (permissions), never "fix the address".
func TestLiveUnreadableRemoteNamed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	locked := filepath.Join(t.TempDir(), "site.git")
	if err := os.Mkdir(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	s, out := offlineSession(t, locked)
	s.reportOffline("fatal: '" + locked + "' does not appear to be a git repository\n" + gitAdviceTail)
	got := out.String()
	if !strings.Contains(got, "cannot read origin ("+locked+") (permission denied): the address is right") || strings.Contains(got, "set-url") {
		t.Fatalf("unreadable remote line:\n%s", got)
	}
}

// A host that refuses refs/lore/*: the refusal line is said once per state
// like the offline one (a sync used to print it twice, once for its own
// push and again at the end of the run), never during sync's own report,
// and the session keeps the host's words for sync's line.
func TestLiveRefusedLineSaidOnce(t *testing.T) {
	s, out := offlineSession(t, "https://git.example.test/acme/site.git")
	refused := live.PushOutcome{Status: live.PushRefused, Detail: "[remote rejected] (pre-receive hook declined)"}
	s.syncReport = true
	s.reportPush(refused)
	if got := out.String(); strings.Contains(got, "not pushed yet") {
		t.Fatalf("sync's report says it in its own line; the session must not:\n%s", got)
	}
	if s.waitWhy != liveRefusedWhy || s.refusedDetail != refused.Detail {
		t.Fatalf("waitWhy %q detail %q: sync's line needs both", s.waitWhy, s.refusedDetail)
	}
	s.syncReport = false
	for range 3 { // the finish push of the same run, then later commands
		s.reportPush(refused)
	}
	if n := strings.Count(out.String(), "the remote refused refs/lore/live"); n != 0 {
		t.Fatalf("already said during sync: said %d more times:\n%s", n, out.String())
	}
}
