package wire

import (
	"strings"
	"testing"
)

// TestStoreGone (H-11): a sync whose remote answered "no repository" is
// not "offline: check the network": it names set-url for a moved store
// and the restore for a deleted one; no answer stays offline.
func TestStoreGone(t *testing.T) {
	t.Parallel()
	const raw = "git@git.example.test:acme/team-store.git"
	missing := "fatal: '/srv/remotes/team-store.git' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"
	cause, next, gone := storeGone(missing, raw, "acme")
	if !gone {
		t.Fatal("a missing repository was read as offline")
	}
	for _, want := range []string{"no git repository at " + raw, "moved, was renamed or deleted", "local changes are kept"} {
		if !strings.Contains(cause, want) {
			t.Errorf("cause %q lacks %q", cause, want)
		}
	}
	for _, want := range []string{"lore store set-url --name acme --url <new-url>", "create an empty repository at " + raw, "lore store push --store acme --restore --yes"} {
		if !strings.Contains(next, want) {
			t.Errorf("next %q lacks %q", next, want)
		}
	}
	if strings.Contains(cause+next, "network") {
		t.Errorf("a missing repository blames the network: %q / %q", cause, next)
	}
	if _, _, gone := storeGone("ssh: Could not resolve hostname git.example.test: Name or service not known\n", raw, "acme"); gone {
		t.Error("no answer (DNS) was read as gone")
	}
	if _, _, gone := storeGone("", raw, "acme"); gone {
		t.Error("no git text was read as gone")
	}
	if cause, _, gone := storeGone("git@git.example.test: Permission denied (publickey).\n", raw, "acme"); !gone || !strings.Contains(cause, "refused this machine's credentials") {
		t.Errorf("refused credentials: gone=%v cause=%q", gone, cause)
	}
}
