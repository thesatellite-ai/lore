//go:build teame2e

package teame2e

// B11 ports (PASS3 phase E): command-line behaviours the old binary's unit
// and e2e tests in saas/cmd/cli pinned (flag2_test.go, core_printed_test.go,
// move_cmd_test.go, oldcli_p2b_test.go, compat_flags_test.go,
// body_input_test.go, cmd_fixes_test.go, sql_shortcut_test.go,
// subfolder_write_e2e_test.go, core_delete_purge_test.go), run against the
// binary under test (LORE_TEAM_BIN, cmd/lore from phase E on) so they
// survive the deletion of the old module. Each test names the old test it
// replaces.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// b11ExitBadInput is the exit of every bad-input error (FLAG2, class
// bad_input).
const b11ExitBadInput = 20

// b11Solo is one developer's checkout of acme/app after `lore init`: the
// single-repo world the old saas/cmd/cli fixtures (initProject,
// newCoreFixture, newToolRepo) gave.
func b11Solo(t *testing.T, caps ...th.Cap) *th.Clone {
	t.Helper()
	w := newTeamWorld(t, caps...)
	dev := w.Human("dev", "dev@acme.example")
	c := dev.Clone(w.Remote(slugApp), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	return c
}

// b11Data decodes the data member of a --json envelope into v.
func b11Data(t *testing.T, r th.Result, v any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("not a JSON envelope: %v\n%s", err, r)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("data: %v\n%s", err, r)
	}
}

// b11Prefix is the id prefix of a knowledge table.
var b11Prefix = map[string]string{"memories": "mem", "rules": "rul", "decisions": "dec", "hotfixes": "hfx", "patterns": "pat"}

// b11RowFiles counts the row files of table in the checkout's .lore/data.
func b11RowFiles(t *testing.T, c *th.Clone, table string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(c.Dir, ".lore", "data", table, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

// b11Count is how many rows a `<noun> list --json` prints.
func b11Count(t *testing.T, c *th.Clone, noun string) int {
	t.Helper()
	var rows []json.RawMessage
	b11Data(t, c.MustLore(noun, "list", "--json"), &rows)
	return len(rows)
}

// TestB11_InputErrorsExit20 (old flag2_test.go TestFlag2InputErrorsExit20):
// every malformed command line exits 20 with exactly one
// [E_INVALID_INPUT] line naming what is wrong, on host commands (init) as
// on operations. v0.1.17's `backup` (dropped) is replaced by `init`, the
// host command cmd/lore keeps.
func TestB11_InputErrorsExit20(t *testing.T) {
	c := b11Solo(t, th.Cmd("org", "link"))
	for _, tc := range []struct {
		name string
		args []string
		say  string
	}{
		{"unknown root command", []string{"nosuchverb"}, `unknown command "nosuchverb"`},
		{"unknown subcommand of a generated group", []string{"memory", "nosuchverb"}, `unknown command "nosuchverb"`},
		{"unknown flag on a host command", []string{"init", "--nope"}, "unknown flag: --nope"},
		{"unknown flag on a generated command", []string{"memory", "list", "--nope"}, "unknown flag: --nope"},
		{"unknown shorthand flag", []string{"init", "-Z"}, "unknown shorthand flag"},
		{"missing positional on an operation", []string{"org", "link"}, "missing required field org"},
		{"too many arguments on an operation", []string{"org", "link", "a", "b"}, "too many arguments: want at most 1"},
		{"required flag not set", []string{"org", "link", "northwind"}, "--project <folder>"},
		{"missing id on a generated show", []string{"memory", "show"}, "missing required field id"},
	} {
		r := c.Lore(tc.args...)
		if r.Code != b11ExitBadInput || strings.Count(r.Stderr, "[E_INVALID_INPUT]") != 1 || !strings.Contains(r.Stderr, tc.say) {
			t.Errorf("%s: exit %d, want %d with one [E_INVALID_INPUT] and %q: %s", tc.name, r.Code, b11ExitBadInput, tc.say, r)
		}
	}
}

// TestB11_ActorDeleteRefused (old flag2_test.go TestActorDeleteRefused):
// `lore actor delete` is refused with E_REFUSED (exit 23) naming
// `lore actor deactivate` (BATCH2 #8: actors are never deleted).
func TestB11_ActorDeleteRefused(t *testing.T) {
	c := b11Solo(t, th.Cmd("actor", "deactivate"))
	for _, args := range [][]string{{"actor", "delete", "act_0123456789abcdef0123456789abcdef"}, {"actor", "delete"}} {
		c.Lore(args...).Fails(t, 23, "[E_REFUSED]", "lore actor deactivate")
	}
}

// TestB11_ErrorPrintedOnce (old core_printed_test.go TestCoreErrorPrintedOnce
// and TestCoreFlagErrorPrintedOnce): a failing operation (store init into a
// folder that cannot be created: E_INTERNAL, exit 1, the same status as a
// foreign error) prints its error once, and an unknown flag is printed
// once, never a second line from main.
func TestB11_ErrorPrintedOnce(t *testing.T) {
	c := b11Solo(t, th.Cmd("store", "init"), th.Cmd("migrate", "run"))
	r := c.Lore("store", "init", "extra", "file:///proc/lore-cannot-create/x", "--yes")
	if r.Code == 0 || strings.Count(r.Stderr, "ERROR:") != 1 {
		t.Fatalf("the error must be printed once: %s", r)
	}
	for _, args := range [][]string{{"migrate", "run", "--bogus"}, {"store", "init", "acme", "--url", "x"}} {
		if r := c.Lore(args...); r.Code == 0 || strings.Count(r.Stderr, "unknown flag") != 1 {
			t.Fatalf("lore %v: the flag error must be printed once: %s", args, r)
		}
	}
}

// TestB11_ShareIsAnUnknownCommand (old move_cmd_test.go
// TestShareIsAnUnknownCommand; BATCH 2: `lore move` is the one command,
// `lore share` is removed): `lore share …` answers exactly like any other
// unknown root command, with no alias, stub or special hint. Delta: with
// flags after it (`share <id> --to repo`) cmd/lore's cobra reports the
// unknown flag first, as it does for any unknown command, so the
// "unknown command" line is pinned on the flagless form.
func TestB11_ShareIsAnUnknownCommand(t *testing.T) {
	c := b11Solo(t)
	const id = "mem_0123456789abcdef0123456789abcdef"
	first := func(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }
	for _, tail := range [][]string{{id}, {id, "--to", "repo", "--yes"}} {
		share := c.Lore(append([]string{"share"}, tail...)...)
		other := c.Lore(append([]string{"nosuchverb"}, tail...)...)
		if share.Code != b11ExitBadInput || share.Code != other.Code ||
			strings.ReplaceAll(first(share.Stderr), `"share"`, `"nosuchverb"`) != first(other.Stderr) {
			t.Fatalf("lore share must answer like any unknown command:\nshare: %s\nnosuchverb: %s", share, other)
		}
		if strings.Contains(share.Stderr, "lore move") {
			t.Fatalf("no hint special-cased for share: %s", share)
		}
	}
	c.Lore("share", id).Fails(t, b11ExitBadInput, `unknown command "share" for "lore"`)
}

// TestB11_RepoSetURLUnnormalisableIsExit20 (old oldcli_p2b_test.go
// TestRepoSetURL_UnnormalisableIsExit20): an address the normaliser refuses
// answers E_REPO_URL, a bad argument: exit 20.
func TestB11_RepoSetURLUnnormalisableIsExit20(t *testing.T) {
	c := b11Solo(t, th.Cmd("repo", "set-url"))
	for _, bad := range []string{"not a url", "https://"} {
		c.Lore("repo", "set-url", bad).Fails(t, b11ExitBadInput, "E_REPO_URL")
	}
}

// TestB11_CompatFlagsCoreNouns (old compat_flags_test.go
// TestCompatFlags_CoreNouns): v0.1.17's common flags on the knowledge
// nouns: --read-only and LORE_READ_ONLY=1 refuse writes (E_READ_ONLY) and
// allow reads, --db is refused (E_UNSUPPORTED), --allow-secrets is gone
// (unknown flag); each refusal is printed once and changes no row file;
// v0.1.17's delete --confirm is a soft delete with the purge hint (FA-DEL-1,
// DQ8).
func TestB11_CompatFlagsCoreNouns(t *testing.T) {
	c := b11Solo(t, th.Cmd("trash", "list"))
	for _, n := range []struct {
		name, table string
		add         []string
	}{
		{"memory", "memories", []string{"--body", "the printer is downstairs"}},
		{"rule", "rules", []string{"--body", "run the tests"}},
		{"decision", "decisions", []string{"--title", "Payments", "--body", "Stripe"}},
		{"hotfix", "hotfixes", []string{"--title", "Pin", "--body", "v1.2 crashes"}},
		{"pattern", "patterns", []string{"--title", "Retry", "--body", "back off"}},
	} {
		add := append([]string{n.name, "add"}, n.add...)
		id := mustID(t, c.MustLore(add...), b11Prefix[n.table])
		with := func(extra ...string) []string { return append(append([]string(nil), add...), extra...) }
		for _, tc := range []struct {
			name string
			args []string
			env  []string
			code string // "" = must succeed
			want string
		}{
			{"add --read-only", with("--read-only"), nil, "E_READ_ONLY", "read-only"},
			{"add LORE_READ_ONLY=1", add, []string{"LORE_READ_ONLY=1"}, "E_READ_ONLY", "LORE_READ_ONLY"},
			{"delete --read-only", []string{n.name, "delete", id, "--yes", "--read-only"}, nil, "E_READ_ONLY", "read-only"},
			{"add --db", with("--db", "/nonexistent/lore.db"), nil, "E_UNSUPPORTED", "--db"},
			{"add --allow-secrets", with("--allow-secrets"), nil, "unknown flag", "--allow-secrets"},
			{"list --read-only", []string{n.name, "list", "--read-only"}, nil, "", id},
			{"show LORE_READ_ONLY=1", []string{n.name, "show", id}, []string{"LORE_READ_ONLY=1"}, "", id},
		} {
			before := b11RowFiles(t, c, n.table)
			r := c.With(tc.env...).Lore(tc.args...)
			if tc.code == "" {
				if r.Code != 0 || !strings.Contains(r.Stdout, tc.want) {
					t.Fatalf("%s %s: want success naming %s: %s", n.name, tc.name, tc.want, r)
				}
				continue
			}
			if r.Code == 0 || !strings.Contains(r.Stderr, tc.code) || !strings.Contains(r.Stderr, tc.want) {
				t.Fatalf("%s %s: want %s naming %s: %s", n.name, tc.name, tc.code, tc.want, r)
			}
			marker := tc.code
			if strings.HasPrefix(tc.code, "E_") {
				marker = "[" + tc.code + "]"
			}
			if got := strings.Count(r.Stderr, marker); got != 1 {
				t.Fatalf("%s %s: the error must be printed once, got %d: %s", n.name, tc.name, got, r)
			}
			if after := b11RowFiles(t, c, n.table); after != before {
				t.Fatalf("%s %s: a refused command changed the row files (%d -> %d)", n.name, tc.name, before, after)
			}
		}
		before := b11RowFiles(t, c, n.table)
		c.MustLore(n.name, "delete", id, "--confirm").Says(t, "moved to the trash", "lore trash purge <id> --confirm")
		if after := b11RowFiles(t, c, n.table); after != before {
			t.Fatalf("%s delete --confirm purged the row file (%d -> %d)", n.name, before, after)
		}
		c.MustLore("trash", "list").Says(t, id)
	}
}

// TestB11_PositionalBody (old body_input_test.go
// TestE2E_PositionalBodyIsRefused, ported to the phase 4 g1/g2 spec delta
// its comment cites: rule, pattern and memory take the body positionally or
// with --body, and both spellings write the same body).
func TestB11_PositionalBody(t *testing.T) {
	c := b11Solo(t)
	for _, tc := range []struct {
		args   []string
		prefix string
	}{
		{[]string{"rule", "add", "--severity=must", "never force-push main"}, "rul"},
		{[]string{"rule", "add", "--body=use JWT"}, "rul"},
		{[]string{"pattern", "add", "--title=t", "body text"}, "pat"},
		{[]string{"memory", "add", "use JWT"}, "mem"},
		{[]string{"memory", "add", "--body=use pgx"}, "mem"},
	} {
		mustID(t, c.MustLore(tc.args...), tc.prefix)
	}
	c.MustLore("rule", "list").Says(t, "never force-push main", "use JWT")
	c.MustLore("memory", "list").Says(t, "use JWT", "use pgx")
}

// TestB11_PlaybookAndPromptDescription (old cmd_fixes_test.go
// TestFix_PlaybookAndPromptAddStoreADescription and
// TestFix_KnowledgeDescription): add stores --description, and the title
// when none is given.
func TestB11_PlaybookAndPromptDescription(t *testing.T) {
	c := b11Solo(t)
	for _, kind := range []string{"playbook", "prompt"} {
		c.MustLore(kind, "add", "--title", "How to release", "--body", "1. Test. 2. Tag.")
		c.MustLore(kind, "add", "--title", "Review", "--description", "Read the diff first", "--body", "List bugs, then style.")
		c.MustLore(kind, "add", "--title", "Padded", "--description", "  padded  ", "--body", "x")
		var rows []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Body        string `json:"body"`
		}
		b11Data(t, c.MustLore(kind, "list", "--json"), &rows)
		want := map[string]string{"How to release": "How to release", "Review": "Read the diff first", "Padded": "padded"}
		if len(rows) != len(want) {
			t.Fatalf("%s rows: %+v", kind, rows)
		}
		for _, r := range rows {
			if want[r.Name] != r.Description || r.Body == "" {
				t.Fatalf("%s %q: description %q, body %q", kind, r.Name, r.Description, r.Body)
			}
		}
	}
}

// TestB11_SnapshotSourceKind (old cmd_fixes_test.go
// TestFix_SnapshotAddSourceKind and TestFix_ParseSourceKind): --source
// takes the closed list of source kinds (manual by default); anything else
// is refused with the list and writes nothing.
func TestB11_SnapshotSourceKind(t *testing.T) {
	c := b11Solo(t)
	c.MustLore("snapshot", "add", "--title", "Week 44", "--body", "Checkout works.")
	c.MustLore("snapshot", "add", "--title", "Imported", "--body", "From the wiki.", "--source", "imported")
	for _, bad := range []string{"wiki", "Manual", "learn_from", "manual x"} {
		c.Lore("snapshot", "add", "--title", "Bad", "--body", "x", "--source", bad).Fails(t, b11ExitBadInput, "E_INVALID_INPUT", "manual, learn-from")
	}
	var rows []struct {
		Title      string `json:"title"`
		SourceKind string `json:"source_kind"`
	}
	b11Data(t, c.MustLore("snapshot", "list", "--json"), &rows)
	got := map[string]string{}
	for _, s := range rows {
		got[s.Title] = s.SourceKind
	}
	if len(got) != 2 || got["Week 44"] != "manual" || got["Imported"] != "imported" {
		t.Fatalf("snapshots: %+v (the refused ones must not be written)", rows)
	}
}

// TestB11_RuleListHidesArchived (old cmd_fixes_test.go
// TestFix_RuleListHidesArchived).
func TestB11_RuleListHidesArchived(t *testing.T) {
	c := b11Solo(t)
	old := mustID(t, c.MustLore("rule", "add", "--body", "Run the tests before you push"), "rul")
	keep := mustID(t, c.MustLore("rule", "add", "--body", "Keep the API stable"), "rul")
	c.MustLore("rule", "archive", old)
	if out := c.MustLore("rule", "list").Stdout; strings.Contains(out, old) || !strings.Contains(out, keep) {
		t.Fatalf("default list must hide the archived rule:\n%s", out)
	}
	if out := c.MustLore("rule", "list", "--archived").Stdout; !strings.Contains(out, old+" ") || !strings.Contains(out, "[archived]") || !strings.Contains(out, keep) {
		t.Fatalf("--archived must show it with a badge:\n%s", out)
	}
	var rows []struct {
		ID string `json:"id"`
	}
	b11Data(t, c.MustLore("rule", "list", "--json"), &rows)
	if len(rows) != 1 || rows[0].ID != keep {
		t.Fatalf("--json default: %+v", rows)
	}
}

// TestB11_SupersedesMarksTheOldRow (old cmd_fixes_test.go
// TestFix_SupersedesMarksTheOldRow): --supersedes points the old row at
// its replacement (in the row file too; a decision becomes superseded), and
// refusals (an already replaced row, an unknown id, another kind's id)
// write nothing.
func TestB11_SupersedesMarksTheOldRow(t *testing.T) {
	c := b11Solo(t)
	type sup struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		SupersededByID string `json:"superseded_by_id"`
	}
	for _, kc := range []struct {
		kind, table string
		add         []string
	}{
		{"decision", "decisions", []string{"--title", "Payments", "--body", "Stripe."}},
		{"rule", "rules", []string{"--body", "Run the tests"}},
		{"memory", "memories", []string{"--body", "The printer is downstairs"}},
		{"hotfix", "hotfixes", []string{"--title", "Pin the driver", "--body", "v1.2 crashes"}},
		{"pattern", "patterns", []string{"--title", "Retry", "--body", "Back off"}},
	} {
		prefix := b11Prefix[kc.table]
		add := func(extra ...string) string {
			return mustID(t, c.MustLore(append(append([]string{kc.kind, "add"}, kc.add...), extra...)...), prefix)
		}
		old := add()
		repl := add("--supersedes", old)
		var o, n sup
		b11Data(t, c.MustLore(kc.kind, "show", old, "--json"), &o)
		b11Data(t, c.MustLore(kc.kind, "show", repl, "--json"), &n)
		if o.SupersededByID != repl || n.SupersededByID != "" {
			t.Fatalf("%s: old %+v new %+v", kc.kind, o, n)
		}
		if kc.kind == "decision" && (o.Status != "superseded" || n.Status != "accepted") {
			t.Fatalf("decision statuses: old %q new %q", o.Status, n.Status)
		}
		raw, ok := c.ReadFile(filepath.Join(".lore", "data", kc.table, old+".json"))
		if !ok || !strings.Contains(raw, repl) {
			t.Fatalf("%s: the row file of the old row lacks superseded_by_id %s:\n%s", kc.kind, repl, raw)
		}
		before := b11Count(t, c, kc.kind)
		for _, bad := range []struct{ id, code string }{
			{old, "E_INVALID_INPUT"},
			{prefix + "_00000000000000000000000000000000", "E_INVALID_ID"}, // B-ERRS-1: no UUIDv7
			{prefix + "_01a1a681c07e7dfe8ec01fc44d487e86", "E_NOT_FOUND"},
		} {
			c.Lore(append(append([]string{kc.kind, "add"}, kc.add...), "--supersedes", bad.id)...).Refused(t, bad.code)
		}
		if after := b11Count(t, c, kc.kind); after != before {
			t.Fatalf("%s: a refused --supersedes wrote a row (%d -> %d)", kc.kind, before, after)
		}
	}
	rul := mustID(t, c.MustLore("rule", "add", "--body", "another rule"), "rul")
	c.Lore("hotfix", "add", "--title", "x", "--body", "y", "--supersedes", rul).Refused(t, "E_INVALID_ID") // another noun's id (B-ERRS-1)
}

// TestB11_IdentitySetRefusesABareName (old cmd_fixes_test.go
// TestFix_IdentitySetRefusesABareName): a bare name is refused and writes
// nothing; a name with --key is stored and identity show resolves it.
func TestB11_IdentitySetRefusesABareName(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("identity", "set"))
	alice := w.Human("alice", "alice@example.com")
	c := alice.Clone(w.Remote(slugApp), "app")
	before, _ := os.ReadFile(alice.LorePath("config.yaml")) // absent is fine: compared as bytes
	c.Lore("identity", "set", "alice").Fails(t, b11ExitBadInput, "E_INVALID_INPUT")
	after, _ := os.ReadFile(alice.LorePath("config.yaml"))
	if string(before) != string(after) {
		t.Fatalf("a refused identity set changed config.yaml:\nbefore %s\nafter  %s", before, after)
	}
	if _, err := os.Stat(alice.LorePath("identity.yaml")); !os.IsNotExist(err) {
		t.Fatalf("a refused identity set must write nothing: %v", err)
	}
	c.MustLore("identity", "set", "Alice Doe", "--key", "human:alice@example.com")
	c.With("LORE_HUMAN=1").MustLore("identity", "show").Says(t, "human:alice@example.com", "Alice Doe")
}

// TestB11_DoctorForgetsTheSecretOfADeletedRow (old cmd_fixes_test.go
// TestFix_DoctorForgetsTheSecretOfADeletedRow): a secret-looking body asks
// first (E_NEEDS_CONFIRM) and writes nothing; stored with --not-a-secret
// --reason --yes, doctor has nothing to report, before and after delete.
func TestB11_DoctorForgetsTheSecretOfADeletedRow(t *testing.T) {
	c := b11Solo(t, th.Cmd("doctor"))
	secretBody := "Deploy key for staging: AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in source
	for _, kind := range []string{"memory", "rule"} {
		before := b11Count(t, c, kind)
		c.Lore(kind, "add", "--body", secretBody).Refused(t, "E_NEEDS_CONFIRM")
		if after := b11Count(t, c, kind); after != before {
			t.Fatalf("%s: a refused add wrote a row (%d -> %d)", kind, before, after)
		}
		id := mustID(t, c.MustLore(kind, "add", "--not-a-secret", "--reason", "test fixture key", "--yes", "--body", secretBody), map[string]string{"memory": "mem", "rule": "rul"}[kind])
		c.Lore("doctor").Never(t, "credential pattern")
		c.MustLore(kind, "delete", id, "--yes")
		c.Lore("doctor").Never(t, id, "credential pattern")
	}
}

// TestB11_SQLShortcut (old sql_shortcut_test.go TestSQLShortcut): `lore sql
// <query>` runs sql.run like `lore sql run <query>`, flags before or after
// the query, several words joined; a bad flag is a usage error;
// --show-sensitive asks first.
func TestB11_SQLShortcut(t *testing.T) {
	c := b11Solo(t, th.Cmd("sql", "run"))
	c.MustLore("memory", "add", "--body", "hello sql")
	const q = "SELECT COUNT(*) AS n FROM memories_all"
	short := c.MustLore("sql", q, "--json").Stdout
	long := c.MustLore("sql", "run", q, "--json").Stdout
	if short != long {
		t.Fatalf("lore sql <q> must equal lore sql run <q>:\n%s\n%s", short, long)
	}
	// --json is indented for people (surface/cli printJSON), so read the
	// count from the decoded result, not from the bytes.
	var res struct {
		Count *int `json:"count"`
	}
	if err := json.Unmarshal([]byte(short), &res); err != nil || res.Count == nil || *res.Count != 1 {
		t.Fatalf("lore sql --json must report count 1 (err %v):\n%s", err, short)
	}
	c.MustLore("sql", "--json", "SELECT 1 AS one").Says(t, `"one"`)
	c.MustLore("sql", "SELECT", "2", "AS", "two").Says(t, "two")
	c.Lore("sql", "--bogus", q).Refused(t, "unknown flag: --bogus")
	c.Lore("sql", "--show-sensitive", q).Fails(t, 26, "E_NEEDS_CONFIRM")
}

// TestB11_WriteFromSubfolder (old subfolder_write_e2e_test.go
// TestE2E_CoreWriteFromSubfolder): a write run from a folder deep inside
// the repo stores exactly one row file in the repo's .lore/data and one
// audit record in the repo's .lore/state/audit-v2.jsonl (SPEC 9.2; the
// v0.1.17 lore.db chain is gone), and creates no .lore in the subfolders.
func TestB11_WriteFromSubfolder(t *testing.T) {
	c := b11Solo(t)
	before := b11RowFiles(t, c, "memories")
	id := mustID(t, c.In(filepath.Join("sub", "deeper")).MustLore("memory", "add", "--body", "written from a subfolder"), "mem")
	if _, ok := c.ReadFile(filepath.Join(".lore", "data", "memories", id+".json")); !ok || b11RowFiles(t, c, "memories") != before+1 {
		t.Fatalf("want one new row file %s.json in the repo's .lore/data", id)
	}
	for _, d := range []string{"sub", filepath.Join("sub", "deeper")} {
		if _, err := os.Stat(filepath.Join(c.Dir, d, ".lore")); !os.IsNotExist(err) {
			t.Fatalf("a .lore folder was created in %s: %v", d, err)
		}
	}
	audit, _ := c.ReadFile(filepath.Join(".lore", "state", "audit-v2.jsonl"))
	if n := strings.Count(audit, id); n != 1 {
		t.Fatalf("the subfolder write is in the repo's audit record %d times, want 1:\n%s", n, audit)
	}
}

// TestB11_ResourceMetaDeletePurgeConfirm (old core_delete_purge_test.go
// TestResourceMetaDeletePurgeConfirm): resource and meta delete --purge
// asks for --confirm (a bare --yes never purges), delete --confirm is a
// soft delete with the purge hint, --purge --confirm purges; the bulk form
// too; the trash holds only the soft-deleted rows.
func TestB11_ResourceMetaDeletePurgeConfirm(t *testing.T) {
	c := b11Solo(t, th.Cmd("resource", "kind", "add"), th.Cmd("meta", "add"))
	c.MustLore("resource", "kind", "add", "host")
	c.MustLore("resource", "field", "add", "host", "provider", "--type", "text")
	for _, n := range []string{"h_soft", "h_gone", "h_bulk"} {
		c.MustLore("resource", "add", "host", n)
	}
	c.MustLore("resource", "edit", "h_bulk", "--set", "provider=x")
	c.MustLore("meta", "add", "k_soft", "--type", "text")
	c.MustLore("meta", "add", "k_gone", "--type", "text", "--values", "a,b")
	for _, n := range []struct{ noun, soft, gone string }{{"resource", "h_soft", "h_gone"}, {"meta", "k_soft", "k_gone"}} {
		for _, refused := range [][]string{{"--purge"}, {"--purge", "--yes"}} {
			c.Lore(append([]string{n.noun, "delete", n.gone}, refused...)...).Fails(t, 26, "E_NEEDS_CONFIRM", "--confirm")
		}
		c.MustLore(n.noun, "delete", n.soft, "--confirm").Says(t, "moved to the trash", "lore trash purge <id> --confirm")
		c.MustLore(n.noun, "delete", n.gone, "--purge", "--confirm").Says(t, "purged for good")
	}
	c.Lore("resource", "delete", "--kind", "host", "--where", "provider=x", "--purge").Fails(t, 26, "every matching row")
	c.MustLore("resource", "delete", "--kind", "host", "--where", "provider=x", "--purge", "--confirm").Says(t, "purged for good")
	trash := c.MustLore("trash").Says(t, `"h_soft"`, `"k_soft"`)
	trash.Never(t, `"h_gone"`, `"k_gone"`, `"h_bulk"`)
}

// TestB11_SearchHydratesHits (old usecases_misc_test.go
// TestUC_MiscGlobalSearchHydratesHits and TestUC_MiscTruncBody; ops/search
// has no test of its own): `lore search` finds every kind, each hit carries
// its id and pretty id, a long body is cut to 80 characters ending in
// "...", a decision shows its title, and no match says so.
func TestB11_SearchHydratesHits(t *testing.T) {
	c := b11Solo(t)
	mem := mustID(t, c.MustLore("memory", "add", "--body", "zebra stripes confuse predators"), "mem")
	longRule := "zebra crossings need a dedicated rule whose body is deliberately longer than eighty characters in total"
	rule := mustID(t, c.MustLore("rule", "add", "--body", longRule), "rul")
	c.MustLore("decision", "add", "--title=zebra decision", "--body=we picked zebra")
	var res struct {
		Count int `json:"count"`
		Hits  []struct {
			Entity string `json:"entity"`
			ID     string `json:"id"`
			Pretty string `json:"pretty"`
			Body   string `json:"body"`
		} `json:"hits"`
	}
	b11Data(t, c.MustLore("search", "zebra", "--json"), &res)
	if res.Count < 3 {
		t.Fatalf("expected hits from three kinds: %+v", res)
	}
	seen := map[string]int{}
	for i, h := range res.Hits {
		seen[h.Entity] = i
	}
	if i, ok := seen["memory"]; !ok || res.Hits[i].ID != mem || res.Hits[i].Pretty != mem {
		t.Fatalf("memory hit not hydrated: %+v", res.Hits)
	}
	if i, ok := seen["rule"]; !ok || res.Hits[i].ID != rule || len(res.Hits[i].Body) != 80 || !strings.HasSuffix(res.Hits[i].Body, "...") ||
		!strings.HasPrefix(longRule, strings.TrimSuffix(res.Hits[i].Body, "...")) {
		t.Fatalf("rule body must be cut to 80: %+v", res.Hits)
	}
	if i, ok := seen["decision"]; !ok || res.Hits[i].Body != "zebra decision" || res.Hits[i].Pretty == "" {
		t.Fatalf("decision hit shows its title: %+v", res.Hits)
	}
	c.MustLore("search", "qqqnomatchqqq").Says(t, "(no matches)")
}

// TestB11_DirectiveReinstallIsIdempotent (old usecases_misc_test.go
// TestUC_MiscDirectiveReinstallIsIdempotent): re-installing leaves the block
// byte for byte as `directive show` prints it (a "$RID" in it survives), and
// a stale block is replaced in place, keeping the person's own notes.
// TOOL NEUTRAL: a folder with no tool file gets AGENTS.md.
func TestB11_DirectiveReinstallIsIdempotent(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("directive", "install"))
	c := w.Human("dev", "dev@acme.example").Clone(w.Remote(slugApp), "app")
	block := c.MustLore("directive", "show").Stdout
	c.MustLore("directive", "install")
	c.MustLore("directive", "install").Says(t, "(no change)")
	got, _ := c.ReadFile("AGENTS.md")
	if got != block {
		t.Fatalf("re-installed block differs from `directive show` ($RID kept: %v)", strings.Contains(got, "$RID"))
	}
	const hand = "\nhand notes\n"
	first, rest, _ := strings.Cut(block, "\n")
	c.WriteFile("AGENTS.md", first+"\nan outdated line\n"+rest+hand)
	c.MustLore("directive", "install")
	if got, _ := c.ReadFile("AGENTS.md"); got != block+hand {
		t.Fatalf("a stale block must be replaced in place, keeping hand notes:\n%q", got)
	}
}
