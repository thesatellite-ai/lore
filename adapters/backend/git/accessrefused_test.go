package git

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// TestAccessRefusedClassifier: which failed-fetch answers count as "the
// remote refused this machine" (wrong key, no key, a repo the account cannot
// see) rather than "the remote never answered". The fetch wraps git's error
// in errors.Join with errOffline, so every case is checked bare and wrapped.
func TestAccessRefusedClassifier(t *testing.T) {
	t.Parallel()
	notFound := "ERROR: Repository not found.\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists."
	cases := map[string]struct {
		stderr string
		want   bool
	}{
		"ssh no key accepted":          {"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", true},
		"ssh key of another account":   {notFound, true},
		"github https repo not found":  {"remote: Repository not found.\nfatal: repository 'https://github.com/octo-work/lore-me.git/' not found", true},
		"https bad credentials":        {"fatal: Authentication failed for 'https://github.com/octo-work/lore-me.git/'", true},
		"https forbidden":              {"fatal: unable to access 'https://github.com/octo-work/lore-me.git/': The requested URL returned error: 403", true},
		"dns failure":                  {"ssh: Could not resolve hostname github.com: Name or service not known\nfatal: Could not read from remote repository.", false},
		"connection timed out":         {"ssh: connect to host github.com port 22: Connection timed out\nfatal: Could not read from remote repository.", false},
		"local path missing":           {"fatal: '/tmp/absent.git' does not appear to be a git repository\nfatal: Could not read from remote repository.", false},
		"empty stderr":                 {"", false},
		"repo text without the prefix": {"note: Repository not found in cache", false},
	}
	for name, c := range cases {
		ge := &gitError{cmd: "fetch", code: 128, stderr: c.stderr}
		wrapped := errs.Wrap(errs.Offline, "git: fetch the store", errors.Join(errOffline, ge))
		for form, err := range map[string]error{"bare": ge, "wrapped": wrapped} {
			if got := accessRefused(err); got != c.want {
				t.Errorf("%s (%s): accessRefused = %v, want %v", name, form, got, c.want)
			}
		}
	}
	if accessRefused(errors.New("git never ran")) {
		t.Error("an error that is not a git run counts as refused")
	}
}

// TestFirstOpenAccessRefused opens a store clone whose ssh answers like
// GitHub does to a wrong or missing key, or never answers: the refusal is
// E_STORE_UNREACHABLE with the access message (no "go online", no raw ssh
// text in the message), the silent remote keeps the offline message.
func TestFirstOpenAccessRefused(t *testing.T) {
	t.Parallel()
	const remote = "git@github.com:octo-work/lore-me.git"
	cases := map[string]struct {
		sshStderr  string
		wantMsg    string
		wantNoText string
	}{
		"wrong key": {"ERROR: Repository not found.", msgAccessRefused, "Repository not found"},
		"no key":    {"git@github.com: Permission denied (publickey).", msgAccessRefused, "Permission denied"},
		"offline":   {"ssh: Could not resolve hostname github.com: Name or service not known", "git: the store has no local copy yet and cannot be reached", "resolve hostname"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			// the shared fake ssh (TestMain) prints this case's answer from its environment
			route := WithRoute(func(context.Context, string) ([]string, []string) {
				return nil, []string{"GIT_SSH_COMMAND=" + refusingSSHBin, sshStderrEnv + "=" + c.sshStderr}
			})
			_, err := OpenClone(t.Context(), remote, filepath.Join(dir, "c"), rowfs.FolderRoot, store.OpenOptions{}, route)
			if !errs.Has(err, errs.StoreUnreachable) {
				t.Fatalf("err = %v, want E_STORE_UNREACHABLE", err)
			}
			msg := errs.EnvelopeOf(err).Error.Message
			if msg != c.wantMsg {
				t.Errorf("message = %q, want %q", msg, c.wantMsg)
			}
			if strings.Contains(msg, c.wantNoText) {
				t.Errorf("message quotes ssh's own text %q: %q", c.wantNoText, msg)
			}
		})
	}
}
