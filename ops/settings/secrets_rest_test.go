package settings_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// TestRESTSecretChoice (SECRETS 4 over REST, P2-CTX-5): over the real
// engine a write whose body holds a detected secret and carries no answer
// header is refused (never stored); X-Lore-Secret-Unstored redacts it,
// X-Lore-Secret-Ref replaces it with the reference, X-Lore-Not-A-Secret
// with a reason and yes keeps it; a bad answer (blank reason, a ref with no
// scheme, a control character) is refused; no response ever echoes the
// secret. The answer travels in headers only: body fields such as
// "secret_unstored" are not part of memory.add's input.
func TestRESTSecretChoice(t *testing.T) {
	h := opstest.New(t, ownRepo) // a client repo refuses secret references (BATCH 5)
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rest.New(h.App.Registry(), h.App, rest.Options{Token: restToken, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in source
	body := `{"body":"deploy key ` + key + ` end"}`
	post := func(b string, hdr ...[2]string) (int, string) {
		t.Helper()
		req, rerr := http.NewRequest(http.MethodPost, ts.URL+"/v1/memories", strings.NewReader(b))
		if rerr != nil {
			t.Fatal(rerr)
		}
		req.Header.Set("Authorization", "Bearer "+restToken)
		req.Header.Set("Content-Type", "application/json")
		for _, kv := range hdr {
			req.Header.Add(kv[0], kv[1])
		}
		resp, rerr := ts.Client().Do(req)
		if rerr != nil {
			t.Fatal(rerr)
		}
		defer func() { _ = resp.Body.Close() }()
		out, rerr := io.ReadAll(resp.Body)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if strings.Contains(string(out), key) && len(hdr) == 0 {
			t.Errorf("response echoes the secret: %s", out)
		}
		return resp.StatusCode, string(out)
	}
	stored := func() string { return h.JSON(h.Must("memory.list", `{}`)) }

	// No answer: refused or asks, and nothing is written.
	code, out := post(body)
	if code/100 == 2 || (!strings.Contains(out, "E_SECRET_DETECTED") && !strings.Contains(out, "E_NEEDS_CONFIRM")) {
		t.Fatalf("no answer header: %d %s", code, out)
	}
	if strings.Contains(stored(), "deploy key") {
		t.Fatal("a refused secret write stored the row")
	}
	for _, bad := range [][2]string{
		{rest.HeaderFalsePositive, " "},
		{rest.HeaderVaultRef, "acme/aws"},
		{rest.HeaderUnstoredFinding, "maybe"},
	} {
		if code, out = post(body, bad); code/100 == 2 || !strings.Contains(out, "E_INVALID_INPUT") {
			t.Errorf("bad answer %q: %d %s", bad, code, out)
		}
	}
	if strings.Contains(stored(), "deploy key") {
		t.Fatal("a bad answer stored the row")
	}
	if code, out = post(`{"body":"x ` + key + `","secret_unstored":true}`); code/100 == 2 {
		t.Errorf("a body field answered the finding: %d %s", code, out)
	}

	if code, out = post(body, [2]string{rest.HeaderUnstoredFinding, "true"}); code != http.StatusCreated {
		t.Fatalf("unstored: %d %s", code, out)
	}
	if code, out = post(`{"body":"vault key `+key+` end"}`, [2]string{rest.HeaderVaultRef, "gopass://acme/aws"}); code != http.StatusCreated {
		t.Fatalf("secret ref: %d %s", code, out)
	}
	if code, out = post(`{"body":"doc sample `+key+` end","yes":true}`, [2]string{rest.HeaderFalsePositive, "sample from the aws docs"}); code != http.StatusCreated {
		t.Fatalf("not a secret: %d %s", code, out)
	}
	s := stored()
	if !strings.Contains(s, "${secret:unstored}") || !strings.Contains(s, "${secret:gopass://acme/aws}") || !strings.Contains(s, "doc sample "+key) {
		t.Errorf("stored rows %q", s)
	}
	if strings.Contains(strings.ReplaceAll(s, "doc sample "+key, ""), key) {
		t.Errorf("a redacted row kept the secret: %q", s)
	}
}
