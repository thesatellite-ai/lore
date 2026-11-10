//go:build userscen

package userscen

// migrate_helpers_test.go — shared helpers of the `migrate` area (moving
// v0.1.17 data to the 0.1.18 format, the format gate, `lore init`). Every
// helper name starts with mig so it never collides with another area's.
//
// The v0.1.17 data is made the way it reaches a 0.1.18 user: rows written
// by today's binary are turned back into v0.1.17 files (`_v: 1`, no `_hlc`,
// compact, keys sorted, as v0.1.17's writer laid them out), committed and
// pushed, so every field a user can set is in the old file and the lossy
// diff after `lore migrate run` covers all of them.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// migDataDir is the repo store's data folder inside a checkout.
const migDataDir = ".lore/data"

// migSlug is the remote every migrate scenario clones (placeholder owner).
const migSlug = "github.com/octo-personal/app"

// Row envelope keys (model/row).
const (
	migKeyVersion = "_v"
	migKeyHLC     = "_hlc"
)

// migKind is one kind of row a v0.1.17 user had, and how a user adds one
// with every field they can set filled.
type migKind struct {
	noun  string   // the CLI noun (lore <noun> show)
	table string   // the folder under .lore/data
	add   []string // the add command, flags included
}

// migKinds are the knowledge and work kinds v0.1.17 users had in their
// repos. Bodies carry unicode, HTML characters and quotes, the spellings a
// re-encode must keep.
func migKinds() []migKind {
	return []migKind{
		{"memory", "memories", []string{"memory", "add", "Café note ünï — <b>&</b> \"q\"", "--kind", "episodic", "--source-ref", "doc#7", "--no-live"}},
		{"rule", "rules", []string{"rule", "add", "Use tabs in Go files ünï", "--severity", "should", "--summary", "tabs", "--activation", "glob", "--globs", `["*.go"]`, "--source-ref", "adr-3", "--no-live"}},
		{"decision", "decisions", []string{"decision", "add", "We keep SQLite for the cache — 日本", "--title", "Cache storage", "--status", "proposed", "--source-ref", "rfc-9"}},
		{"hotfix", "hotfixes", []string{"hotfix", "add", "Restart the worker after a deploy", "--title", "Worker restart", "--severity", "critical", "--summary", "restart it"}},
		{"pattern", "patterns", []string{"pattern", "add", "Wrap every error with context", "--title", "Error wrapping", "--language", "go"}},
		{"playbook", "playbooks", []string{"playbook", "add", "1. build\n2. tag\n3. push", "--title", "Release", "--description", "how a release goes out"}},
		{"prompt", "prompts", []string{"prompt", "add", "Review {{args.file}} for bugs", "--title", "Review a file", "--description", "a review prompt", "--args-schema", `{"type":"object"}`}},
		{"task", "tasks", []string{"task", "add", "Ship the importer ünï", "--no-live", "--priority", "high", "--due", "2026-12-01", "--estimate", "3", "--body", "long body <i>x</i>"}},
	}
}

// migNew builds a World with alice's own repo (octo-personal/app), `lore
// init`ed, committed and pushed.
func migNew(t *testing.T) (*th.World, *th.Remote, *th.Clone) {
	t.Helper()
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	app := w.Remote(migSlug)
	c := alice.Clone(app, "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.CommitAll("lore init")
	c.AdminPush("origin", "main")
	return w, app, c
}

// migFiles lists the row file names of table in c (sorted; none when the
// folder does not exist).
func migFiles(t *testing.T, c *th.Clone, table string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(c.Dir, migDataDir, table))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e.Name())
		}
	}
	return out
}

// migAdd runs k's add command in c and returns the id of the one new row
// file it wrote under .lore/data/<table>.
func migAdd(t *testing.T, c *th.Clone, k migKind) string {
	t.Helper()
	before := migFiles(t, c, k.table)
	c.Lore(k.add...).OK(t)
	var added []string
	for _, f := range migFiles(t, c, k.table) {
		if !slices.Contains(before, f) {
			added = append(added, f)
		}
	}
	if len(added) != 1 {
		t.Fatalf("%s add wrote %d new files in %s, want 1: %v", k.noun, len(added), k.table, added)
	}
	return strings.TrimSuffix(added[0], ".json")
}

// migRowPath is the absolute path of a row file in c.
func migRowPath(c *th.Clone, table, id string) string {
	return filepath.Join(c.Dir, migDataDir, table, id+".json")
}

// migRead reads a file (fails the test when it is missing).
func migRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// migDoc decodes a row file into its members.
func migDoc(t *testing.T, b []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, b)
	}
	return m
}

// migEncodeV017 lays m out the way v0.1.17 wrote a row file: compact, keys
// sorted, no HTML escaping.
func migEncodeV017(t *testing.T, m map[string]json.RawMessage) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// migDowngrade rewrites one row file as v0.1.17 wrote it (`_v: 1`, no
// `_hlc`) and returns the new bytes.
func migDowngrade(t *testing.T, path string) []byte {
	t.Helper()
	m := migDoc(t, migRead(t, path))
	m[migKeyVersion] = json.RawMessage("1")
	delete(m, migKeyHLC)
	b := migEncodeV017(t, m)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

// migCompact is the compact form of a JSON value (for comparing values
// written by two encoders).
func migCompact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("bad JSON value %s: %v", raw, err)
	}
	return buf.String()
}

// migJSONEqual reports whether two JSON documents hold the same value.
func migJSONEqual(a, b string) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, []byte(a)) != nil || json.Compact(&cb, []byte(b)) != nil {
		return false
	}
	var ma, mb map[string]json.RawMessage
	if json.Unmarshal(ca.Bytes(), &ma) != nil || json.Unmarshal(cb.Bytes(), &mb) != nil {
		return ca.String() == cb.String()
	}
	if len(ma) != len(mb) {
		return false
	}
	for k, va := range ma {
		vb, ok := mb[k]
		if !ok || !migJSONEqual(string(va), string(vb)) {
			return false
		}
	}
	return true
}

// migRunOut is `lore migrate run --json` (ops/migrate RunOut).
type migRunOut struct {
	DryRun    bool     `json:"dry_run"`
	Count     int      `json:"count"`
	Rows      []string `json:"rows"`
	Steps     []string `json:"steps"`
	Journal   []string `json:"journal"`
	BackupRef string   `json:"backup_ref"`
}

// migPlace is one place of `lore migrate status --json`.
type migPlace struct {
	Place string `json:"place"`
	V1    int    `json:"v1"`
	V2    int    `json:"v2"`
	Newer int    `json:"newer"`
	Total int    `json:"total"`
}

// migStatusOut is `lore migrate status --json` (ops/migrate StatusOut).
type migStatusOut struct {
	Writes         int64      `json:"writes"`
	Places         []migPlace `json:"places"`
	Features       []string   `json:"features"`
	MinLoreVersion string     `json:"min_lore_version"`
	Pending        []string   `json:"pending"`
	Journal        string     `json:"journal"`
	Lock           string     `json:"lock"`
	Notes          []string   `json:"notes"`
}

// migStatus runs `lore migrate status --json` in c.
func migStatus(t *testing.T, c *th.Clone) migStatusOut {
	t.Helper()
	return th.DecodeJSON[migStatusOut](t, c.Lore("migrate", "status", "--json"))
}

// migRepoPlace is the repo store's line of a status.
func migRepoPlace(t *testing.T, st migStatusOut) migPlace {
	t.Helper()
	for _, p := range st.Places {
		if p.Place == "repo" {
			return p
		}
	}
	t.Fatalf("migrate status has no repo place: %+v", st)
	return migPlace{}
}

// migBackupRefs lists the migrate backup refs of c.
func migBackupRefs(c *th.Clone) []string {
	out := c.Git("for-each-ref", "--format=%(refname)", "refs/lore-backup/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// migSnapshot reads every file under c's .lore/data, keyed by relative path.
func migSnapshot(t *testing.T, c *th.Clone) map[string]string {
	t.Helper()
	root := filepath.Join(c.Dir, migDataDir)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(filepath.Clean(p))
		out[rel] = string(b)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// migSameSnapshot fails when two snapshots differ in any file or byte.
func migSameSnapshot(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s changed:\n got %s\nwant %s", what, k, got[k], v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: new file %s", what, k)
		}
	}
}
