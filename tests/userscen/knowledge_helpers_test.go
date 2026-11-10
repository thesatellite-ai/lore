//go:build userscen

package userscen

// Helpers shared by the knowledge-area scenarios (knowledge_*_test.go):
// a person with a local personal store working in an initialised code
// repo, the table of every knowledge kind with the flags a user types for
// it, and small JSON shapes the docs promise for add, show and list.

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// knowKind is one knowledge noun as a user drives it from the CLI.
type knowKind struct {
	noun     string // CLI noun: lore <noun> add
	prefix   string // id prefix: mem, rul, …
	table    string // table name for --on-table (tags, comments)
	addTitle bool   // add needs --title
	titleKey string // field holding the title in show --json ("" when none)
	editFlag string // edit flag that changes the title ("" when none)
	canEdit  bool   // lore <noun> edit exists
	archive  bool   // lore <noun> archive / unarchive exist
	creator  bool   // show --json carries created_by_actor_id
	textBody bool   // show without --json prints the body (every kind since INTEG-KNOWLEDGE-3; v0.1.17 printed only "XX  <id>" for ten of them)
}

// knowKinds is every knowledge kind with an add/show/list/delete lifecycle
// (techdoc takes --name/--base-url and has its own test).
var knowKinds = []knowKind{
	{noun: "memory", prefix: "mem", table: "memories", canEdit: true, archive: true, creator: true, textBody: true},
	{noun: "rule", prefix: "rul", table: "rules", canEdit: true, archive: true, creator: true, textBody: true},
	{noun: "decision", prefix: "dec", table: "decisions", addTitle: true, titleKey: "title", editFlag: "--title", canEdit: true, archive: true, creator: true, textBody: true},
	{noun: "hotfix", prefix: "hfx", table: "hotfixes", addTitle: true, titleKey: "title", editFlag: "--title", canEdit: true, archive: true, creator: true, textBody: true},
	{noun: "pattern", prefix: "pat", table: "patterns", addTitle: true, titleKey: "title", editFlag: "--title", canEdit: true, archive: true, creator: true, textBody: true},
	{noun: "playbook", prefix: "pbk", table: "playbooks", addTitle: true, titleKey: "name", editFlag: "--name", canEdit: true, archive: true, creator: true, textBody: true},
	{noun: "prompt", prefix: "prm", table: "prompts", addTitle: true, titleKey: "name", editFlag: "--name", canEdit: true, archive: true, creator: true, textBody: true},
	{noun: "architecturenote", prefix: "anr", table: "architecture_notes", addTitle: true, titleKey: "title", editFlag: "--title", canEdit: true, creator: true, textBody: true},
	{noun: "cookbookrecipe", prefix: "cbr", table: "cookbook_recipes", addTitle: true, titleKey: "title", editFlag: "--title", canEdit: true, creator: true, textBody: true},
	{noun: "incident", prefix: "inc", table: "incidents", addTitle: true, titleKey: "title", editFlag: "--title", canEdit: true, creator: true, textBody: true},
	{noun: "tastepref", prefix: "tst", table: "taste_prefs", canEdit: true, creator: true, textBody: true},
	{noun: "behaviour", prefix: "bhv", table: "behaviours", addTitle: true, titleKey: "name", editFlag: "--name", canEdit: true, creator: true, textBody: true},
	{noun: "snapshot", prefix: "snp", table: "snapshots", addTitle: true, titleKey: "title", archive: true, creator: true, textBody: true},
	{noun: "workflow", prefix: "wfl", table: "workflows", addTitle: true, titleKey: "name", editFlag: "--name", canEdit: true, textBody: true},
	{noun: "suggestion", prefix: "sug", table: "suggestions", addTitle: true, titleKey: "title", editFlag: "--title", canEdit: true, creator: true, textBody: true},
}

// knowAdded is the --json answer of every `lore <noun> add`.
type knowAdded struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

// knowRow holds the fields of a knowledge row the tests read; every kind
// fills a subset.
type knowRow struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	Title     string `json:"title"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by_actor_id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Kind      string `json:"kind"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
	StatusStr string `json:"status_str"`
	Source    string `json:"source_kind"`
	Language  string `json:"language"`
}

// title is the row's title whichever key the kind stores it under.
func (r knowRow) title(k knowKind) string {
	if k.titleKey == "name" {
		return r.Name
	}
	return r.Title
}

// knowShow is the show --json envelope.
type knowShow struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Data          knowRow `json:"data"`
}

// knowList is the list --json envelope.
type knowList struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count"`
	Data          []knowRow `json:"data"`
}

// ids lists the ids of a list in order.
func (l knowList) ids() []string {
	out := make([]string, 0, len(l.Data))
	for _, r := range l.Data {
		out = append(out, r.ID)
	}
	return out
}

// has reports whether the list holds id.
func (l knowList) has(id string) bool {
	for _, r := range l.Data {
		if r.ID == id {
			return true
		}
	}
	return false
}

// knowErr is the --json error envelope.
type knowErr struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Exit    int    `json:"exit"`
		Class   string `json:"class"`
	} `json:"error"`
}

// knowProject is one person (person@example.com) with a local personal
// store in an initialised code repo; it returns the clone they work in.
func knowProject(t *testing.T) (*th.World, *th.Actor, *th.Clone) {
	t.Helper()
	w := newWorld(t)
	a := w.Human("person", "person@example.com")
	a.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}})
	c := a.Clone(w.Remote("github.com/octo-personal/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	return w, a, c
}

// knowNeighbour adds one live row of a kind other than k (a memory, or a
// rule when k is memory) with the given title and body, and returns its id
// and kind. The per-kind scenarios each build their own project so the
// kinds run in parallel; the neighbour keeps what one shared project gave
// them for free: another table's row sits beside k's rows, so a list,
// count, comment filter or duplicate check of k that leaked rows of other
// kinds still fails.
func knowNeighbour(t *testing.T, c *th.Clone, k knowKind, title, body string) (string, knowKind) {
	t.Helper()
	other := knowKindByNoun(t, "memory")
	if k.noun == other.noun {
		other = knowKindByNoun(t, "rule")
	}
	return knowAdd(t, c, other, title, body), other
}

// knowAdd adds one row of kind k with body (and title when the kind needs
// one) plus extra flags, and returns its id.
func knowAdd(t *testing.T, c *th.Clone, k knowKind, title, body string, extra ...string) string {
	t.Helper()
	args := []string{k.noun, "add", "--body", body}
	if k.addTitle {
		args = append(args, "--title", title)
	}
	args = append(append(args, extra...), "--json")
	got := th.DecodeJSON[knowAdded](t, c.Lore(args...))
	if !strings.HasPrefix(got.ID, k.prefix+"_") {
		t.Fatalf("%s add: id %q does not start with %s_", k.noun, got.ID, k.prefix)
	}
	return got.ID
}

// knowShowRow runs show --json and returns the row.
func knowShowRow(t *testing.T, c *th.Clone, k knowKind, id string) knowRow {
	t.Helper()
	s := th.DecodeJSON[knowShow](t, c.Lore(k.noun, "show", id, "--json"))
	if s.SchemaVersion != 1 || s.Data.ID != id {
		t.Fatalf("%s show --json: schema %d id %q, want 1 and %s", k.noun, s.SchemaVersion, s.Data.ID, id)
	}
	return s.Data
}

// knowListRows runs list --json with extra flags.
func knowListRows(t *testing.T, c *th.Clone, k knowKind, extra ...string) knowList {
	t.Helper()
	l := th.DecodeJSON[knowList](t, c.Lore(append([]string{k.noun, "list", "--json"}, extra...)...))
	if l.SchemaVersion != 1 || l.Count != len(l.Data) {
		t.Fatalf("%s list --json: schema %d count %d with %d rows", k.noun, l.SchemaVersion, l.Count, len(l.Data))
	}
	return l
}

// knowJSONErr decodes a failed command's --json error (printed on stdout).
func knowJSONErr(t *testing.T, r th.Result) knowErr {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("expected a failure: %s", r)
	}
	var e knowErr
	if err := json.Unmarshal([]byte(r.Stdout), &e); err != nil {
		t.Fatalf("decode the JSON error of %s: %v", r, err)
	}
	if e.Error.Exit != r.Code {
		t.Fatalf("JSON error says exit %d, the process exited %d: %s", e.Error.Exit, r.Code, r)
	}
	return e
}

// knowUnknownID is a well-formed id of prefix p that no row has: a real
// id from this world with its last hex digit changed.
func knowUnknownID(real string) string {
	last := real[len(real)-1]
	repl := byte('0')
	if last == '0' {
		repl = '1'
	}
	return real[:len(real)-1] + string(repl)
}
