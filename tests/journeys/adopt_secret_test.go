//go:build journeys && !windows

package journeys

// adopt_secret_test.go — devco:D-717 (security): `lore store adopt` copied
// a repo's rows into a store folder with no secret scan at all, so
// key-shaped values (one a real-shaped staging API key) went into the
// store copy, and --secret-unstored / --not-a-secret / --secret-ref had no
// effect; only an outside scanner caught them. Lee adopts a repo whose
// row files hold two keys (put there by hand, as an old lore or an editor
// would): the adopt is refused naming both rows and the reruns, the store
// gets nothing; each printed answer then does what it says, and the
// store's git history never holds a key.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	jAsSlug = "github.com/acme-co/keys-app"
	jAsWord = "zerkelmoth"
)

func TestJourneyAdoptRunsTheSecretScan(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	// built at run time: no key-shaped literal in the repo
	keyA := "AKIA" + strings.Repeat("Q", 4) + "7XKD2M4N5P6R"
	keyB := "AKIA" + strings.Repeat("Z", 4) + "3WJH8B9C2V4T"
	repo := jPlainRepo(t, lee, w.Remote(jAsSlug), filepath.Join(parent, "keys-app"))
	var idA, idB string

	// memID is the id of the repo's memory whose body holds word.
	memID := func(c *teamharness.Clone, word string) string {
		t.Helper()
		paths, _ := filepath.Glob(filepath.Join(c.Dir, ".lore", "data", "memories", "*.json"))
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), word) {
				return strings.TrimSuffix(filepath.Base(p), ".json")
			}
		}
		t.Fatalf("no memory holds %q", word)
		return ""
	}
	// plant swaps word for key in the memory file by hand (no lore write).
	plant := func(c *teamharness.Clone, id, word, key string) {
		t.Helper()
		rel := ".lore/data/memories/" + id + ".json"
		body, _ := c.ReadFile(rel)
		c.WriteFile(rel, strings.Replace(body, word, key, 1))
	}
	adopt := func(folder string, extra ...string) teamharness.Result {
		return lee.LoreIn(repo, append([]string{"store", "adopt", "--target", tlOrg + "/" + folder, "--yes"}, extra...)...)
	}
	folderFiles := func(folder string) []string {
		return tt.store.FilesMatching("refs/heads/main", folder+"/memories/")
	}

	w.Step("day1/a-repo-whose-rows-hold-two-keys", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		for _, f := range []string{"keys_unstored", "keys_ref", "keys_allowed"} {
			lee.Lore(parent, "store", "folder", "add", "team", f).OK(t)
		}
		lee.LoreIn(repo, "init", "--non-interactive", "--name=keys-app").OK(t)
		lee.LoreIn(repo, "memory", "add", "--body", "staging token "+jAsWord+"a", "--to", "repo").OK(t)
		lee.LoreIn(repo, "memory", "add", "--body", "deploy token "+jAsWord+"b", "--to", "repo").OK(t)
		idA, idB = memID(repo, jAsWord+"a"), memID(repo, jAsWord+"b")
		plant(repo, idA, jAsWord+"a", keyA)
		plant(repo, idB, jAsWord+"b", keyB)
	})

	w.Step("day1/adopt-refused-naming-both-rows-nothing-written", func() {
		r := adopt("keys_unstored").Refused(t, "E_NEEDS_CONFIRM", idA, idB, "nothing written",
			"lore store adopt --target "+tlOrg+"/keys_unstored --yes --secret-unstored",
			"lore store adopt --target "+tlOrg+"/keys_unstored --yes --not-a-secret --reason")
		r.Never(t, keyA, keyB)
		adopt("keys_unstored", "--dry-run").Refused(t, idA, idB).Never(t, keyA, keyB)
		lee.Lore(parent, "sync").OK(t)
		if got := folderFiles("keys_unstored"); len(got) != 0 {
			t.Fatalf("a refused adopt wrote rows into the store: %v", got)
		}
	})

	w.Step("day1/secret-unstored-redacts-both", func() {
		adopt("keys_unstored", "--secret-unstored").OK(t).Says(t, "memories 2").Never(t, keyA, keyB)
		lee.Lore(parent, "sync").OK(t)
		got := folderFiles("keys_unstored")
		if len(got) != 2 {
			t.Fatalf("the folder holds %d memories, want 2: %v", len(got), got)
		}
		for _, p := range got {
			body, _ := tt.store.Read("refs/heads/main", p)
			if !strings.Contains(body, "${secret:unstored}") {
				t.Errorf("%s is not redacted:\n%s", p, body)
			}
		}
		// devco:D-762: each redacted row gets its store-then-rotate task in
		// the folder, as the flag's help says
		tasks := ""
		for _, p := range tt.store.FilesMatching("refs/heads/main", "keys_unstored/tasks/") {
			body, _ := tt.store.Read("refs/heads/main", p)
			tasks += body
		}
		for _, id := range []string{idA, idB} {
			if !strings.Contains(tasks, "secret-unstored-"+id) {
				t.Errorf("no store-then-rotate task for %s in the folder:\n%s", id, tasks)
			}
		}
	})

	w.Step("day1/secret-ref-one-per-finding", func() {
		adopt("keys_ref", "--secret-ref", "op://vault/a", "--secret-ref", "op://vault/b", "--secret-ref", "op://vault/c", "--dry-run").
			Refused(t, "2 findings but 3 --secret-ref values")
		adopt("keys_ref", "--secret-ref", "op://vault/staging", "--secret-ref", "op://vault/deploy").OK(t).Never(t, keyA, keyB)
		lee.Lore(parent, "sync").OK(t)
		all := ""
		for _, p := range folderFiles("keys_ref") {
			body, _ := tt.store.Read("refs/heads/main", p)
			all += body
		}
		for _, ref := range []string{"op://vault/staging", "op://vault/deploy"} {
			if !strings.Contains(all, ref) {
				t.Errorf("the folder does not hold the reference %s:\n%s", ref, all)
			}
		}
	})

	w.Step("day1/not-a-secret-keeps-them-with-allow-entries", func() {
		adopt("keys_allowed", "--not-a-secret", "--reason", "made-up fixture keys").OK(t).Says(t, "memories 2")
		lee.Lore(parent, "sync").OK(t)
		if got := folderFiles("keys_allowed"); len(got) != 2 {
			t.Fatalf("the folder holds %d memories, want 2: %v", len(got), got)
		}
		if allows := tt.store.FilesMatching("refs/heads/main", "keys_allowed/secret_allows/"); len(allows) < 2 {
			// one per (field, rule, value), as a write records them
			t.Errorf("want an allow entry for each kept key in the folder, got %v", allows)
		}
	})

	w.Step("day1/the-history-of-the-unstored-and-ref-folders-holds-no-key-but-the-allowed-one", func() {
		// only keys_allowed was told to keep the values; its two rows are
		// the only objects that may hold them
		hits := tt.store.AllObjectsContain(keyA, keyB)
		if len(hits) > 2 {
			t.Errorf("the store's history holds a key in %d objects, want only the 2 kept rows: %v", len(hits), hits)
		}
	})
}
