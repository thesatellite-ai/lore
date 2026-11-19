package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestErrorLineMasksTypedSecret (devco R-11): `lore show <token>`, `lore
// memory add --to <token>`, `lore config set --key <token>` refuse with a
// message that repeats the input; the printed line (human and --json)
// names the rule and hash, never the token.
func TestErrorLineMasksTypedSecret(t *testing.T) {
	tok := "gh" + "p_" + "aB3dE9fG1hJ2kL8mN4pQ7rS5tU6vW0xY1zC2"
	err := errs.New(errs.NotFound, "no store named "+tok+" is attached").WithHint("not " + tok)
	for _, asJSON := range []bool{false, true} {
		var out, eout bytes.Buffer
		printError(&out, &eout, err, asJSON, Palette{}, "lore memory add")
		got := out.String() + eout.String()
		if strings.Contains(got, tok) || !strings.Contains(got, "redacted github_token sha256:") {
			t.Fatalf("json=%v: %s", asJSON, got)
		}
	}
}
