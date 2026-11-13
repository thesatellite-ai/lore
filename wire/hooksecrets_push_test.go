package wire_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestPushCheckAgreesWithCommitCheck (user reports A-8, B-6): after a format
// rewrite the commit check let a value the same row already held at HEAD
// through, and the push of that same commit was refused, because the push
// check scanned every pushed blob whole. With the remote's commits as
// bases (wire.WithPushBases, what the pre-push hook passes) a value the
// same row held there is not new; a value the push adds still blocks, with
// its ready allow line in the one spelling (--row-id, --field, --rule)
// and "push again". Break-verified: with the push branch of hookSecrets
// removed, the "already on the remote" case is refused.
func TestPushCheckAgreesWithCommitCheck(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time, never a literal key
	other := "AKIA" + strings.Repeat("Z", 16)
	memID := h.ID(h.Must("memory.add", `{"body":"target"}`))
	memFile := allowRowFile(t, h.Repo, "memories", memID)
	relPath, err := filepath.Rel(h.Repo, memFile)
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.ToSlash(relPath)
	b, err := os.ReadFile(filepath.Clean(memFile))
	if err != nil {
		t.Fatal(err)
	}
	committed := strings.Replace(string(b), `"target"`, `"rotate `+key+`"`, 1)
	if err := os.WriteFile(memFile, []byte(committed), 0o600); err != nil {
		t.Fatal(err)
	}
	histCommit(t, h.Repo, "history with a key")
	out, err := exec.Command("git", "-C", h.Repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(out))
	reformatted := strings.ReplaceAll(committed, `": `, `":  `) // the same row, another layout (a migrate rewrite)

	pushed := wire.WithPushBases(ctx, []string{base})
	if err := h.App.HookSecrets(pushed, h.Repo, wire.LeakWherePush, map[string][]byte{rel: []byte(reformatted)}); err != nil {
		t.Fatalf("a value the same row held on the remote must not block the push: %v", err)
	}
	// Without bases (an empty remote) every finding still blocks.
	if err := h.App.HookSecrets(ctx, h.Repo, wire.LeakWherePush, map[string][]byte{rel: []byte(reformatted)}); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("with no remote commit to compare with, err = %v, want E_LEAK_BLOCKED", err)
	}
	added := strings.Replace(committed, key, key+" and "+other, 1)
	err = h.App.HookSecrets(pushed, h.Repo, wire.LeakWherePush, map[string][]byte{rel: []byte(added)})
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("a value the push adds: err = %v, want E_LEAK_BLOCKED", err)
	}
	msg := err.Error()
	var e *errs.Error
	if errors.As(err, &e) {
		msg += "\nhint: " + e.Hint
	}
	for _, want := range []string{"not a secret? lore secrets allow --row-id " + memID + " --field body --rule ", "push again"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the push refusal misses %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, other) || strings.Contains(msg, key) {
		t.Fatalf("the refusal printed a value:\n%s", msg)
	}
}

// TestCommitMessageFindingNamesNoRowAllow (user report C-10): a secret in a
// commit message (the commit-msg check, or a pushed commit's message) gets
// a `fix:` line to edit the message, never an allow command with --row-id
// or --field, which a message does not have. Break-verified: with a
// message finding treated as a file finding with no allow line (the old
// code), the hint is the --row-id template and the test fails.
func TestCommitMessageFindingNamesNoRowAllow(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("Q", 16)
	for where, doc := range map[string]string{commitWhere: "commit message", wire.LeakWherePush: wire.PushCommitMsgPrefix + "abc1234"} {
		err := h.App.HookSecrets(ctx, h.Repo, where, map[string][]byte{doc: []byte("fix: rotate key " + key + "\n")})
		if !errs.Has(err, errs.LeakBlocked) {
			t.Fatalf("%s: err = %v, want E_LEAK_BLOCKED", where, err)
		}
		full := err.Error()
		var e *errs.Error
		if errors.As(err, &e) {
			full += "\nhint: " + e.Hint
		}
		if strings.Contains(full, "--row-id") || strings.Contains(full, "--field") || !strings.Contains(full, "fix: ") {
			t.Errorf("%s: a commit message finding must name the message fix only:\n%s", where, full)
		}
	}
}
