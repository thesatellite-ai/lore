package signals

import (
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
)

// TestRemoteMissing (owner Mac run 2, item 34): git's own message tells a
// remote that does not exist (hosting "Repository not found", a path with
// no repository, https's "repository '<url>' not found") from one that
// could not be reached or refused this machine's key; only the first is
// "missing", wrapped in an OfflineError or not.
func TestRemoteMissing(t *testing.T) {
	t.Parallel()
	const advice = "fatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"
	cases := []struct {
		stderr string
		want   bool
	}{
		{"ERROR: Repository not found.\n" + advice, true},
		{"remote: Repository not found.\nfatal: repository 'https://example.com/o/gone.git/' not found\n", true},
		{"fatal: repository 'https://example.com/o/gone.git/' not found\n", true},
		{"fatal: '/srv/gone' does not appear to be a git repository\n" + advice, true},
		{"git@example.com: Permission denied (publickey).\n" + advice, false},
		{"ssh: Could not resolve hostname example.com: nodename nor servname provided\n" + advice, false},
		{"fatal: unable to access 'https://example.com/o/r.git/': Could not resolve host: example.com\n", false},
		{"fatal: Authentication failed for 'https://example.com/o/r.git/'\n", false},
		{"ssh: connect to host example.com port 22: Connection refused\n" + advice, false},
	}
	for _, tc := range cases {
		te := &gitremote.TransportError{Op: "fetch", Err: gitremote.ErrUnconfirmed, Stderr: tc.stderr}
		if got := RemoteMissing(te); got != tc.want {
			t.Errorf("RemoteMissing(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
		off := &OfflineError{Reason: "remote unreachable", Err: te}
		if got := RemoteMissing(off); got != tc.want {
			t.Errorf("RemoteMissing(OfflineError %q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
	if RemoteMissing(errors.New("Repository not found")) || RemoteMissing(&OfflineError{Reason: "autopush is off"}) {
		t.Error("only a git transport error's stderr decides")
	}
}
