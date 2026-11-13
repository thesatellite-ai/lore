//go:build userscen

package userscen

// campaign_query_helpers_test.go — shared helpers of the query campaign
// (search, sql, show, api, audit, tui, spec and the REST surface as a
// whole). Every name starts with cq so it never collides with another
// area's helpers; the REST plumbing (restStart, restReq, restSchemaCheck,
// restDocs) is the rest area's, reused as is.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cqExitBadInput is the bad_input class exit (E_INVALID_INPUT,
// E_INVALID_ID, E_SQL_REFUSED; docs/reference/errors.md).
const cqExitBadInput = 20

// cqAddID runs an add command with --json and returns the new row's id
// (the add answer is {"id": …, "store": …}).
func cqAddID(t *testing.T, c *th.Clone, args ...string) string {
	t.Helper()
	r := c.MustLore(append(args, "--json")...)
	// {"id": …} (most adds) or the show envelope {"data": {"id": …}} (task add)
	var out struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &out); err != nil || out.ID == "" && out.Data.ID == "" {
		t.Fatalf("%v: no id in %q (%v)", args, r.Stdout, err)
	}
	if out.ID != "" {
		return out.ID
	}
	return out.Data.ID
}

// cqRefusal asserts r is the refusal code with a hint that holds every
// hintSays, a non-zero exit equal to the envelope's exit, under --json.
// r must come from a --json run.
func cqRefusal(t *testing.T, what string, r th.Result, code string, hintSays ...string) {
	t.Helper()
	e := restCLIErr(r)
	if e == nil {
		t.Errorf("%s: no error envelope (exit %d): %.400s", what, r.Code, r.Out())
		return
	}
	if e.Code != code {
		t.Errorf("%s: code %s, want %s (%s)", what, e.Code, code, e.Message)
	}
	if r.Code == 0 || r.Code != e.Exit {
		t.Errorf("%s: process exit %d, envelope exit %d", what, r.Code, e.Exit)
	}
	if strings.TrimSpace(e.Hint) == "" {
		t.Errorf("%s: %s %q has no hint naming the next step", what, e.Code, e.Message)
	}
	for _, s := range hintSays {
		if !strings.Contains(e.Hint, s) {
			t.Errorf("%s: hint %q lacks %q", what, e.Hint, s)
		}
	}
}

// cqTreeHash is one hash over every file under dir (path and bytes), so a
// command that must write nothing can be checked by hashing before/after.
func cqTreeHash(t *testing.T, dir string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, f)
		h.Write([]byte(rel + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cqDataHash is cqTreeHash of the clone's .lore/data (its rows).
func cqDataHash(t *testing.T, c *th.Clone) string {
	t.Helper()
	return cqTreeHash(t, filepath.Join(c.Dir, ".lore", "data"))
}

// cqSearchOut is the part of the search.global envelope the checks read.
type cqSearchOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          struct {
		Query string `json:"query"`
		Count int    `json:"count"`
		Hits  []struct {
			Entity  string  `json:"entity"`
			ID      string  `json:"id"`
			Score   float64 `json:"score"`
			Snippet string  `json:"snippet"`
			InTrash bool    `json:"in_trash"`
		} `json:"hits"`
		External []json.RawMessage `json:"external"`
	} `json:"data"`
}

// cqSearch runs `lore search run --query <q> --json extra…` and decodes it; a
// refusal fails the test.
func cqSearch(t *testing.T, c *th.Clone, q string, extra ...string) cqSearchOut {
	t.Helper()
	r := c.Lore(append([]string{"search", "run", q, "--json"}, extra...)...)
	if r.Code != 0 {
		t.Fatalf("search %q: exit %d %.400s", q, r.Code, r.Out())
	}
	var out cqSearchOut
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("search %q: not JSON: %v %.300s", q, err, r.Stdout)
	}
	if out.SchemaVersion != 1 || out.Kind != "search.global" || out.Data.Hits == nil || out.Data.External == nil || out.Data.Count != len(out.Data.Hits) {
		t.Errorf("search %q: envelope %+v", q, out)
	}
	return out
}

// cqIDs is the sorted id set of a search answer.
func cqIDs(o cqSearchOut) []string {
	ids := make([]string, 0, len(o.Data.Hits))
	for _, h := range o.Data.Hits {
		ids = append(ids, h.ID)
	}
	sort.Strings(ids)
	return ids
}

// cqSorted returns a sorted copy.
func cqSorted(s ...string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// cqEqual reports whether two string slices hold the same values in order.
func cqEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cqSchemaOf returns the declared JSON schema of method path for status,
// with a resolver for its $refs; ok false when the document declares none.
func cqSchemaOf(doc restOAPI, method, path string, status string) (*restSchema, func(string) *restSchema, bool) {
	resolve := func(ref string) *restSchema {
		return doc.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	o, ok := doc.Paths[path][strings.ToLower(method)]
	if !ok {
		return nil, resolve, false
	}
	resp, ok := o.Responses[status]
	if !ok {
		return nil, resolve, false
	}
	media, ok := resp.Content["application/json"]
	if !ok || media.Schema == nil {
		return nil, resolve, false
	}
	return media.Schema, resolve, true
}

// cqCheckReply fails unless rep's status is declared for method path and
// its body matches the declared schema.
func cqCheckReply(t *testing.T, doc restOAPI, what, method, path string, rep restReply) {
	t.Helper()
	s, resolve, ok := cqSchemaOf(doc, method, path, strconv.Itoa(rep.status))
	if !ok {
		t.Errorf("%s: answered %d, which %s %s does not declare: %.300s", what, rep.status, method, path, rep.body)
		return
	}
	for _, p := range restSchemaCheck(s, json.RawMessage(rep.body), "$", resolve) {
		t.Errorf("%s (%d): %s", what, rep.status, p)
	}
}
