package wire_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestHookSecretsGoSumHash: the commit and push secret check passes a
// go.sum (or go.work.sum) module hash line, refuses the same line in any
// other file, and still refuses a real token on a go.sum line. The hash is
// built at run time so this file never holds the flagged line.
func TestHookSecretsGoSumHash(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	hash := "modernc.org/token v1.1.0 h1:" + "Xl7Ap9dKaEs5kLoOQeQm" + "PWevfnk/DM5qcLcYlA8ys6Y=\n"
	token := "token = ghp_" + strings.Repeat("A1b2", 9) + "\n"
	cases := []struct {
		name, path, body string
		refused          bool
	}{
		{"go.sum hash", "go.sum", hash, false},
		{"go.work.sum hash", "tools/go.work.sum", hash, false},
		{"hash line outside a checksum file", "notes.md", hash, true},
		{"token in go.sum", "go.sum", hash + token, true},
	}
	for _, c := range cases {
		err := h.App.HookSecrets(ctx, h.Repo, wire.LeakWherePush, map[string][]byte{c.path: []byte(c.body)})
		if got := errs.Has(err, errs.LeakBlocked); got != c.refused {
			t.Errorf("%s: err = %v, want refused=%v", c.name, err, c.refused)
		}
	}
}
