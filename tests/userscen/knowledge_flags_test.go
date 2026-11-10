//go:build userscen

package userscen

// Knowledge flag scenarios through the real binary:
//   - closed-set flags (rule --severity, hotfix --severity, decision
//     --status, memory --kind): every allowed value round-trips into show
//     --json, the defaults are the documented ones, a wrong value is refused
//     with exit 20 naming the allowed set, and nothing is written
//   - list --kind and --sort created_at:asc|desc (with a pinned clock), a
//     bad sort direction refused
//   - memory --key: a retry returns the same id and writes nothing
//   - --supersedes on every kind the docs name: the old row records the new
//     id, a replaced decision becomes superseded
//   - memory invalidate stamps valid_until and keeps the row listed and
//     searchable (documented)
//   - delete --confirm works like --yes (v0.1.17 spelling, documented)
//   - odd text matrix: unicode, emoji, newlines, markdown fence, shell
//     characters, HTML, "null", a leading dash, 64 KiB; surrounding blanks
//     trimmed; a blank body refused with E_EMPTY_BODY, a blank title refused
//   - duplicate adds: the same text twice is two rows for every kind except
//     prompt (names are unique: refused, naming the existing id) and tag
//   - pattern / cookbookrecipe --language, playbook / prompt --description,
//     prompt --args-schema must be a JSON object
//   - person vs agent attribution: an agent session's rows carry another
//     actor id than its owner's, on every kind with a creator

import (
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func knowKindByNoun(t *testing.T, noun string) knowKind {
	t.Helper()
	for _, k := range knowKinds {
		if k.noun == noun {
			return k
		}
	}
	t.Fatalf("no knowledge kind %q", noun)
	return knowKind{}
}

func TestKnowledgeClosedSetFlags(t *testing.T) {
	_, _, c := knowProject(t)
	cases := []struct {
		noun, flag, field, def string
		values                 []string
	}{
		{"rule", "--severity", "severity", "must", []string{"must", "should", "may"}},
		{"hotfix", "--severity", "severity", "high", []string{"low", "medium", "high", "critical"}},
		{"decision", "--status", "status", "accepted", []string{"proposed", "accepted", "superseded", "deprecated"}},
		{"memory", "--kind", "kind", "retrieved", []string{"core", "retrieved", "episodic", "procedural", "archival"}},
	}
	pick := func(r knowRow, field string) string {
		switch field {
		case "severity":
			return r.Severity
		case "status":
			return r.Status
		}
		return r.Kind
	}
	for _, tc := range cases {
		t.Run(tc.noun, func(t *testing.T) {
			k := knowKindByNoun(t, tc.noun)
			def := knowAdd(t, c, k, "Default", "default value")
			if got := pick(knowShowRow(t, c, k, def), tc.field); got != tc.def {
				t.Fatalf("default %s = %q, want %q", tc.field, got, tc.def)
			}
			for _, v := range tc.values {
				id := knowAdd(t, c, k, "With "+v, "value "+v, tc.flag, v)
				if got := pick(knowShowRow(t, c, k, id), tc.field); got != v {
					t.Fatalf("%s %s stored %q", tc.flag, v, got)
				}
			}
			before := knowListRows(t, c, k).Count
			args := []string{tc.noun, "add", "--body", "bad value", tc.flag, "nope"}
			if k.addTitle {
				args = append(args, "--title", "Bad")
			}
			r := c.Lore(args...).Fails(t, 20, tc.values...)
			r.Never(t, "✓")
			if after := knowListRows(t, c, k).Count; after != before {
				t.Fatalf("a refused add wrote a row: %d → %d", before, after)
			}
			// An edit to a wrong value is refused the same way and changes nothing.
			if tc.noun != "memory" {
				c.Lore(tc.noun, "edit", def, tc.flag, "nope").Fails(t, 20, tc.values[0])
				if got := pick(knowShowRow(t, c, k, def), tc.field); got != tc.def {
					t.Fatalf("refused edit changed %s to %q", tc.field, got)
				}
			}
		})
	}
	// memory list --kind filters on the kind.
	k := knowKindByNoun(t, "memory")
	l := knowListRows(t, c, k, "--kind", "episodic")
	if l.Count != 1 || l.Data[0].Kind != "episodic" {
		t.Fatalf("memory list --kind episodic: %+v", l.Data)
	}
}

func TestKnowledgeListSortOrder(t *testing.T) {
	_, a, c := knowProject(t)
	a.SetNow(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC))
	for _, k := range []knowKind{knowKindByNoun(t, "memory"), knowKindByNoun(t, "decision"), knowKindByNoun(t, "pattern")} {
		t.Run(k.noun, func(t *testing.T) {
			var want []string
			for _, n := range []string{"one", "two", "three"} {
				a.Advance(time.Hour)
				want = append(want, knowAdd(t, c, k, "Sort "+n, "sorted "+n))
			}
			asc := knowListRows(t, c, k, "--sort", "created_at:asc").ids()
			if strings.Join(asc, ",") != strings.Join(want, ",") {
				t.Fatalf("created_at:asc %v, want %v", asc, want)
			}
			desc := knowListRows(t, c, k, "--sort", "created_at:desc").ids()
			if len(desc) != 3 || desc[0] != want[2] || desc[2] != want[0] {
				t.Fatalf("created_at:desc %v, want reverse of %v", desc, want)
			}
			c.Lore(k.noun, "list", "--sort", "created_at:sideways").Fails(t, 20, "E_INVALID_INPUT", "asc or desc")
		})
	}
}

func TestKnowledgeMemoryIdempotencyKey(t *testing.T) {
	_, _, c := knowProject(t)
	k := knowKindByNoun(t, "memory")
	first := th.DecodeJSON[knowAdded](t, c.Lore("memory", "add", "retry the gateway", "--key", "job-42", "--json"))
	type again struct {
		ID       string `json:"id"`
		Existing bool   `json:"existing"`
	}
	second := th.DecodeJSON[again](t, c.Lore("memory", "add", "retry the gateway", "--key", "job-42", "--json"))
	if second.ID != first.ID || !second.Existing {
		t.Fatalf("retry with the same --key: %+v, first %s", second, first.ID)
	}
	c.Lore("memory", "add", "retry the gateway", "--key", "job-42").OK(t).Says(t, first.ID, "nothing written")
	if l := knowListRows(t, c, k); l.Count != 1 {
		t.Fatalf("three adds under one key made %d rows", l.Count)
	}
	// Another key is another row.
	other := th.DecodeJSON[knowAdded](t, c.Lore("memory", "add", "retry the gateway", "--key", "job-43", "--json"))
	if other.ID == first.ID {
		t.Fatal("a different --key returned the same row")
	}
}

func TestKnowledgeSupersedes(t *testing.T) {
	_, _, c := knowProject(t)
	type sup struct {
		Data struct {
			Status       string `json:"status"`
			SupersededBy string `json:"superseded_by_id"`
		} `json:"data"`
	}
	for _, noun := range []string{"rule", "memory", "decision", "hotfix", "pattern"} {
		t.Run(noun, func(t *testing.T) {
			k := knowKindByNoun(t, noun)
			old := knowAdd(t, c, k, "Old "+noun, "old text")
			nw := knowAdd(t, c, k, "New "+noun, "new text", "--supersedes", old)
			got := th.DecodeJSON[sup](t, c.Lore(noun, "show", old, "--json"))
			if got.Data.SupersededBy != nw {
				t.Fatalf("old row superseded_by_id %q, want %s", got.Data.SupersededBy, nw)
			}
			if noun == "decision" && got.Data.Status != "superseded" {
				t.Fatalf("replaced decision status %q", got.Data.Status)
			}
			// Superseding an unknown id or an id of another kind is refused
			// and writes nothing.
			before := knowListRows(t, c, k).Count
			for _, target := range []string{knowUnknownID(old), "tsk" + old[len(k.prefix):]} {
				args := []string{noun, "add", "--body", "y", "--supersedes", target}
				if k.addTitle {
					args = append(args, "--title", "T")
				}
				c.Lore(args...).Refused(t, target).Never(t, "unknown flag")
			}
			if after := knowListRows(t, c, k).Count; after != before {
				t.Fatalf("refused --supersedes wrote rows: %d → %d", before, after)
			}
		})
	}
}

func TestKnowledgeMemoryInvalidateStaysListed(t *testing.T) {
	_, _, c := knowProject(t)
	k := knowKindByNoun(t, "memory")
	id := knowAdd(t, c, k, "", "the office is on floor three")
	c.Lore("memory", "invalidate", id).OK(t).Says(t, id, "invalidated")
	type valid struct {
		Data struct {
			ValidUntil string `json:"valid_until"`
		} `json:"data"`
	}
	if v := th.DecodeJSON[valid](t, c.Lore("memory", "show", id, "--json")); v.Data.ValidUntil == "" {
		t.Fatal("invalidate left valid_until empty")
	}
	c.Lore("memory", "show", id).OK(t).Says(t, "valid-until")
	if l := knowListRows(t, c, k); !l.has(id) {
		t.Fatal("an invalidated memory must stay listed (docs: archive it to hide it)")
	}
	c.Lore("search", "office").OK(t).Says(t, id)
	c.Lore("memory", "invalidate", knowUnknownID(id)).Fails(t, th.ExitNotFound, "E_NOT_FOUND")
}

func TestKnowledgeDeleteConfirmSpelling(t *testing.T) {
	_, _, c := knowProject(t)
	k := knowKindByNoun(t, "rule")
	id := knowAdd(t, c, k, "", "old spelling delete")
	c.Lore("rule", "delete", id, "--confirm").OK(t).Says(t, "trash")
	c.Lore("rule", "show", id).Fails(t, th.ExitRefused, "E_IN_TRASH")
}

func TestKnowledgeOddText(t *testing.T) {
	scenParallel(t)
	long := strings.Repeat("0123456789abcdef", 4096) // 64 KiB
	texts := []struct{ name, in, want string }{
		{"unicode", "日本語のメモ — ünïcødé", "日本語のメモ — ünïcødé"},
		{"emoji", "ship it 🚀✅", "ship it 🚀✅"},
		{"newlines", "line one\nline two\n\nline four", "line one\nline two\n\nline four"},
		{"markdown", "# Head\n\n- item\n\n```go\nfmt.Println(\"x\")\n```", "# Head\n\n- item\n\n```go\nfmt.Println(\"x\")\n```"},
		{"shell", "$HOME `id` $(rm -rf x) ; | && '\"", "$HOME `id` $(rm -rf x) ; | && '\""},
		{"html", "<script>alert(1)</script>", "<script>alert(1)</script>"},
		{"null word", "null", "null"},
		{"leading dash", "-v --json", "-v --json"},
		{"padded", "  padded text \n", "padded text"},
		{"long", long, long},
	}
	// One project per noun, the nouns in parallel; inside a noun the texts
	// run in sequence on that project, so each noun's rows sit side by side
	// as before (sub-test names stay noun/text).
	for _, noun := range []string{"memory", "rule", "decision", "incident"} {
		t.Run(noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			k := knowKindByNoun(t, noun)
			for _, tx := range texts {
				t.Run(tx.name, func(t *testing.T) {
					args := []string{noun, "add", "--body=" + tx.in, "--json"}
					if k.addTitle {
						args = append(args, "--title="+tx.in)
					}
					id := th.DecodeJSON[knowAdded](t, c.Lore(args...)).ID
					row := knowShowRow(t, c, k, id)
					if row.Body != tx.want {
						t.Fatalf("body stored as %q, want %q", knowCut(row.Body), knowCut(tx.want))
					}
					if k.addTitle && row.Title != tx.want {
						t.Fatalf("title stored as %q, want %q", knowCut(row.Title), knowCut(tx.want))
					}
				})
			}
			t.Run("blank", func(t *testing.T) {
				args := []string{noun, "add", "--body=   \n\t"}
				if k.addTitle {
					args = append(args, "--title=ok")
				}
				c.Lore(args...).Fails(t, 20, "E_EMPTY_BODY")
				if k.addTitle {
					c.Lore(noun, "add", "--body=ok", "--title=   ").Fails(t, 20, "E_INVALID_INPUT", "title")
					c.Lore(noun, "add", "--body=ok").Fails(t, 20, "E_INVALID_INPUT", "title")
				}
			})
		})
	}
}

// knowCut shortens a string for a failure message.
func knowCut(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

func TestKnowledgeDuplicateAdds(t *testing.T) {
	_, _, c := knowProject(t)
	for _, k := range knowKinds {
		t.Run(k.noun, func(t *testing.T) {
			first := knowAdd(t, c, k, "Same title", "same body")
			if k.noun == "prompt" {
				// Prompt names are unique per project: the second add is
				// refused with E_NAME_TAKEN (exit 23, INTEG-QUESTIONS-1),
				// names the existing row and writes nothing.
				c.Lore("prompt", "add", "--title", "Same title", "--body", "same body").Fails(t, 23, "E_NAME_TAKEN", first).Never(t, "✓")
				if l := knowListRows(t, c, k); l.Count != 1 {
					t.Fatalf("refused duplicate prompt left %d rows", l.Count)
				}
				return
			}
			second := knowAdd(t, c, k, "Same title", "same body")
			if second == first {
				t.Fatalf("a second add returned the first id %s", first)
			}
			if l := knowListRows(t, c, k); l.Count != 2 {
				t.Fatalf("two identical adds listed %d rows", l.Count)
			}
		})
	}
}

func TestKnowledgeKindSpecificFields(t *testing.T) {
	_, _, c := knowProject(t)
	for _, noun := range []string{"pattern", "cookbookrecipe"} {
		k := knowKindByNoun(t, noun)
		id := knowAdd(t, c, k, "Lang "+noun, "code", "--language", "go")
		if got := knowShowRow(t, c, k, id).Language; got != "go" {
			t.Fatalf("%s --language stored %q", noun, got)
		}
	}
	type described struct {
		Data struct {
			Description string `json:"description"`
			ArgsSchema  string `json:"args_schema"`
		} `json:"data"`
	}
	for _, noun := range []string{"playbook", "prompt"} {
		k := knowKindByNoun(t, noun)
		withDesc := knowAdd(t, c, k, "Described "+noun, "steps", "--description", "one line")
		if d := th.DecodeJSON[described](t, c.Lore(noun, "show", withDesc, "--json")); d.Data.Description != "one line" {
			t.Fatalf("%s --description stored %q", noun, d.Data.Description)
		}
		// Documented default: the description is the title.
		noDesc := knowAdd(t, c, k, "Plain "+noun, "steps")
		if d := th.DecodeJSON[described](t, c.Lore(noun, "show", noDesc, "--json")); d.Data.Description != "Plain "+noun {
			t.Fatalf("%s default description %q, want the title", noun, d.Data.Description)
		}
	}
	p := knowKindByNoun(t, "prompt")
	knowAdd(t, c, p, "Schema ok", "hello {{args.name}}", "--args-schema", `{"type":"object"}`)
	c.Lore("prompt", "add", "--title", "Schema bad", "--body", "x", "--args-schema", "not json").Fails(t, 20, "E_INVALID_INPUT")
	c.Lore("prompt", "add", "--title", "Schema array", "--body", "x", "--args-schema", "[1,2]").Fails(t, 20, "E_INVALID_INPUT")
	// suggestion --status round trip.
	s := knowKindByNoun(t, "suggestion")
	sid := knowAdd(t, c, s, "Suggest", "do it")
	c.Lore("suggestion", "edit", sid, "--status", "accepted").OK(t)
	if got := knowShowRow(t, c, s, sid).StatusStr; got != "accepted" {
		t.Fatalf("suggestion status %q", got)
	}
}

func TestKnowledgeAgentVsPersonAttribution(t *testing.T) {
	_, a, c := knowProject(t)
	agent := c.As(a.Agent("claude"))
	for _, k := range knowKinds {
		if !k.creator {
			continue
		}
		t.Run(k.noun, func(t *testing.T) {
			mine := knowAdd(t, c, k, "Person "+k.noun, "by the person")
			theirs := knowAdd(t, agent, k, "Agent "+k.noun, "by the agent")
			p := knowShowRow(t, c, k, mine).CreatedBy
			g := knowShowRow(t, c, k, theirs).CreatedBy
			if p == "" || g == "" || p == g {
				t.Fatalf("person %q and agent %q must be two actors", p, g)
			}
		})
	}
	// whoami names the agent session as the agent.
	agent.Lore("whoami").OK(t).Says(t, "agent:claude")
	c.Lore("whoami").OK(t).Never(t, "agent:claude")
}
