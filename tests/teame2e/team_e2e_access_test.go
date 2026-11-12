//go:build teame2e

package teame2e

// Canonical store urls in shared config, each machine's own access in its
// config.yaml (brief 3.6, 3.7). The team's lore-org.yaml names its stores
// at a host no machine reaches directly (git.example.invalid); each
// person's ~/.lore/config.yaml maps that canonical prefix to where their
// machine reaches it (here: the fake host's bare repositories on disk).
// Org create, import, refresh, a store write by short name and its push
// all go through the mapping (`git -c url.<via>.insteadOf=<url>` per call);
// a machine without the mapping cannot reach the store and doctor names
// the fix.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The canonical host the shared config names; nothing resolves it.
const (
	accessHost      = "git.example.invalid"
	accessTeamURL   = "git@" + accessHost + ":acme/notes.git"
	accessSecondURL = "git@" + accessHost + ":acme/lore-x.git"
)

// accessConfig is a config.yaml mapping the canonical host to the fake
// host's repositories on disk (via, a local path prefix).
func accessConfig(r *th.Remote) string {
	via := filepath.ToSlash(filepath.Dir(filepath.Dir(r.Dir))) + "/"
	return "access:\n  - url: \"git@" + accessHost + ":\"\n    via: \"" + via + "\"\n"
}

func TestAccess_CanonicalOrgStoreThroughMachineMapping(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("org", "create"), th.Cmd("org", "import"), th.Cmd("org", "refresh"), th.Cmd("store", "folder", "add"))
	notes := w.Remote("github.com/acme/notes")
	second := w.Remote("github.com/acme/lore-x")
	app := w.Remote("github.com/acme/app")

	alice := w.Human("alice", "alice@acme.example")
	alice.WriteHomeFile(".lore/config.yaml", accessConfig(notes))
	repo := alice.Clone(app, "app")
	repo.MustLore("init", "--non-interactive", "--name=app")
	repo.CommitAll("lore")
	repo.AdminPush("origin", "main")
	repo.MustLore("store", "init", "acme_notes", accessTeamURL)
	repo.MustLore("org", "create", "acme", "--store", "acme_notes", "--key", "human:alice@acme.example", "--name", "Alice", "--yes")
	orgFile, ok := notes.Read("refs/heads/main", "org/lore-org.yaml")
	if !ok || !strings.Contains(orgFile, accessTeamURL) {
		t.Fatalf("lore-org.yaml must name the canonical url:\n%s", orgFile)
	}

	// Bob's machine: the same mapping, nothing else.
	bob := w.Human("bob", "bob@acme.example")
	bob.WriteHomeFile(".lore/config.yaml", accessConfig(notes))
	out := filepath.Join(bob.Home, "work")
	bob.Lore(out, "org", "import", accessTeamURL, "--key", "human:bob@acme.example", "--name", "Bob", "--yes").OK(t).Says(t, "added org entry acme")

	// The team adds a second store to lore-org.yaml (canonical url); bob's
	// refresh reads it through the mapping.
	edit := alice.Clone(notes, "notes-edit")
	edit.WriteFile("org/lore-org.yaml", "name: acme\nstores:\n  - url: "+accessTeamURL+"\n    org_folder: org\n  - url: "+accessSecondURL+"\n")
	edit.CommitAll("a second store")
	edit.AdminPush("origin", "HEAD:main")
	bob.Lore(out, "org", "refresh", "acme").OK(t)
	cfg, err := os.ReadFile(bob.LorePath("config.yaml"))
	if err != nil || !strings.Contains(string(cfg), accessSecondURL) {
		t.Fatalf("refresh did not copy the second store (%v):\n%s", err, cfg)
	}

	// A store write by its short name, pushed through the mapping.
	bob.Lore(out, "store", "folder", "add", "lore-x", "client_a").OK(t).Says(t, "client_a in lore-x")
	if !slices.ContainsFunc(second.Files("refs/heads/main"), func(f string) bool { return strings.HasPrefix(f, "client_a/") }) {
		t.Fatalf("client_a was not pushed to the second store: %v", second.Files("refs/heads/main"))
	}
	bob.SetEnv("LORE_ORG", "acme") // the org in view: store list shows its stores
	list := bob.Lore(out, "store", "list").OK(t)
	if !slices.ContainsFunc(strings.Split(list.Stdout, "\n"), func(l string) bool {
		f := strings.Fields(l)
		return len(f) > 1 && f[0] == "lore-x" && f[1] == accessSecondURL
	}) {
		t.Fatalf("store list must name the second store lore-x, its url in the URL column:\n%s", list.Stdout)
	}

	// Without the mapping the canonical host is unreachable, and doctor
	// says how to map it.
	bob.WriteHomeFile(".lore/config.yaml", withoutKey(string(cfg), "access"))
	bob.Lore(out, "doctor").Says(t, "store_access", accessSecondURL, "cannot be reached", "access:", "insteadOf")
}

// withoutKey is config.yaml text without one top-level key's block.
func withoutKey(cfg, key string) string {
	var out []string
	skip := false
	for _, line := range strings.Split(cfg, "\n") {
		top := line != "" && line[0] != ' ' && line[0] != '#'
		if top {
			skip = strings.HasPrefix(line, key+":")
		}
		if !skip {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
