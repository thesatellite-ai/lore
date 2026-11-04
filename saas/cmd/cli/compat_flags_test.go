package main

import (
	"strings"
	"testing"
)

// The v0.1.17 spellings the core's commands answer through the compat
// mount (core_mount addCompatFlags; spec-deltas memparity): --read-only and
// LORE_READ_ONLY=1 refuse writes and allow reads, --db is E_UNSUPPORTED
// (the core never opens lore.db), --confirm is refused naming --yes,
// --allow-secrets is an unknown flag (O12a), and every refusal prints its
// error once and writes nothing. Matrix: noun × spelling × read/write verb.
func TestCompatFlags_CoreNouns(t *testing.T) {
	_, c := initProject(t)
	type noun struct {
		name, table string
		add         []string
	}
	nouns := []noun{
		{"memory", "memories", []string{"--body", "the printer is downstairs"}},
		{"rule", "rules", []string{"--body", "run the tests"}},
		{"decision", "decisions", []string{"--title", "Payments", "--body", "Stripe"}},
		{"hotfix", "hotfixes", []string{"--title", "Pin", "--body", "v1.2 crashes"}},
		{"pattern", "patterns", []string{"--title", "Retry", "--body", "back off"}},
	}
	for _, n := range nouns {
		id := idPattern.FindString(c.lore(append([]string{n.name, "add"}, n.add...)...).stdout)
		if id == "" {
			t.Fatalf("%s add printed no id", n.name)
		}
		add := append([]string{n.name, "add"}, n.add...)
		cases := []struct {
			name string
			args []string
			env  []string
			code string // "" = must succeed
			want string // text the refusal must carry
		}{
			{"add --read-only", append(append([]string(nil), add...), "--read-only"), nil, "E_READ_ONLY", "read-only"},
			{"add LORE_READ_ONLY=1", add, []string{"LORE_READ_ONLY=1"}, "E_READ_ONLY", "LORE_READ_ONLY"},
			{"delete --read-only", []string{n.name, "delete", id, "--yes", "--read-only"}, nil, "E_READ_ONLY", "read-only"},
			{"add --db", append(append([]string(nil), add...), "--db", "/nonexistent/lore.db"), nil, "E_UNSUPPORTED", "--db"},
			{"delete --confirm", []string{n.name, "delete", id, "--confirm"}, nil, "E_INVALID_INPUT", "--yes"},
			{"add --allow-secrets", append(append([]string(nil), add...), "--allow-secrets"), nil, "unknown flag", "--allow-secrets"},
			{"list --read-only", []string{n.name, "list", "--read-only"}, nil, "", id},
			{"show LORE_READ_ONLY=1", []string{n.name, "show", id}, []string{"LORE_READ_ONLY=1"}, "", id},
		}
		for _, tc := range cases {
			before := ucCountFiles(t, c, n.table)
			c.env = tc.env
			r := c.loreAny(tc.args...)
			c.env = nil
			if tc.code == "" {
				if r.code != 0 || !strings.Contains(r.stdout, tc.want) {
					t.Fatalf("%s %s: want success naming %s, got %d %s %s", n.name, tc.name, tc.want, r.code, r.stdout, r.stderr)
				}
				continue
			}
			if r.code == 0 || !strings.Contains(r.stderr, tc.code) || !strings.Contains(r.stderr, tc.want) {
				t.Fatalf("%s %s: want %s naming %s, got %d %s", n.name, tc.name, tc.code, tc.want, r.code, r.stderr)
			}
			marker := tc.code // "[E_X]" as printed, so the hint's LORE_READ_ONLY is not counted
			if strings.HasPrefix(tc.code, "E_") {
				marker = "[" + tc.code + "]"
			}
			if got := strings.Count(r.stderr, marker); got != 1 {
				t.Fatalf("%s %s: the error must be printed once, got %d times:\n%s", n.name, tc.name, got, r.stderr)
			}
			if after := ucCountFiles(t, c, n.table); after != before {
				t.Fatalf("%s %s: a refused command changed the row files (%d -> %d)", n.name, tc.name, before, after)
			}
		}
		// The refusals left the row in place: a real delete still works.
		c.lore(n.name, "delete", id, "--yes")
	}
}
