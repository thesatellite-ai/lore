package rest

// token_rotate_scen_test.go — the token-file check `lore api` serves with
// (Options.Authenticate + TokenFileAuth): a rotate applies to a running
// server, a removed or unsafe token file lets nobody in, and New still
// refuses to build an API with no credential at all. The end-to-end twin
// through the real binary is tests/userscen TestRestAuth_TokenFileLifecycle.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRestTokenSource_RotateOnRunningServer walks one server through
// rotate, rotate, remove, unsafe mode and re-create.
func TestRestTokenSource_RotateOnRunningServer(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), ".lore")
	first, err := LoadOrCreateToken(home)
	if err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	s, err := New(f.reg, f, Options{Authenticate: TokenFileAuth(home)})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	ok := func(what, tok string) {
		t.Helper()
		if r := call(h, http.MethodGet, "/v1/memories", "", tok); r.status != http.StatusOK {
			t.Errorf("%s: %d %s", what, r.status, r.body)
		}
	}
	refused := func(what, tok string) {
		t.Helper()
		r := call(h, http.MethodGet, "/v1/memories", "", tok)
		if r.status != http.StatusUnauthorized || code(t, r) != errs.Unauthorized {
			t.Errorf("%s: %d %s", what, r.status, r.body)
		}
	}
	ok("first token", first)
	second, err := RotateToken(home)
	if err != nil {
		t.Fatal(err)
	}
	refused("old token after rotate", first)
	ok("new token after rotate", second)

	if err := os.Remove(TokenPath(home)); err != nil {
		t.Fatal(err)
	}
	refused("token file removed", second)
	refused("token file removed, empty bearer", "")

	third, err := RotateToken(home)
	if err != nil {
		t.Fatal(err)
	}
	ok("re-created token", third)
	if err := os.Chmod(TokenPath(home), 0o644); err != nil {
		t.Fatal(err)
	}
	refused("token file readable by others", third)
	if err := os.Chmod(TokenPath(home), tokenMode); err != nil {
		t.Fatal(err)
	}
	ok("mode fixed", third)
	if err := os.WriteFile(TokenPath(home), []byte("garbage\n"), tokenMode); err != nil {
		t.Fatal(err)
	}
	refused("garbage token file", "garbage")
}

// TestRestTokenSource_NoCredential: New refuses options with no token and
// no authenticator, so a server can never start open.
func TestRestTokenSource_NoCredential(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	if _, err := New(f.reg, f, Options{}); !errs.Has(err, errs.ConfigInvalid) {
		t.Errorf("no token, no authenticator: %v", err)
	}
}
