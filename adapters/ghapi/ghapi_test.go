package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// secret is built at run time (no key-shaped literal in the source).
var secret = "gho_" + strings.Repeat("Q", 12) + "tok"

// Every way a Token can be printed prints [redacted].
func TestTokenNeverFormats(t *testing.T) {
	tok, ok := NewToken("  " + secret + "\n")
	if !ok {
		t.Fatal("NewToken refused a token")
	}
	js, _ := json.Marshal(struct{ T Token }{tok})
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", "tok", tok)
	for _, s := range []string{fmt.Sprint(tok), fmt.Sprintf("%v %+v %#v %s %q", tok, tok, tok, tok, tok), string(js), logs.String()} {
		if strings.Contains(s, secret) || !strings.Contains(s, tokenRedacted) {
			t.Errorf("formatted token: %q", s)
		}
	}
	for _, bad := range []string{"", "  ", "a b", "a\nb"} {
		if _, ok := NewToken(bad); ok {
			t.Errorf("NewToken(%q) accepted", bad)
		}
	}
}

// The client sends the token only as the Authorization header, reads the
// scopes, and its errors never carry the body (here echoing the token).
func TestClientCallsAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/user":
			w.Header().Set("X-OAuth-Scopes", "repo, read:org")
			_, _ = w.Write([]byte(`{"id":42,"login":"mira","name":"Mira","email":"m@x.example"}`))
		case "/user/emails":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"` + r.Header.Get("Authorization") + `"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(secret))
		}
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL}
	tok, _ := NewToken(secret)
	ctx := context.Background()
	u, err := c.User(ctx, tok)
	if err != nil || u.User.Login != "mira" || u.User.NoreplyEmail() != "42+mira@users.noreply.github.com" || u.CanReadEmails() || !u.ScopesKnown {
		t.Fatalf("User = %+v, %v", u, err)
	}
	_, err = c.Emails(ctx, tok)
	if StatusOf(err) != http.StatusNotFound || strings.Contains(err.Error(), secret) {
		t.Errorf("Emails err = %v", err)
	}
	_, err = c.Repos(ctx, tok)
	if StatusOf(err) != http.StatusInternalServerError || strings.Contains(err.Error(), secret) {
		t.Errorf("Repos err = %v", err)
	}
	if _, err = c.User(ctx, Token{}); err != ErrNoToken {
		t.Errorf("empty token err = %v", err)
	}
	other, _ := NewToken("wrong")
	if _, err = c.User(ctx, other); StatusOf(err) != http.StatusUnauthorized {
		t.Errorf("wrong token err = %v", err)
	}
}
