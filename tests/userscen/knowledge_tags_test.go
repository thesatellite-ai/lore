//go:build userscen

package userscen

// Tags, pins, comments, commit links and search through the real binary:
//   - tag add (name, color), list --json, a bad name refused with the
//     snake_case suggestion, a duplicate name refused naming the existing
//     tag and writing nothing
//   - add --tag and list --tag on memory, rule, decision and hotfix (AND of
//     several tags), an unknown tag refused with the create command and no
//     row written; tag edit --name keeps the tagged rows
//   - tag attach / detach on a row of every knowledge kind: attach twice is
//     one binding, detach twice reports 0 the second time, a wrong-kind id
//     is E_INVALID_ID, an unknown row E_NOT_FOUND, a trashed row E_IN_TRASH
//   - pin set full/line on a must rule and a critical/high hotfix, refused
//     on a should rule and a medium hotfix, bad --context refused with a
//     hint that runs; the help example runs; unset twice is harmless
//   - comment add/list/search/delete on a row, --json shapes, blank body
//     refused, unknown or trashed target refused, list filtered by --on-id
//   - link add a real commit to a decision and a memory, list by entity and
//     by commit, commit show, remove asks first then keeps a tombstone
//   - search: ranking (a title hit and two-word hit beat a one-word body
//     hit), --tables narrowing, --limit, empty results exit 0 "(no
//     matches)", --json shape with data.count and hits, a blank query
//     refused, unknown --tables refused, trashed rows only with
//     --include-deleted

import (
	"regexp"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

type knowTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type knowTagList struct {
	Count int       `json:"count"`
	Data  []knowTag `json:"data"`
}

func TestKnowledgeTagCatalogue(t *testing.T) {
	_, _, c := knowProject(t)
	type added struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	a := th.DecodeJSON[added](t, c.Lore("tag", "add", "--name", "cert_rotation", "--color", "#ff8800", "--json"))
	if !strings.HasPrefix(a.ID, "tag_") || a.Name != "cert_rotation" {
		t.Fatalf("tag add --json: %+v", a)
	}
	l := th.DecodeJSON[knowTagList](t, c.Lore("tag", "list", "--json"))
	if l.Count != 1 || l.Data[0].Name != "cert_rotation" || l.Data[0].Color != "#ff8800" {
		t.Fatalf("tag list: %+v", l)
	}
	c.Lore("tag", "add", "--name", "Bad Name").Fails(t, 20, "E_INVALID_IDENTIFIER", "bad_name")
	c.Lore("tag", "add", "--name", "").Fails(t, 20, "E_INVALID_INPUT")
	c.Lore("tag", "add", "--name", "cert_rotation").Refused(t, a.ID).Never(t, "✓")
	if l := th.DecodeJSON[knowTagList](t, c.Lore("tag", "list", "--json")); l.Count != 1 {
		t.Fatalf("refused tag adds wrote rows: %+v", l)
	}
}

func TestKnowledgeTagFilters(t *testing.T) {
	_, _, c := knowProject(t)
	c.MustLore("tag", "add", "--name", "billing")
	c.MustLore("tag", "add", "--name", "urgent")
	for _, noun := range []string{"memory", "rule", "decision", "hotfix"} {
		t.Run(noun, func(t *testing.T) {
			k := knowKindByNoun(t, noun)
			both := knowAdd(t, c, k, "Both", "both tags", "--tag", "billing", "--tag", "urgent")
			one := knowAdd(t, c, k, "One", "one tag", "--tag", "billing")
			none := knowAdd(t, c, k, "None", "no tag")
			if got := knowListRows(t, c, k, "--tag", "billing"); !got.has(both) || !got.has(one) || got.has(none) {
				t.Fatalf("--tag billing: %v", got.ids())
			}
			if got := knowListRows(t, c, k, "--tag", "billing", "--tag", "urgent"); got.Count != 1 || !got.has(both) {
				t.Fatalf("--tag billing --tag urgent (all must match): %v", got.ids())
			}
			before := knowListRows(t, c, k).Count
			args := []string{noun, "add", "--body", "x", "--tag", "nosuch"}
			if k.addTitle {
				args = append(args, "--title", "x")
			}
			c.Lore(args...).Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore tag add --name nosuch").Never(t, "✓")
			if after := knowListRows(t, c, k).Count; after != before {
				t.Fatalf("an add with an unknown tag wrote a row: %d → %d", before, after)
			}
			// edit --untag / --tag change the bindings.
			c.Lore(noun, "edit", one, "--untag", "billing").OK(t)
			if got := knowListRows(t, c, k, "--tag", "billing"); got.has(one) {
				t.Fatalf("--untag kept the binding: %v", got.ids())
			}
			c.Lore(noun, "edit", none, "--tag", "urgent").OK(t)
			if got := knowListRows(t, c, k, "--tag", "urgent"); !got.has(none) {
				t.Fatalf("edit --tag did not bind: %v", got.ids())
			}
		})
	}
	// Renaming a tag keeps every tagged row.
	c.MustLore("tag", "edit", "billing", "--name", "payments")
	k := knowKindByNoun(t, "memory")
	if got := knowListRows(t, c, k, "--tag", "payments"); got.Count != 1 {
		t.Fatalf("after renaming billing → payments: %v", got.ids())
	}
	// The old name no longer matches the renamed tag: not in a filter, and
	// an --untag by the old name leaves the renamed binding alone.
	if r := c.Lore("memory", "list", "--tag", "billing"); r.Code == 0 && strings.Contains(r.Stdout, "mem_") {
		t.Fatalf("the old tag name still filters rows after the rename: %s", r)
	}
	tagged := knowListRows(t, c, k, "--tag", "payments").Data[0].ID
	c.Lore("memory", "edit", tagged, "--untag", "billing")
	if got := knowListRows(t, c, k, "--tag", "payments"); !got.has(tagged) {
		t.Fatalf("--untag of the old name dropped the renamed tag: %v", got.ids())
	}
	// A new tag may take the freed name; it tags nothing yet.
	c.MustLore("tag", "add", "--name", "billing")
	if got := knowListRows(t, c, k, "--tag", "billing"); got.Count != 0 {
		t.Fatalf("a new tag billing matches rows of the renamed one: %v", got.ids())
	}
}

func TestKnowledgeTagAttachEveryKind(t *testing.T) {
	scenParallel(t)
	type bound struct {
		Tag     string `json:"tag"`
		OnTable string `json:"on_table"`
		OnID    string `json:"on_id"`
	}
	for _, k := range knowKinds {
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t) // one project per kind: the kinds run in parallel
			c.MustLore("tag", "add", "--name", "topic")
			id := knowAdd(t, c, k, "Attach "+k.noun, "attach body")
			b := th.DecodeJSON[bound](t, c.Lore("tag", "attach", "--on-table", k.table, "--on-id", id, "--tag", "topic", "--json"))
			if b.Tag != "topic" || b.OnTable != k.table || b.OnID != id {
				t.Fatalf("attach --json: %+v", b)
			}
			c.Lore("tag", "attach", "--on-table", k.table, "--on-id", id, "--tag", "topic").OK(t)
			c.Lore("tag", "detach", "--on-table", k.table, "--on-id", id, "--tag", "topic").OK(t).Says(t, "detached 1")
			c.Lore("tag", "detach", "--on-table", k.table, "--on-id", id, "--tag", "topic").OK(t).Says(t, "detached 0")
			c.Lore("tag", "attach", "--on-table", k.table, "--on-id", "tsk"+id[len(k.prefix):], "--tag", "topic").Fails(t, 20, "E_INVALID_ID", k.prefix+"_")
			c.Lore("tag", "attach", "--on-table", k.table, "--on-id", knowUnknownID(id), "--tag", "topic").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
			c.Lore("tag", "attach", "--on-table", k.table, "--on-id", id, "--tag", "nosuch").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
			c.MustLore(k.noun, "delete", id, "--yes")
			c.Lore("tag", "attach", "--on-table", k.table, "--on-id", id, "--tag", "topic").Fails(t, th.ExitRefused, "E_IN_TRASH")
		})
	}
}

func TestKnowledgePins(t *testing.T) {
	_, _, c := knowProject(t)
	rule := knowKindByNoun(t, "rule")
	hotfix := knowKindByNoun(t, "hotfix")
	must := knowAdd(t, c, rule, "", "Never log tokens")
	should := knowAdd(t, c, rule, "", "Prefer small PRs", "--severity", "should")
	critical := knowAdd(t, c, hotfix, "Prod DB", "do not run migrations by hand", "--severity", "critical")
	high := knowAdd(t, c, hotfix, "Flaky CI", "rerun once")
	medium := knowAdd(t, c, hotfix, "Slow tests", "be patient", "--severity", "medium")
	type pinned struct {
		ID      string `json:"id"`
		Context string `json:"context"`
	}
	for _, id := range []string{must, critical, high} {
		for _, form := range []string{"full", "line"} {
			p := th.DecodeJSON[pinned](t, c.Lore("pin", "set", id, "--context", form, "--json"))
			if p.ID != id || p.Context != form {
				t.Fatalf("pin set %s --context %s: %+v", id, form, p)
			}
		}
		if p := th.DecodeJSON[pinned](t, c.Lore("pin", "unset", id, "--json")); p.ID != id || p.Context != "" {
			t.Fatalf("pin unset: %+v", p)
		}
		c.Lore("pin", "unset", id).OK(t).Says(t, "unpinned")
	}
	for _, id := range []string{should, medium} {
		c.Lore("pin", "set", id, "--context", "full").Fails(t, 20, "E_INVALID_INPUT", "only must rules and critical/high hotfixes")
	}
	c.Lore("pin", "set", knowUnknownID(must), "--context", "full").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	mem := knowAdd(t, c, knowKindByNoun(t, "memory"), "", "not pinnable")
	c.Lore("pin", "set", mem, "--context", "full").Refused(t, "E_")

	// A bad --context is refused, and the command its hint names runs.
	bad := c.Lore("pin", "set", must, "--context", "bogus").Fails(t, 20, "E_INVALID_INPUT", "full or line")
	hint := regexp.MustCompile("lore pin[^\n]*--context full").FindString(bad.Out())
	if hint == "" {
		t.Fatalf("no runnable hint: %s", bad)
	}
	c.Lore(strings.Fields(strings.Replace(strings.TrimPrefix(hint, "lore "), "<id>", must, 1))...).OK(t).Says(t, "pinned")
	// The help example runs too, with a real id in place of the sample.
	help := c.Lore("pin", "set", "--help").OK(t).Stdout
	ex := regexp.MustCompile(`lore pin[^\n]*--context full`).FindString(help)
	if ex == "" {
		t.Fatalf("pin set --help has no example: %s", help)
	}
	ex = regexp.MustCompile(`rul_[0-9a-f]{32}`).ReplaceAllString(ex, must)
	c.Lore(strings.Fields(strings.TrimPrefix(ex, "lore "))...).OK(t).Says(t, "pinned")
}

type knowComment struct {
	ID        string `json:"id"`
	Table     string `json:"entity_table"`
	EntityID  string `json:"entity_id"`
	Body      string `json:"body"`
	CreatedBy string `json:"created_by_actor_id"`
}

type knowCommentList struct {
	Kind  string        `json:"kind"`
	Count int           `json:"count"`
	Data  []knowComment `json:"data"`
}

// knowCommentFound is the comment search --json answer.
type knowCommentFound struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	Data  []struct {
		ID      string `json:"id"`
		Snippet string `json:"snippet"`
	} `json:"data"`
}

// knowCommentText is the comment every scenario leaves and searches for.
const knowCommentText = "agreed; revisit Q3 🚀"

// Each kind runs in its own project, in parallel. The neighbour row of
// another kind carries the same comment, so --on-id must filter it out and
// search must find the comments of both tables, including the one on k's
// row after that row went to the trash (as the shared project checked it
// across every kind at the end).
func TestKnowledgeComments(t *testing.T) {
	scenParallel(t)
	for _, k := range knowKinds {
		t.Run(k.noun, func(t *testing.T) {
			_, a, c := knowProject(t)
			agent := c.As(a.Agent("claude"))
			type added struct {
				ID      string `json:"id"`
				OnTable string `json:"on_table"`
				OnID    string `json:"on_id"`
			}
			nid, nk := knowNeighbour(t, c, k, "Neighbour", "a row of another kind")
			c.MustLore("comment", "add", "--on-table", nk.table, "--on-id", nid, "--body", knowCommentText)
			id := knowAdd(t, c, k, "Commented "+k.noun, "commented body")
			mine := th.DecodeJSON[added](t, c.Lore("comment", "add", "--on-table", k.table, "--on-id", id, "--body", knowCommentText, "--json"))
			if !strings.HasPrefix(mine.ID, "cmt_") || mine.OnTable != k.table || mine.OnID != id {
				t.Fatalf("comment add --json: %+v", mine)
			}
			theirs := th.DecodeJSON[added](t, agent.Lore("comment", "add", "--on-table", k.table, "--on-id", id, "--body", "agent note", "--json"))
			l := th.DecodeJSON[knowCommentList](t, c.Lore("comment", "list", "--on-id", id, "--json"))
			if l.Kind != "comment.list" || l.Count != 2 {
				t.Fatalf("comment list --on-id: %+v", l)
			}
			by := map[string]knowComment{}
			for _, cm := range l.Data {
				by[cm.ID] = cm
			}
			if got := by[mine.ID]; got.Body != knowCommentText || got.Table != k.table || got.EntityID != id {
				t.Fatalf("listed comment: %+v", got)
			}
			if by[mine.ID].CreatedBy == by[theirs.ID].CreatedBy {
				t.Fatalf("person and agent comments carry the same actor %s", by[mine.ID].CreatedBy)
			}
			c.Lore("comment", "add", "--on-table", k.table, "--on-id", id, "--body", "  \n").Fails(t, 20, "E_EMPTY_BODY")
			c.Lore("comment", "add", "--on-table", k.table, "--on-id", knowUnknownID(id), "--body", "x").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
			c.Lore("comment", "add", "--on-table", k.table, "--on-id", "tsk"+id[len(k.prefix):], "--body", "x").Fails(t, 20, "E_INVALID_ID")
			// delete asks first, then the comment leaves the list.
			c.Lore("comment", "delete", theirs.ID).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
			c.Lore("comment", "delete", theirs.ID, "--yes").OK(t)
			if l := th.DecodeJSON[knowCommentList](t, c.Lore("comment", "list", "--on-id", id, "--json")); l.Count != 1 || l.Data[0].ID != mine.ID {
				t.Fatalf("after deleting one comment: %+v", l)
			}
			// A trashed target takes no new comment.
			c.MustLore(k.noun, "delete", id, "--yes")
			c.Lore("comment", "add", "--on-table", k.table, "--on-id", id, "--body", "late").Fails(t, th.ExitRefused, "E_IN_TRASH")

			// Search spans tables: k's comment (its row now trashed) and the
			// neighbour's; the deleted agent comment is gone.
			f := th.DecodeJSON[knowCommentFound](t, c.Lore("comment", "search", "revisit", "--json"))
			if f.Kind != "comment.search" || f.Count != 2 || !strings.Contains(f.Data[0].Snippet, "revisit") {
				t.Fatalf("comment search: %+v", f)
			}
			found := map[string]bool{}
			for _, h := range f.Data {
				found[h.ID] = true
			}
			if !found[mine.ID] {
				t.Fatalf("comment search misses %s's comment %s: %+v", k.noun, mine.ID, f)
			}
			c.Lore("comment", "search", "zzznothing").OK(t)
			c.Lore("comment", "search", "   ").Fails(t, 20, "E_INVALID_INPUT")
		})
	}
}

func TestKnowledgeCommitLinks(t *testing.T) {
	_, _, c := knowProject(t)
	dec := knowAdd(t, c, knowKindByNoun(t, "decision"), "Use SQLite", "local first")
	mem := knowAdd(t, c, knowKindByNoun(t, "memory"), "", "gateway retries")
	c.WriteFile("main.go", "package main\n")
	c.CommitAll("fix the gateway retry")
	head := c.Head()
	type link struct {
		ID     string `json:"id"`
		Entity string `json:"entity_id"`
		SHA    string `json:"sha"`
	}
	type links struct {
		Kind string `json:"kind"`
		Data []link `json:"data"`
	}
	for _, id := range []string{dec, mem} {
		c.Lore("link", "add", "--entity", id, "--commit", "HEAD").OK(t).Says(t, head[:7], id)
		l := th.DecodeJSON[links](t, c.Lore("link", "list", "--entity", id, "--json"))
		if l.Kind != "link.list" || len(l.Data) != 1 || !strings.HasPrefix(l.Data[0].SHA, head[:7]) {
			t.Fatalf("link list --entity %s: %+v", id, l)
		}
	}
	byCommit := th.DecodeJSON[links](t, c.Lore("link", "list", "--commit", head, "--json"))
	if len(byCommit.Data) != 2 {
		t.Fatalf("link list --commit: %+v", byCommit)
	}
	c.Lore("commit", "show", head).OK(t).Says(t, dec, mem)
	// Unknown entity, wrong-shaped entity.
	c.Lore("link", "add", "--entity", knowUnknownID(dec), "--commit", "HEAD").Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	c.Lore("link", "add", "--entity", "garbage", "--commit", "HEAD").Fails(t, 20, "garbage")
	// Remove asks first, then the link is gone from the list.
	lid := th.DecodeJSON[links](t, c.Lore("link", "list", "--entity", dec, "--json")).Data[0].ID
	c.Lore("link", "remove", lid).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM")
	c.Lore("link", "remove", lid, "--yes").OK(t)
	if l := th.DecodeJSON[links](t, c.Lore("link", "list", "--entity", dec, "--json")); len(l.Data) != 0 {
		t.Fatalf("removed link still listed: %+v", l)
	}
}

type knowHit struct {
	Table string  `json:"entity_table"`
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type knowSearch struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          struct {
		Query string    `json:"query"`
		Count int       `json:"count"`
		Hits  []knowHit `json:"hits"`
	} `json:"data"`
}

func TestKnowledgeSearch(t *testing.T) {
	_, _, c := knowProject(t)
	mem := knowKindByNoun(t, "memory")
	rule := knowKindByNoun(t, "rule")
	dec := knowKindByNoun(t, "decision")
	strong := knowAdd(t, c, dec, "Invoice rounding", "invoice rounding uses banker's rounding for every invoice")
	weak := knowAdd(t, c, mem, "", "the rounding of numbers in charts is cosmetic")
	ruleHit := knowAdd(t, c, rule, "", "Never round an invoice total twice")
	knowAdd(t, c, mem, "", "unrelated text about deploys")

	s := th.DecodeJSON[knowSearch](t, c.Lore("search", "invoice rounding", "--json"))
	if s.SchemaVersion != 1 || s.Kind != "search.global" || s.Data.Query != "invoice rounding" || s.Data.Count != len(s.Data.Hits) {
		t.Fatalf("search --json envelope: %+v", s)
	}
	if len(s.Data.Hits) == 0 || s.Data.Hits[0].ID != strong {
		t.Fatalf("the row matching both words in title and body must rank first: %+v", s.Data.Hits)
	}
	for _, h := range s.Data.Hits {
		if h.ID == weak {
			t.Fatalf("a row without the word invoice matched an AND query: %+v", s.Data.Hits)
		}
	}
	// --tables narrows to one kind; --limit caps the merge.
	only := th.DecodeJSON[knowSearch](t, c.Lore("search", "invoice", "--tables", "rule", "--json"))
	if len(only.Data.Hits) != 1 || only.Data.Hits[0].ID != ruleHit || only.Data.Hits[0].Table != "rules" {
		t.Fatalf("--tables rule: %+v", only.Data.Hits)
	}
	if lim := th.DecodeJSON[knowSearch](t, c.Lore("search", "rounding", "--limit", "1", "--json")); len(lim.Data.Hits) != 1 {
		t.Fatalf("--limit 1 gave %d hits", len(lim.Data.Hits))
	}
	// Prefix query.
	c.Lore("search", "invoi*").OK(t).Says(t, strong)
	// No results: exit 0 and "(no matches)"; JSON count 0 with an empty list.
	c.Lore("search", "zzznothing").OK(t).Says(t, "(no matches)")
	empty := th.DecodeJSON[knowSearch](t, c.Lore("search", "zzznothing", "--json"))
	if empty.Data.Count != 0 || empty.Data.Hits == nil || len(empty.Data.Hits) != 0 {
		t.Fatalf("empty search --json: %+v (hits must be [] not null)", empty)
	}
	c.Lore("search", "").Fails(t, 20, "E_INVALID_INPUT")
	c.Lore("search", "x", "--tables", "bogus").Fails(t, 20, "E_INVALID_INPUT", "--tables")
	// Per-kind search finds the same rows.
	c.Lore("decision", "search", "rounding").OK(t).Says(t, strong)
	// Trashed rows only with --include-deleted.
	c.MustLore("rule", "delete", ruleHit, "--yes")
	c.Lore("search", "invoice", "--tables", "rule").OK(t).Never(t, ruleHit)
	c.Lore("search", "invoice", "--tables", "rule", "--include-deleted").OK(t).Says(t, ruleHit)
	// Edited text is found under the new words, not the old.
	c.MustLore("memory", "edit", weak, "--body", "chart labels use thousands separators")
	c.Lore("search", "separators").OK(t).Says(t, weak)
	c.Lore("search", "cosmetic").OK(t).Says(t, "(no matches)")
}
