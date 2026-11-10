package rest

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTokenFileAuthRotate (BUG-API-ROTATE): `lore api` checks the token
// file on every request, so a rotate refuses the old token at once and
// accepts the new one without a restart; a missing, garbage or
// group-readable file refuses every token, including the last good one.
func TestTokenFileAuthRotate(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "lore")
	old, err := LoadOrCreateToken(home)
	if err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	s, err := New(f.reg, f, Options{Authenticate: TokenFileAuth(home), LoreVersion: "0.1.18", Kinds: kindsFake{}})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	want := func(step, token string, status int, hdr ...string) {
		t.Helper()
		r := call(h, http.MethodGet, PathSpec, "", token, hdr...)
		if r.status != status {
			t.Fatalf("%s: status %d, want %d (%s)", step, r.status, status, r.body)
		}
		if status == http.StatusUnauthorized && code(t, r) != errs.Unauthorized {
			t.Fatalf("%s: code %s", step, code(t, r))
		}
	}
	want("old token before rotate", old, http.StatusOK)
	want("no token", "", http.StatusUnauthorized)
	want("wrong token", testToken, http.StatusUnauthorized)

	rot, err := RotateToken(home)
	if err != nil {
		t.Fatal(err)
	}
	want("old token after rotate", old, http.StatusUnauthorized)
	want("new token after rotate", rot, http.StatusOK)

	if err := os.Chmod(TokenPath(home), tokenMode|groupReadBit); err != nil {
		t.Fatal(err)
	}
	want("group-readable file", rot, http.StatusUnauthorized)
	if err := os.WriteFile(TokenPath(home), []byte("short\n"), tokenMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(TokenPath(home), tokenMode); err != nil {
		t.Fatal(err)
	}
	want("garbage file", rot, http.StatusUnauthorized)
	want("garbage file, its content as token", "short", http.StatusUnauthorized)
	if err := os.Remove(TokenPath(home)); err != nil {
		t.Fatal(err)
	}
	want("missing file", rot, http.StatusUnauthorized)
	// An unusable file must not degrade to an empty expected token: an
	// empty bearer would then match it (closed, never open).
	want("missing file, empty bearer", "", http.StatusUnauthorized, "Authorization", "Bearer ")
}
