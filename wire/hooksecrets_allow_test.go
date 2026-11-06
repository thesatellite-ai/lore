package wire_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestPushHonoursCommittedAllow (SECRETS 19.4, later 499): the pre-push
// secret check on a real git repo reads secret_allows entries from the
// index only. A pushed memory row whose body holds a credential is refused
// while its sal_ entry is only on disk, passes once the entry is staged,
// and is refused again for a second credential the entry does not cover
// (another hash) and for the same credential outside a .lore/data row.
func TestPushHonoursCommittedAllow(t *testing.T) {
	h := opstest.New(t)
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("Q", 16)
	other := "AKIA" + strings.Repeat("Z", 16)
	memID := h.ID(h.Must("memory.add", `{"body":"target"}`))
	memFile := allowRowFile(t, h.Repo, "memories", memID)
	b, err := os.ReadFile(filepath.Clean(memFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"target"`) {
		t.Fatalf("memory row file has no body field: %s", b)
	}
	pushed := func(secret string) map[string][]byte {
		rel, rerr := filepath.Rel(h.Repo, memFile)
		if rerr != nil {
			t.Fatal(rerr)
		}
		return map[string][]byte{filepath.ToSlash(rel): []byte(strings.Replace(string(b), `"target"`, `"rotate `+secret+`"`, 1))}
	}
	push := func(docs map[string][]byte) error {
		return h.App.HookSecrets(ctx, h.Repo, wire.LeakWherePush, docs)
	}

	allowID := h.ID(h.MustYes("secrets.allow", `{"row_id":"`+memID+`","rule":"aws_access_key","field":"body","match_hash":`+
		strconv.FormatUint(uint64(scan.HashMatch(key)), 10)+`,"reason":"test fixture"}`))
	allowFile := allowRowFile(t, h.Repo, "secret_allows", allowID)

	if err := push(pushed(key)); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("allow entry only on disk: err = %v, want E_LEAK_BLOCKED", err)
	}
	allowGit(t, h.Repo, "add", "--", allowFile)
	if err := push(pushed(key)); err != nil {
		t.Fatalf("allow entry staged: err = %v, want pass", err)
	}
	if err := push(pushed(other)); !errs.Has(err, errs.LeakBlocked) || strings.Contains(err.Error(), other) {
		t.Fatalf("credential the entry does not cover: err = %v, want E_LEAK_BLOCKED without the value", err)
	}
	if err := push(map[string][]byte{"notes.txt": []byte("rotate " + key)}); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("same credential outside the row: err = %v, want E_LEAK_BLOCKED", err)
	}
	allowGit(t, h.Repo, "rm", "-q", "--cached", "--", allowFile)
	if err := push(pushed(key)); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("allow entry unstaged again: err = %v, want E_LEAK_BLOCKED", err)
	}
}

// allowRowFile is the one row file of id under the repo's .lore/data/<table>.
func allowRowFile(t *testing.T, repo, table, id string) string {
	t.Helper()
	var hit []string
	err := filepath.WalkDir(filepath.Join(repo, ".lore", "data", table), func(p string, _ os.DirEntry, werr error) error {
		if werr == nil && strings.Contains(filepath.Base(p), id) {
			hit = append(hit, p)
		}
		return werr
	})
	if err != nil || len(hit) != 1 {
		t.Fatalf("row file of %s in %s: %v %v", id, table, hit, err)
	}
	return hit[0]
}

func allowGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// built directly (as wire's gitRun does): the program is the resolved
	// git binary and every argument is its own argv entry, no shell
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
