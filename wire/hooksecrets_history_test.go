package wire_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// commitWhere is the commit check's where (wire's checkWhereCommit).
const commitWhere = "commit"

// histCommit commits everything in repo without hooks, as a fixed person.
func histCommit(t *testing.T, repo, msg string) {
	t.Helper()
	allowGit(t, repo, "add", "-A")
	allowGit(t, repo, "-c", "user.name=Test Person", "-c", "user.email=test@example.test", "commit", "-q", "--no-verify", "-m", msg)
}

// TestCommitCheckBlocksOnlyAddedSecrets (D2, bug B1-14): a secret-looking
// value the same row already held at HEAD does not block a commit (a format
// rewrite, the row file moved), nor one the same file held at HEAD; a value
// the commit adds, in that row, in another row or in that file, still does.
// Break-verified: with the HEAD comparison disabled (dropHistoryHits
// returning hits unchanged) the "already in history" cases fail; with every
// finding treated as old, the "new" cases fail.
func TestCommitCheckBlocksOnlyAddedSecrets(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time, never a literal key
	other := "AKIA" + strings.Repeat("Z", 16)
	memID := h.ID(h.Must("memory.add", `{"body":"target"}`))
	otherID := h.ID(h.Must("memory.add", `{"body":"elsewhere"}`))
	memFile := allowRowFile(t, h.Repo, "memories", memID)
	otherFile := allowRowFile(t, h.Repo, "memories", otherID)
	rel := func(p string) string {
		r, err := filepath.Rel(h.Repo, p)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(r)
	}
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	committed := strings.Replace(read(memFile), `"target"`, `"rotate `+key+`"`, 1)
	if err := os.WriteFile(memFile, []byte(committed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Repo, "notes.md"), []byte("old line https://example.test "+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	histCommit(t, h.Repo, "history with a key")

	check := func(docs map[string][]byte) error {
		return h.App.HookSecrets(ctx, h.Repo, commitWhere, docs)
	}
	reformatted := strings.ReplaceAll(committed, `": `, `":  `) // the same row, another layout
	if reformatted == committed {
		t.Fatal("the reformat changed nothing")
	}
	moved := ".lore/data/memories/old/" + filepath.Base(memFile)

	old := map[string]map[string][]byte{
		"same row, new format":  {rel(memFile): []byte(reformatted)},
		"same row, moved file":  {moved: []byte(committed)},
		"same file, a new line": {"notes.md": []byte("old line https://example.test " + key + "\nnew line\n")},
	}
	for name, docs := range old {
		if err := check(docs); err != nil {
			t.Errorf("%s: err = %v, want pass (the value is already in history)", name, err)
		}
	}
	added := map[string]map[string][]byte{
		"a second key in that row": {rel(memFile): []byte(strings.Replace(committed, key, key+" and "+other, 1))},
		"that key in another row":  {rel(otherFile): []byte(strings.Replace(read(otherFile), `"elsewhere"`, `"copy `+key+`"`, 1))},
		"that key in another file": {"other.md": []byte(key + "\n")},
		"a new key in that file":   {"notes.md": []byte("old line https://example.test " + key + "\n" + other + "\n")},
	}
	for name, docs := range added {
		err := check(docs)
		if !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("%s: err = %v, want E_LEAK_BLOCKED", name, err)
			continue
		}
		if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), other) {
			t.Errorf("%s: the refusal shows the value: %v", name, err)
		}
	}
	// A push is not a commit: what it carries is checked whole.
	if err := h.App.HookSecrets(ctx, h.Repo, wire.LeakWherePush, old["same row, new format"]); !errs.Has(err, errs.LeakBlocked) {
		t.Errorf("push of an old value: err = %v, want E_LEAK_BLOCKED", err)
	}
}

// TestCommitCheckNoHeadBlocksAll: before the first commit nothing is in
// history, so every finding blocks (fail closed).
func TestCommitCheckNoHeadBlocksAll(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	key := "AKIA" + strings.Repeat("Q", 16)
	err := h.App.HookSecrets(context.Background(), h.Repo, commitWhere, map[string][]byte{"notes.md": []byte(key + "\n")})
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED", err)
	}
}

// TestSecretsAllowTakesTheFingerprint (bug B1-15): the sha256 fingerprint
// a refused commit prints is what --match-hash takes (with or without its
// prefix), and the entry it writes holds the finding's match hash; the
// number still works, a value that is neither is refused at once.
func TestSecretsAllowTakesTheFingerprint(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	key := "AKIA" + strings.Repeat("Q", 16)
	memID := h.ID(h.Must("memory.add", `{"body":"target"}`))
	memFile := allowRowFile(t, h.Repo, "memories", memID)
	b, err := os.ReadFile(filepath.Clean(memFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memFile, []byte(strings.Replace(string(b), `"target"`, `"rotate `+key+`"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	fp := scan.Mask(key)
	for _, given := range []string{fp, strings.TrimPrefix(fp, "sha256:")} {
		id := h.ID(h.MustYes("secrets.allow", `{"row_id":"`+memID+`","rule":"aws_access_key","field":"body","match_hash":"`+given+`","reason":"test fixture"}`))
		got, _ := os.ReadFile(filepath.Clean(allowRowFile(t, h.Repo, "secret_allows", id)))
		want := `"match_hash": ` + strconv.FormatUint(uint64(scan.HashMatch(key)), 10)
		if !strings.Contains(string(got), want) {
			t.Fatalf("--match-hash %s wrote %s, want %s", given, got, want)
		}
	}
	h.Fails("secrets.allow", `{"row_id":"`+memID+`","rule":"aws_access_key","field":"body","match_hash":"sha256:0123456789ab","reason":"x"}`, errs.NotFound)
	h.Fails("secrets.allow", `{"row_id":"`+memID+`","rule":"aws_access_key","field":"body","match_hash":"sha256:zz","reason":"x"}`, errs.InvalidInput)
}
