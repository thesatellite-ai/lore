//go:build userscen

package userscen

// Helpers of the knowledge campaign (campaign_knowledge_*_test.go): the
// 121 commands of the knowledge area as a person types them, the odd input
// values every add/edit/search is driven with, and a tree hash that proves a
// refused or dry-run command wrote nothing. The kind table itself is
// knowKinds (knowledge_helpers_test.go): the campaign drives the same kinds
// a user meets, as one matrix over kinds × verbs × input classes.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// campKnowCRUDNouns are the nouns whose commands share the generic CRUD
// shape; campKnowVerbs lists each noun's verbs as `lore <noun> --help`
// names them (the inventory of the knowledge area, 2026-11-08).
var campKnowVerbs = map[string][]string{
	"architecturenote": {"add", "delete", "edit", "list", "search", "show"},
	"behaviour":        {"add", "delete", "edit", "list", "search", "show"},
	"cookbookrecipe":   {"add", "delete", "edit", "list", "search", "show"},
	"decision":         {"add", "archive", "delete", "edit", "list", "search", "show", "unarchive"},
	"directive":        {"install", "show"},
	"explain":          {"run"},
	"handoff":          {"ack", "add", "delete", "list", "search", "show"},
	"hotfix":           {"add", "archive", "delete", "edit", "list", "search", "show", "unarchive"},
	"incident":         {"add", "delete", "edit", "list", "search", "show"},
	"lint":             {"run"},
	"memory":           {"add", "archive", "delete", "edit", "invalidate", "list", "move", "search", "show", "unarchive"},
	"pattern":          {"add", "archive", "delete", "edit", "list", "search", "show", "unarchive"},
	"pii-pattern":      {"add", "disable", "enable", "list"},
	"playbook":         {"add", "archive", "delete", "edit", "list", "search", "show", "unarchive"},
	"prompt":           {"add", "archive", "delete", "edit", "list", "search", "show", "unarchive"},
	"rule":             {"add", "archive", "delete", "edit", "list", "search", "show", "unarchive"},
	"snapshot":         {"add", "archive", "delete", "list", "search", "show", "unarchive"},
	"suggestion":       {"add", "delete", "edit", "list", "search", "show"},
	"tastepref":        {"add", "delete", "edit", "list", "search", "show"},
	"techdoc":          {"add", "delete", "list", "search", "show"},
	"why-context":      {"show"},
}

// campKnowCommands is every command of the area as "noun verb", sorted.
func campKnowCommands() []string {
	var out []string
	for n, vs := range campKnowVerbs {
		for _, v := range vs {
			out = append(out, n+" "+v)
		}
	}
	sort.Strings(out)
	return out
}

// campKnowKinds is knowKinds without workflow (the workflow area owns it).
func campKnowKinds() []knowKind {
	var out []knowKind
	for _, k := range knowKinds {
		if k.noun != "workflow" {
			out = append(out, k)
		}
	}
	return out
}

// campOdd is one odd input value a person really types.
type campOdd struct {
	name string
	in   string
	want string // what show must give back (the input, trimmed)
}

// campOddValues are the input classes every text field is driven with.
var campOddValues = []campOdd{
	{"long10k", strings.Repeat("lorem ipsum ", 834)[:10000], strings.Repeat("lorem ipsum ", 834)[:10000]},
	{"rtl", "שלום עולם — مرحبا بالعالم", "שלום עולם — مرحبا بالعالم"},
	{"emoji", "deploy 🚀 then ✅", "deploy 🚀 then ✅"},
	// ZWJ is kept (I-4): a joined emoji comes back joined.
	{"emoji-zwj", "dev 👩\u200d💻", "dev 👩\u200d💻"},
	{"reserved", "true", "true"},
	{"reserved-sql", "DROP TABLE memories; --", "DROP TABLE memories; --"},
	{"spaces-slashes", "src/api handlers/v2 \\ path with spaces", "src/api handlers/v2 \\ path with spaces"},
	{"id-lookalike", "mem_00000000000000000000000000000000", "mem_00000000000000000000000000000000"},
}

// campTreeHash hashes every file under dir (names and bytes) except git's
// own folder and lore's lock and cache files, so a command that must write
// nothing can be checked for it.
func campTreeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if rel == ".git" || rel == filepath.Join(".lore", "state") {
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		if base == "lock" || strings.HasPrefix(base, "lore-cache.db") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		h.Write([]byte(rel + "\x00"))
		h.Write(b)
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", dir, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// campAddArgs is `lore <noun> add` with body (and title when the kind needs
// one) as --flag=value, so values that start with a dash stay values.
func campAddArgs(k knowKind, title, body string) []string {
	args := []string{k.noun, "add", "--body=" + body}
	if k.addTitle {
		args = append(args, "--title="+title)
	}
	return append(args, "--json")
}

// campMustAdd adds one row and returns its id.
func campMustAdd(t *testing.T, c *th.Clone, k knowKind, title, body string) string {
	t.Helper()
	got := th.DecodeJSON[knowAdded](t, c.Lore(campAddArgs(k, title, body)...))
	if !strings.HasPrefix(got.ID, k.prefix+"_") {
		t.Fatalf("%s add: id %q", k.noun, got.ID)
	}
	return got.ID
}
