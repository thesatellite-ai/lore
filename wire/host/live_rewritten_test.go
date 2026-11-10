package host

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRewrittenErrorNamesTheBackup (b-flow4): the refusal of a write while
// the remote live ref was rewritten names the exact backup ref in its
// message (the surface prints message and hint, not the wrapped cause);
// without a name in the cause it names the backup namespace; the code and
// the ErrRemoteRewritten chain are kept.
func TestRewrittenErrorNamesTheBackup(t *testing.T) {
	backup := live.RefBackupPrefix + "20261106T051000Z"
	cases := []struct {
		name  string
		cause error
		want  string
	}{
		{"named backup", fmt.Errorf("%w (local copy kept as %s)", live.ErrRemoteRewritten, backup), "kept as " + backup},
		{"no name in the cause", live.ErrRemoteRewritten, "kept under " + live.RefBackupPrefix},
		{"a name outside the backup refs is not trusted", fmt.Errorf("%w (local copy kept as refs/heads/x)", live.ErrRemoteRewritten), "kept under " + live.RefBackupPrefix},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, code := range []errs.Code{errs.ReadOnly, errs.StoreReadOnly} {
				err := rewrittenError(code, c.cause)
				var e *errs.Error
				if !errors.As(err, &e) || e.Code != code || !errors.Is(err, live.ErrRemoteRewritten) {
					t.Fatalf("%s: %v", code, err)
				}
				if !strings.Contains(e.Message, c.want) || !strings.Contains(e.Hint, "lore live restore") {
					t.Fatalf("message %q hint %q, want %q", e.Message, e.Hint, c.want)
				}
			}
		})
	}
}
