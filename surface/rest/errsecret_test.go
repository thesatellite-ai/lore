package rest

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestErrorBodyMasksTypedSecret (devco R-11): `lore api` answers a refusal
// that repeats the input (E_INVALID_ID for a token passed as an id) with
// the rule and hash in place of the token, as the CLI does.
func TestErrorBodyMasksTypedSecret(t *testing.T) {
	tok := "gh" + "p_" + "aB3dE9fG1hJ2kL8mN4pQ7rS5tU6vW0xY1zC2"
	rec := httptest.NewRecorder()
	WriteError(rec, errs.New(errs.InvalidIdentifier, tok+" is not a row id"))
	if b := rec.Body.String(); strings.Contains(b, tok) || !strings.Contains(b, "redacted github_token") || !strings.Contains(b, "E_INVALID_ID") {
		t.Fatalf("body %s", b)
	}
}
