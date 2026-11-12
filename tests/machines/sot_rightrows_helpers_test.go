//go:build machines && !windows

package machines

// sot_rightrows_helpers_test.go — helpers of the source-of-truth proofs
// (sot_rightrows_test.go, sot_ownercase_test.go): read and hand-edit row
// files, run lore with extra environment, run git on a machine, share a
// bare repo behind a GitHub url between machines, back-date a kept
// timestamp (the clock seam: a copy's own "when" field is moved into the
// past instead of waiting), and call `lore api` (csStartAPI) expecting a
// status.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// sotWithEnv is m running every command with the extra KEY=value pairs
// (later pairs win over the base environment).
func sotWithEnv(m *Machine, kv ...string) *Machine {
	c := *m
	c.ExtraEnv = append(slices.Clone(m.ExtraEnv), kv...)
	return &c
}

// sotID is the "id" of a write's --json answer.
func sotID(t *testing.T, out string) string {
	t.Helper()
	var o struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil || o.ID == "" {
		t.Fatalf("no id in %q (%v)", out, err)
	}
	return o.ID
}

// sotRowPath is the row file of id in table in a code repo's checkout:
// <root>/.lore/data/<table>/<id>.json.
func sotRowPath(root, table, id string) string {
	return filepath.Join(root, ".lore", "data", table, id+".json")
}

// sotStoreRowPath is the row file of id in table in a store folder (a
// project or org folder of a store): <folder>/<table>/<id>.json.
func sotStoreRowPath(folder, table, id string) string {
	return filepath.Join(folder, table, id+".json")
}

// sotStoreRootRowPath is the row file of id in table among a store's own
// rows (its root folder, D-ad): <store>/_store/<table>/<id>.json.
func sotStoreRootRowPath(store, table, id string) string {
	return filepath.Join(store, "_store", table, id+".json")
}

// sotRow reads a row file's top-level fields.
func sotRow(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("row file: %v", err)
	}
	row := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &row); err != nil {
		t.Fatalf("row file %s: %v", path, err)
	}
	return row
}

// sotRowText is a row field as text: a JSON string unquoted, anything
// else as its JSON ("" when absent).
func sotRowText(t *testing.T, path, field string) string {
	t.Helper()
	raw, ok := sotRow(t, path)[field]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// sotEditRow sets a string field of a row file by hand, the way a person
// edits it in an editor: the file is rewritten, nothing else is told.
func sotEditRow(t *testing.T, path, field, val string) {
	t.Helper()
	row := sotRow(t, path)
	b, err := json.Marshal(val)
	if err != nil {
		t.Fatal(err)
	}
	row[field] = b
	out, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sotGit runs git in dir with m's environment and returns its output.
func sotGit(t *testing.T, m *Machine, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("/usr/bin/git", args...)
	c.Dir, c.Env = dir, m.env()
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// sotBare makes an empty bare repo (branch main) at path.
func sotBare(t *testing.T, path string) {
	t.Helper()
	if out, err := exec.Command("/usr/bin/git", "init", "-q", "--bare", "-b", "main", path).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare %s: %v %s", path, err, out)
	}
}

// sotRewrite points m's git at the bare repo for url (url.insteadOf in
// m's ~/.gitconfig): lore sees a GitHub url, git reaches a repo on disk
// that several machines share.
func sotRewrite(t *testing.T, m *Machine, bare, url string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(m.Home, ".gitconfig"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("[url \"file://" + bare + "\"]\n\tinsteadOf = " + url + "\n"); err != nil {
		t.Fatal(err)
	}
}

// sotOut is a run's stdout and stderr together (notices go to stderr).
func sotOut(r Result) string { return r.Stdout + "\n" + r.Stderr }

// sotOK runs lore and fails t unless it exits 0; it returns stdout and
// stderr together.
func sotOK(t *testing.T, m *Machine, cwd string, args ...string) string {
	t.Helper()
	r := m.Lore(t, cwd, args...)
	if r.Code != 0 {
		t.Fatalf("%s", r)
	}
	return sotOut(r)
}

// sotAPICall sends one request to `lore api` and fails t unless the
// status is want; it returns the body.
func sotAPICall(t *testing.T, a *csAPI, method, path, body string, want int) string {
	t.Helper()
	got, out := a.call(t, method, path, body)
	if got != want {
		t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, got, want, out)
	}
	return out
}

// sotJSONBody encodes a request body.
func sotJSONBody(t *testing.T, v map[string]string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// sotHTTP names the statuses the proofs expect.
const (
	sotStatusOK       = http.StatusOK
	sotStatusNotFound = http.StatusNotFound
)

// sotBackdateStamp writes a fetch-stamp (RFC 3339, as lore writes it)
// that is age old: the next read treats the clone as due a fetch,
// without waiting out live.fetch_ttl.
func sotBackdateStamp(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fetch stamp: %v", err)
	}
	if err := os.WriteFile(path, []byte(time.Now().Add(-age).UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// sotReadStamp is a fetch-stamp's time.
func sotReadStamp(t *testing.T, path string) time.Time {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("fetch stamp %q: %v", b, err)
	}
	return at
}

// sotActorID is whoami's actor id in dir.
func sotActorID(t *testing.T, m *Machine, dir string) string {
	t.Helper()
	var o struct {
		Data struct {
			Identity struct {
				ActorID string `json:"actor_id"`
			} `json:"identity"`
		} `json:"data"`
	}
	got := lsRun(t, m, dir, "whoami")
	if err := json.Unmarshal([]byte(got), &o); err != nil || o.Data.Identity.ActorID == "" {
		t.Fatalf("whoami names no actor id (%v): %s", err, got)
	}
	return o.Data.Identity.ActorID
}

// sotLoreOnPath puts the lore under test on m's PATH as `lore` (git's lore
// merge driver and hooks call it by that name; the binary under test need
// not be called lore).
func sotLoreOnPath(t *testing.T, m *Machine) {
	t.Helper()
	dir := filepath.Join(m.Dir, "sot-bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(m.bin, filepath.Join(dir, "lore")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	m.PathPrefix = append(m.PathPrefix, dir)
}
