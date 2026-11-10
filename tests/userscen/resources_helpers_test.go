//go:build userscen

// Shared fixtures of the resources area: a person's initialised repo with
// a host/service kind pair, JSON shapes of the resource commands, and a
// file writer for import files. The scenarios live in resources_*_test.go.
package userscen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Exit statuses of docs/reference/errors.md the resource scenarios assert.
const (
	resExitInvalid  = 20 // E_INVALID_INPUT, E_RESOURCE_INVALID, E_RESOURCE_NAME, E_RESOURCE_TYPE
	resExitAmbig    = 21 // E_RESOURCE_AMBIGUOUS
	resExitKind     = 22 // E_KIND_UNKNOWN, E_KIND_CONFLICT
	resExitRefused  = 23 // E_RESOURCE_TAKEN, E_RESOURCE_BATCH, E_RESOURCE_IN_USE, E_IN_TRASH, …
	resExitNotFound = 27 // E_NOT_FOUND
	resExitConfirm  = 26 // E_NEEDS_CONFIRM
)

// resRepo is one person's initialised code repo.
func resRepo(t *testing.T) *th.Clone {
	t.Helper()
	w := newWorld(t)
	a := w.Human("alice", "alice@example.com")
	a.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}})
	c := a.Clone(w.Remote("github.com/octo-personal/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	return c
}

// resHostService adds the kinds most scenarios use: host (provider closed
// set, required; ip; cores number) and service (runs_on ref host; port
// number 1..65535).
func resHostService(t *testing.T, c *th.Clone) {
	t.Helper()
	c.MustLore("resource", "kind", "add", "host", "--description", "a server")
	c.MustLore("resource", "field", "add", "host", "provider", "--type", "text", "--required", "--values", "hetzner,aws")
	c.MustLore("resource", "field", "add", "host", "ip", "--type", "ip")
	c.MustLore("resource", "field", "add", "host", "cores", "--type", "number")
	c.MustLore("resource", "kind", "add", "service")
	c.MustLore("resource", "field", "add", "service", "runs_on", "--type", "ref", "--ref-kind", "host")
	c.MustLore("resource", "field", "add", "service", "port", "--type", "number", "--min", "1", "--max", "65535")
}

// resJSON is `lore resource show --json` (and one item of list --json).
type resJSON struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Store       string            `json:"store"`
	Values      map[string]string `json:"values"`
	LastChecked string            `json:"last_checked"`
}

// resShowJSON is the show envelope.
type resShowJSON struct {
	Data resJSON `json:"data"`
}

// resListJSON is the list envelope.
type resListJSON struct {
	Count int       `json:"count"`
	Data  []resJSON `json:"data"`
}

// resWriteJSON is add/edit/delete --json.
type resWriteJSON struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

// resErrJSON is the --json error envelope.
type resErrJSON struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Exit    int    `json:"exit"`
		Class   string `json:"class"`
	} `json:"error"`
}

// resShow decodes `lore resource show <key> --json`.
func resShow(t *testing.T, c *th.Clone, key string) resJSON {
	t.Helper()
	return th.DecodeJSON[resShowJSON](t, c.Lore("resource", "show", key, "--json")).Data
}

// resList decodes `lore resource list <kind> --json [args]`.
func resList(t *testing.T, c *th.Clone, kind string, args ...string) resListJSON {
	t.Helper()
	return th.DecodeJSON[resListJSON](t, c.Lore(append([]string{"resource", "list", kind, "--json"}, args...)...))
}

// resWriteFile writes an import file under the clone's actor HOME (never
// inside the code repo, so no import file shows in git status) and returns
// its path.
func resWriteFile(t *testing.T, c *th.Clone, name, body string) string {
	t.Helper()
	dir := filepath.Join(c.A.Home, "imports")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// resErrOf decodes a --json refusal (stdout carries the envelope).
func resErrOf(t *testing.T, r th.Result) resErrJSON {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("expected a refusal: %s", r)
	}
	var e resErrJSON
	if err := json.Unmarshal([]byte(r.Stdout), &e); err != nil {
		t.Fatalf("decode error envelope: %v\n%s", err, r)
	}
	return e
}
