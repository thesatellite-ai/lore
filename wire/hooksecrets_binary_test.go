package wire_test

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/internal/testtime"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestHookSecretsSkipBinaryFiles is devco:Q-8: a binary file (a NUL byte
// in its first 8000 bytes, git's rule) is not scanned for secrets, so a
// large random asset neither takes seconds nor is blocked as a
// password_pair at a "line" of random bytes; the same token in a text file
// is still refused.
func TestHookSecretsSkipBinaryFiles(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	token := "token = ghp_" + strings.Repeat("A1b2", 9) + "\n"
	if err := h.App.HookSecrets(ctx, h.Repo, wire.LeakWherePush, map[string][]byte{"notes.txt": []byte(token)}); !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("a token in a text file is refused: %v", err)
	}
	if err := h.App.HookSecrets(ctx, h.Repo, wire.LeakWherePush, map[string][]byte{"asset.bin": []byte("\x00\x01" + token)}); err != nil {
		t.Fatalf("a binary file is not scanned: %v", err)
	}
	big := make([]byte, 20<<20)
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // test bytes, not a secret
	for i := range big {
		big[i] = byte(r.UintN(256))
	}
	big[0] = 0 // binary by git's rule whatever the random bytes are
	start := time.Now()
	if err := h.App.HookSecrets(ctx, h.Repo, wire.LeakWherePush, map[string][]byte{"big.bin": big}); err != nil {
		t.Fatalf("random bytes are not a secret: %v", err)
	}
	if d, bar := time.Since(start), testtime.SpeedBar(2*time.Second); d > bar {
		t.Errorf("a 20 MB binary took %s to check, want well under %s", d, bar)
	}
}
