//go:build userscen

package userscen

// Knowledge campaign: the kind-specific verbs, each as a person meets it.
//   - directive show / install: run twice is a no-op, --json shape, a
//     --target lore cannot write is E_BAD_PATH with the OS reason and a
//     hint (it was E_INTERNAL "write <path>" with no reason);
//   - explain: a typo'd id is E_INVALID_ID (it printed "not found" and
//     exited 0); a row added and not committed yet is on this branch's
//     working tree (it said "not found in live or on this branch");
//   - memory move: to the store already holding it is a clear refusal (it
//     was E_NOT_FOUND "no row … outside repo" with a landed-on-main hint);
//     to the personal store moves it; invalidate twice keeps the first
//     valid_until;
//   - handoff add / list / show / search / ack twice / delete / restore;
//   - lint run, pii-pattern, why-context, techdoc with odd values.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestCampaignKnowledge_Directive(t *testing.T) {
	_, _, c := knowProject(t)
	c.Lore("directive", "show").OK(t).Says(t, "lore:directive:start", "AGENT DIRECTIVE")
	full := c.Lore("directive", "show", "--full").OK(t).Stdout
	if len(full) <= len(c.Lore("directive", "show").OK(t).Stdout) {
		t.Fatalf("--full is not longer than the compact block")
	}
	before := campTreeHash(t, c.Dir)
	c.Lore("directive", "show", "--json").OK(t).Says(t, `"block"`)
	if campTreeHash(t, c.Dir) != before {
		t.Fatalf("directive show wrote a file")
	}

	type files struct {
		Files []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"files"`
	}
	first := th.DecodeJSON[files](t, c.Lore("directive", "install", "--target", "NOTES.md", "--json"))
	if len(first.Files) != 1 || !strings.HasSuffix(first.Files[0].Path, "NOTES.md") || first.Files[0].Status == "unchanged" {
		t.Fatalf("first install: %+v", first)
	}
	body, _ := c.ReadFile("NOTES.md")
	again := th.DecodeJSON[files](t, c.Lore("directive", "install", "--target", "NOTES.md", "--json"))
	if len(again.Files) != 1 || again.Files[0].Status != "unchanged" {
		t.Fatalf("second install is not a no-op: %+v", again)
	}
	if b2, _ := c.ReadFile("NOTES.md"); b2 != body {
		t.Fatalf("second install changed the file")
	}
	// a file with the person's own text keeps it below the block
	c.WriteFile("MINE.md", "# my notes\n\nkeep me\n")
	c.Lore("directive", "install", "--target", "MINE.md").OK(t)
	if b, _ := c.ReadFile("MINE.md"); !strings.Contains(b, "keep me") || !strings.Contains(b, "lore:directive:start") {
		t.Fatalf("install lost the person's text or did not add the block:\n%s", b)
	}
	c.Lore("directive", "install", "--target", "spaced dir/My Notes.md").OK(t)
	if _, ok := c.ReadFile("spaced dir/My Notes.md"); !ok {
		t.Fatalf("a target with spaces was not written")
	}

	c.WriteFile("afile", "x")
	bad := []struct{ name, target, code, says string }{
		{"a folder", ".lore", "E_BAD_PATH", "is a directory"},
		{"through a file", "afile/x.md", "E_BAD_PATH", "not a directory"},
		{"unknown profile", "nosuchtool", "E_INVALID_INPUT", "unknown tool profile"},
	}
	for _, b := range bad {
		t.Run(b.name, func(t *testing.T) {
			r := c.Lore("directive", "install", "--target", b.target).Fails(t, 20, b.code, b.says)
			if !strings.Contains(r.Stderr, "hint:") {
				t.Fatalf("no hint:\n%s", r)
			}
			e := knowJSONErr(t, c.Lore("directive", "install", "--target", b.target, "--json"))
			if e.Error.Code != b.code || e.Error.Hint == "" {
				t.Fatalf("--json error %+v", e.Error)
			}
		})
	}
}

func TestCampaignKnowledge_Explain(t *testing.T) {
	_, _, c := knowProject(t)
	for _, junk := range []string{"junk", "mem_", "rul_xyz", "../../etc/passwd"} {
		r := c.Lore("explain", junk).Fails(t, 20, "E_INVALID_ID")
		if !strings.Contains(r.Stderr, "hint:") {
			t.Fatalf("explain %q: no hint:\n%s", junk, r)
		}
		c.Lore("explain", "run", junk, "--json").Fails(t, 20, "E_INVALID_ID")
	}
	c.Lore("explain").Fails(t, 20, "E_INVALID_INPUT")

	k := knowKindByNoun(t, "memory")
	id := campMustAdd(t, c, k, "", "explain this one")
	c.Lore("explain", id).OK(t).Says(t, id, "working tree, not committed yet", "wins: this branch's copy").
		Never(t, "not found in live or on this branch")
	c.CommitAll("add a memory")
	c.Lore("explain", "run", id).OK(t).Says(t, "this branch (HEAD)", "wins: this branch's copy").Never(t, "working tree")
	// a well-formed id no row has is still the "found nowhere" report
	c.Lore("explain", knowUnknownID(id)).OK(t).Says(t, "not found in live or on this branch")

	rid := th.DecodeJSON[knowAdded](t, c.Lore("rule", "add", "--body", "a live rule", "--live", "--json")).ID
	c.Lore("explain", rid).OK(t).Says(t, rid, "live (local")
}

func TestCampaignKnowledge_MemoryMoveInvalidate(t *testing.T) {
	_, _, c := knowProject(t)
	k := knowKindByNoun(t, "memory")
	id := campMustAdd(t, c, k, "", "a memory to move")

	c.Lore("memory", "move", id).Fails(t, 20, "E_INVALID_INPUT", "--to")
	r := c.Lore("memory", "move", id, "--to", "repo").Fails(t, 20, "E_INVALID_INPUT", "already in store repo")
	if strings.Contains(r.Stderr, "sync peek") {
		t.Fatalf("moving to its own store points at another branch:\n%s", r)
	}
	c.Lore("memory", "move", id, "--to", "nosuchstore").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "no store named nosuchstore")
	c.Lore("memory", "move", "junk", "--to", "me").Fails(t, 20, "E_INVALID_ID", "lore memory list")

	c.Lore("memory", "move", id, "--to", "me").OK(t).Says(t, "moved to me")
	if got := knowShowRow(t, c, k, id).Body; got != "a memory to move" {
		t.Fatalf("moved memory body %q", got)
	}
	c.Lore("memory", "move", id, "--to", "me").Fails(t, 20, "already in store me")
	// a row never returns to a store it left (by design: the old copy is a
	// tombstone there); the refusal says where it stays
	c.Lore("memory", "move", id, "--to", "repo").Fails(t, 23, "E_UNSUPPORTED", "never returns", "keep it in me")

	type valid struct {
		Data struct {
			ValidUntil string `json:"valid_until"`
		} `json:"data"`
	}
	c.Lore("memory", "invalidate", id).OK(t).Says(t, "invalidated")
	first := th.DecodeJSON[valid](t, c.Lore("memory", "show", id, "--json")).Data.ValidUntil
	if first == "" {
		t.Fatalf("invalidate set no valid_until")
	}
	c.Lore("memory", "invalidate", id).OK(t).Says(t, "already invalid since", "nothing written")
	if again := th.DecodeJSON[valid](t, c.Lore("memory", "show", id, "--json")).Data.ValidUntil; again != first {
		t.Fatalf("a second invalidate moved valid_until %s → %s", first, again)
	}
	c.Lore("memory", "invalidate", knowUnknownID(id)).Fails(t, th.ExitNotFound, "E_NOT_FOUND")
}

func TestCampaignKnowledge_Handoff(t *testing.T) {
	_, _, c := knowProject(t)
	c.MustLore("actor", "register")
	c.Lore("handoff", "add", "nobody named").Fails(t, 20, "E_INVALID_INPUT", "--for")
	c.Lore("handoff", "add", "--for", "@nobody", "x").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore actor register")
	c.Lore("handoff", "add", "--for", "@person", "--body", "   ").Fails(t, 20)

	for _, v := range campOddValues {
		t.Run(v.name, func(t *testing.T) {
			id := th.DecodeJSON[knowAdded](t, c.Lore("handoff", "add", "--for", "@person", "--body="+v.in, "--json")).ID
			if !strings.HasPrefix(id, "hnd_") {
				t.Fatalf("handoff id %q", id)
			}
			s := th.DecodeJSON[knowShow](t, c.Lore("handoff", "show", id, "--json"))
			if s.Data.Body != v.want {
				t.Fatalf("handoff body %q", knowCut(s.Data.Body))
			}
		})
	}
	id := th.DecodeJSON[knowAdded](t, c.Lore("handoff", "add", "--for", "@person", "--json", "finish the schema migration")).ID
	c.Lore("handoff", "list").OK(t).Says(t, id)
	c.Lore("handoff", "search", "migration", "--json").OK(t).Says(t, id)
	c.Lore("handoff", "ack", id).OK(t)
	if st := th.DecodeJSON[knowShow](t, c.Lore("handoff", "show", id, "--json")).Data.StatusStr; st != "acked" {
		t.Fatalf("status after ack %q", st)
	}
	c.Lore("handoff", "ack", id).OK(t) // a second ack keeps it acked
	if st := th.DecodeJSON[knowShow](t, c.Lore("handoff", "show", id, "--json")).Data.StatusStr; st != "acked" {
		t.Fatalf("status after a second ack %q", st)
	}
	c.Lore("handoff", "ack", "junk").Fails(t, 20, "E_INVALID_ID")
	c.Lore("handoff", "ack", knowUnknownID(id)).Fails(t, th.ExitNotFound, "E_NOT_FOUND")

	c.Lore("handoff", "delete", id).Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", id)
	c.Lore("handoff", "delete", id, "--yes").OK(t)
	c.Lore("handoff", "list").OK(t).Never(t, id)
	c.Lore("trash", "restore", id, "--yes").OK(t)
	c.Lore("handoff", "list").OK(t).Says(t, id)
}

func TestCampaignKnowledge_LintPiiWhyContext(t *testing.T) {
	_, _, c := knowProject(t)
	type lint struct {
		Findings []struct {
			Code string `json:"code"`
		} `json:"findings"`
		Count int `json:"count"`
	}
	l := th.DecodeJSON[lint](t, c.Lore("lint", "run", "--json"))
	if l.Count != len(l.Findings) {
		t.Fatalf("lint --json count %d with %d findings", l.Count, len(l.Findings))
	}
	before := campTreeHash(t, c.Dir)
	c.Lore("lint").OK(t)
	c.Lore("lint", "run").OK(t)
	if campTreeHash(t, c.Dir) != before {
		t.Fatalf("lint wrote to the repo")
	}

	// a ${row:…} pointing at a purged row is a dangling_ref with a fix
	k := knowKindByNoun(t, "memory")
	gone := campMustAdd(t, c, k, "", "soon gone")
	c.Lore("memory", "delete", gone, "--purge", "--confirm", "--yes").OK(t)
	holder := campMustAdd(t, c, k, "", "see ${row:"+gone+"} for the details")
	l = th.DecodeJSON[lint](t, c.Lore("lint", "run", "--json"))
	if l.Count != 1 || l.Findings[0].Code != "dangling_ref" {
		t.Fatalf("lint after a dangling ${row:…}: %+v", l)
	}
	c.Lore("lint").OK(t).Says(t, "dangling_ref", holder, gone)

	c.Lore("pii-pattern", "list").OK(t)
	c.Lore("pii-pattern", "list", "--json").OK(t)
	for verb, repl := range map[string]string{"add": "lore secrets rule add", "enable": "lore secrets rule on", "disable": "lore secrets rule off"} {
		r := c.Lore("pii-pattern", verb, "email").Fails(t, 20, "E_INVALID_INPUT", repl)
		if !strings.Contains(r.Stderr, "hint:") {
			t.Fatalf("pii-pattern %s: no hint:\n%s", verb, r)
		}
	}

	r := c.Lore("why-context", "show").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore render")
	if !strings.Contains(r.Stderr, "hint:") {
		t.Fatalf("why-context before a render: no hint:\n%s", r)
	}
	campMustAdd(t, c, knowKindByNoun(t, "rule"), "", "Never log tokens")
	c.MustLore("render")
	c.Lore("why-context", "show").OK(t).Says(t, "render_id:", ".lore/LORE.md", "scope:        1 rules, ")
	c.Lore("why-context", "show", "--json").OK(t)
	c.Lore("why-context", "show", "--cut").OK(t)
}

func TestCampaignKnowledge_Techdoc(t *testing.T) {
	_, _, c := knowProject(t)
	type doc struct {
		Data struct {
			Name    string `json:"name"`
			BaseURL string `json:"base_url"`
		} `json:"data"`
	}
	for _, v := range campOddValues {
		t.Run(v.name, func(t *testing.T) {
			id := th.DecodeJSON[knowAdded](t, c.Lore("techdoc", "add", "--name="+v.in, "--base-url", "https://docs.example.com/"+v.name, "--json")).ID
			d := th.DecodeJSON[doc](t, c.Lore("techdoc", "show", id, "--json"))
			if d.Data.Name != v.want || d.Data.BaseURL != "https://docs.example.com/"+v.name {
				t.Fatalf("techdoc stored name %q url %q", knowCut(d.Data.Name), d.Data.BaseURL)
			}
		})
	}
	c.Lore("techdoc", "add", "--base-url", "https://x.example.com").Fails(t, 20, "E_INVALID_INPUT", "name")
	c.Lore("techdoc", "add", "--help").OK(t).Never(t, "(required) (required)")
	id := th.DecodeJSON[knowAdded](t, c.Lore("techdoc", "add", "--name", "Go stdlib", "--base-url", "https://pkg.go.dev/std", "--json")).ID
	c.Lore("techdoc", "search", "stdlib", "--json").OK(t).Says(t, id)
	c.Lore("techdoc", "show", "junk").Fails(t, 20, "E_INVALID_ID", "lore techdoc list")
	c.Lore("techdoc", "show", knowUnknownID(id)).Fails(t, th.ExitNotFound, "E_NOT_FOUND")
	c.Lore("techdoc", "delete", id, "--yes").OK(t)
	c.Lore("techdoc", "list").OK(t).Never(t, id)
	c.Lore("trash", "restore", id, "--yes").OK(t)
	c.Lore("techdoc", "list").OK(t).Says(t, id)
}
