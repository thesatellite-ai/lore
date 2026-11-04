package main

// Tests for commands that used to fail or misbehave: playbook/prompt add
// (no flag for the required description), snapshot add (no source_kind),
// rule list showing archived rules, --supersedes writing the link on the
// wrong row, identity set storing a name as the identity key, and doctor
// keeping a secret warning for a deleted row. Commands run through the real
// binary (e2e helpers in sync_e2e_test.go); pure helpers get table tests.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/constants"
)

// errCode returns the lore error code of err, or "" when it is not one.
func errCode(err error) errcodes.Code {
	var ce *errcodes.CLIError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// idOf returns the first row id with the given prefix in out.
func idOf(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, id := range idPattern.FindAllString(out, -1) {
		if strings.HasPrefix(id, prefix+"_") {
			return id
		}
	}
	t.Fatalf("no %s_ id in %q", prefix, out)
	return ""
}

// ── playbook / prompt / snapshot add ────────────────────────────────────

func TestFix_PlaybookAndPromptAddStoreADescription(t *testing.T) {
	_, c := initProject(t)
	for _, kind := range []string{"playbook", "prompt"} {
		c.lore(kind, "add", "--title", "How to release", "--body", "1. Test. 2. Tag.")
		c.lore(kind, "add", "--title", "Review", "--description", "Read the diff first", "--body", "List bugs, then style.")
		var rows []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Body        string `json:"body"`
		}
		decodeData(t, c.lore(kind, "list", "--json").stdout, &rows)
		want := map[string]string{"How to release": "How to release", "Review": "Read the diff first"}
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

func TestFix_SnapshotAddSourceKind(t *testing.T) {
	_, c := initProject(t)
	c.lore("snapshot", "add", "--title", "Week 44", "--body", "Checkout works.")
	c.lore("snapshot", "add", "--title", "Imported", "--body", "From the wiki.", "--source", constants.SourceImported.String())
	r := c.loreAny("snapshot", "add", "--title", "Bad", "--body", "x", "--source", "wiki")
	if r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_INPUT") || !strings.Contains(r.stderr, "manual, learn-from") {
		t.Fatalf("unknown --source must be refused with the list: %d %s", r.code, r.stderr)
	}
	var rows []struct {
		Title      string `json:"title"`
		SourceKind string `json:"source_kind"`
	}
	decodeData(t, c.lore("snapshot", "list", "--json").stdout, &rows)
	got := map[string]string{}
	for _, s := range rows {
		got[s.Title] = s.SourceKind
	}
	if len(got) != 2 || got["Week 44"] != "manual" || got["Imported"] != "imported" {
		t.Fatalf("snapshots: %+v (the refused one must not be written)", rows)
	}
}

func TestFix_ParseSourceKind(t *testing.T) {
	t.Parallel()
	for _, k := range knownSourceKinds() {
		if got, err := parseSourceKind(k.String()); err != nil || got != k {
			t.Fatalf("parseSourceKind(%q) = %q, %v", k, got, err)
		}
	}
	if got, err := parseSourceKind("  "); err != nil || got != constants.SourceManual {
		t.Fatalf("blank must read as manual: %q, %v", got, err)
	}
	for _, bad := range []string{"Manual", "user", "learn_from", "manual x"} {
		if _, err := parseSourceKind(bad); errCode(err) != errcodes.InvalidInput {
			t.Fatalf("parseSourceKind(%q) = %v, want E_INVALID_INPUT", bad, err)
		}
	}
}

func TestFix_KnowledgeDescription(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ desc, title, want string }{
		{"", "How to release", "How to release"},
		{"  ", "Title", "Title"},
		{"Own summary", "Title", "Own summary"},
		{"  padded  ", "Title", "padded"},
	} {
		got, err := knowledgeDescription(tc.desc, tc.title)
		if err != nil || got != tc.want {
			t.Fatalf("knowledgeDescription(%q,%q) = %q, %v; want %q", tc.desc, tc.title, got, err, tc.want)
		}
	}
	if _, err := knowledgeDescription(" ", ""); errCode(err) != errcodes.InvalidInput {
		t.Fatalf("empty description and title must be E_INVALID_INPUT: %v", err)
	}
}

// ── rule list ───────────────────────────────────────────────────────────

func TestFix_RuleListHidesArchived(t *testing.T) {
	_, c := initProject(t)
	old := c.addRule("Run the tests before you push")
	keep := c.addRule("Keep the API stable")
	c.lore("rule", "archive", old)
	out := c.lore("rule", "list").stdout
	if strings.Contains(out, old) || !strings.Contains(out, keep) {
		t.Fatalf("default list must hide the archived rule:\n%s", out)
	}
	out = c.lore("rule", "list", "--archived").stdout
	if !strings.Contains(out, old+" ") || !strings.Contains(out, "[archived]") || !strings.Contains(out, keep) {
		t.Fatalf("--archived must show it with a badge:\n%s", out)
	}
	var rows []struct {
		ID string `json:"id"`
	}
	decodeData(t, c.lore("rule", "list", "--json").stdout, &rows)
	if len(rows) != 1 || rows[0].ID != keep {
		t.Fatalf("--json default: %+v", rows)
	}
}

// ── --supersedes ────────────────────────────────────────────────────────

// supersedeRowJSON is the part of a row --supersedes touches.
type supersedeRowJSON struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	SupersededByID string `json:"superseded_by_id"`
}

func TestFix_SupersedesMarksTheOldRow(t *testing.T) {
	_, c := initProject(t)
	type kindCase struct {
		kind, prefix string
		add          func(extra ...string) string
	}
	add := func(kind, prefix string, args ...string) func(extra ...string) string {
		return func(extra ...string) string {
			return idOf(t, c.lore(append(append([]string{kind, "add"}, args...), extra...)...).stdout, prefix)
		}
	}
	cases := []kindCase{
		{"decision", "dec", add("decision", "dec", "--title", "Payments", "--body", "Stripe.")},
		{"rule", "rul", add("rule", "rul", "--body", "Run the tests")},
		{"memory", "mem", add("memory", "mem", "--body", "The printer is downstairs")},
		{"hotfix", "hfx", add("hotfix", "hfx", "--title", "Pin the driver", "--body", "v1.2 crashes")},
		{"pattern", "pat", add("pattern", "pat", "--title", "Retry", "--body", "Back off")},
	}
	for _, kc := range cases {
		old := kc.add()
		repl := kc.add("--supersedes", old)
		var o, n supersedeRowJSON
		decodeData(t, c.lore(kc.kind, "show", old, "--json").stdout, &o)
		decodeData(t, c.lore(kc.kind, "show", repl, "--json").stdout, &n)
		if o.SupersededByID != repl {
			t.Fatalf("%s: old row must point at its replacement: %+v", kc.kind, o)
		}
		if n.SupersededByID != "" {
			t.Fatalf("%s: the new row is not superseded: %+v", kc.kind, n)
		}
		if kc.kind == "decision" && (o.Status != "superseded" || n.Status != "accepted") {
			t.Fatalf("decision statuses: old %q new %q", o.Status, n.Status)
		}
		// The link is in the synced file too.
		if doc := c.readDoc(map[string]string{"decision": "decisions", "rule": "rules", "memory": "memories", "hotfix": "hotfixes", "pattern": "patterns"}[kc.kind], old); doc["superseded_by_id"] != repl {
			t.Fatalf("%s: .lore/data file of the old row: %v", kc.kind, doc["superseded_by_id"])
		}

		// Refusals write nothing: an already-replaced row, an unknown id,
		// and an id of another kind.
		before := strings.Count(c.lore(kc.kind, "list", "--json").stdout, `"id"`)
		for _, bad := range []struct{ id, code string }{
			{old, "E_INVALID_INPUT"},
			{kc.prefix + "_00000000000000000000000000000000", "E_NOT_FOUND"}, // unparseable: v0.1.17 says not found
			{kc.prefix + "_01a1a681c07e7dfe8ec01fc44d487e86", "E_NOT_FOUND"}, // well-formed, unknown
		} {
			r := c.w.run(c.dir, c.env, e2eBin, append(supersedeArgs(kc.kind), "--supersedes", bad.id)...)
			if r.code == 0 || !strings.Contains(r.stderr, bad.code) {
				t.Fatalf("%s --supersedes %s: want %s, got %d %s", kc.kind, bad.id, bad.code, r.code, r.stderr)
			}
		}
		if after := strings.Count(c.lore(kc.kind, "list", "--json").stdout, `"id"`); after != before {
			t.Fatalf("%s: a refused --supersedes wrote a row (%d → %d)", kc.kind, before, after)
		}
	}
	// Another kind's id is not a row of this kind.
	rul := idOf(t, c.lore("rule", "list").stdout, "rul")
	if r := c.loreAny("hotfix", "add", "--title", "x", "--body", "y", "--supersedes", rul); r.code == 0 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
		t.Fatalf("cross-kind --supersedes: %d %s", r.code, r.stderr)
	}
}

// supersedeArgs is a valid add call for kind without --supersedes.
func supersedeArgs(kind string) []string {
	switch kind {
	case "rule", "memory":
		return []string{kind, "add", "--body", "Another one"}
	}
	return []string{kind, "add", "--title", "Another", "--body", "Another one"}
}

// ── identity set ────────────────────────────────────────────────────────

func TestFix_FallbackIdentityKeyIsAnEmail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		arg, key           string
		wantDisplay, wantK string
		wantErr            bool
	}{
		{arg: "alice@example.com", wantDisplay: "alice@example.com", wantK: "human:alice@example.com"},
		{arg: "Alice Doe <alice@example.com>", wantDisplay: "Alice Doe", wantK: "human:alice@example.com"},
		{arg: "Alice Doe", key: "human:alice@example.com", wantDisplay: "Alice Doe", wantK: "human:alice@example.com"},
		{arg: "alice@example.com", key: "human:alice@example.com", wantDisplay: "alice@example.com", wantK: "human:alice@example.com"},
		{arg: "alice", wantErr: true},
		{arg: "Alice Doe", wantErr: true},
		{arg: "   ", wantErr: true},
		{arg: "Alice", key: "alice@example.com", wantErr: true},
		{arg: "Alice", key: "human:alice", wantErr: true},
		{arg: "Alice", key: "human:Alice <alice@example.com>", wantErr: true},
		{arg: "bob@example.com", key: "human:alice@example.com", wantErr: true},
	} {
		display, key, err := fallbackIdentity(tc.arg, tc.key)
		if tc.wantErr {
			if errCode(err) != errcodes.InvalidInput {
				t.Fatalf("fallbackIdentity(%q,%q) = %q,%q,%v; want E_INVALID_INPUT", tc.arg, tc.key, display, key, err)
			}
			continue
		}
		if err != nil || display != tc.wantDisplay || key != tc.wantK {
			t.Fatalf("fallbackIdentity(%q,%q) = %q,%q,%v", tc.arg, tc.key, display, key, err)
		}
	}
}

func TestFix_IdentitySetRefusesABareName(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("alice")
	r := c.loreAny("identity", "set", "alice")
	if r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_INPUT") {
		t.Fatalf("bare name: %d %s", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".lore", "identity.yaml")); !os.IsNotExist(err) {
		t.Fatalf("a refused identity set must write nothing: %v", err)
	}
	c.lore("identity", "set", "Alice Doe", "--key", "human:alice@example.com")
	out := c.lore("identity", "show").stdout
	if !strings.Contains(out, "resolved:    human:alice@example.com") || !strings.Contains(out, "display:     Alice Doe") {
		t.Fatalf("identity show:\n%s", out)
	}
}

// ── doctor ──────────────────────────────────────────────────────────────

func TestFix_DoctorForgetsTheSecretOfADeletedRow(t *testing.T) {
	_, c := initProject(t)
	const secretBody = "Deploy key for staging: AKIAIOSFODNN7EXAMPLE"
	// 0.1.18 (O12a): memories and rules are written by the core, which
	// scans at write time; --allow-secrets is gone. Without --yes the add
	// is refused (E_NEEDS_CONFIRM) and nothing is written.
	for _, kind := range []string{"memory", "rule"} {
		before := strings.Count(c.lore(kind, "list", "--json").stdout, `"id"`)
		if r := c.loreAny(kind, "add", "--body", secretBody); r.code == 0 || !strings.Contains(r.stderr, "E_NEEDS_CONFIRM") {
			t.Fatalf("%s: a secret-looking body must ask first: %d %s", kind, r.code, r.stderr)
		}
		if after := strings.Count(c.lore(kind, "list", "--json").stdout, `"id"`); after != before {
			t.Fatalf("%s: a refused add wrote a row (%d -> %d)", kind, before, after)
		}
		// --yes stores it as not a secret: doctor has nothing to report
		// while the row exists, and nothing after it is deleted.
		id := idPattern.FindString(c.lore(kind, "add", "--yes", "--body", secretBody).stdout)
		if id == "" {
			t.Fatalf("%s add --yes printed no id", kind)
		}
		if out := c.loreAny("doctor").stdout; strings.Contains(out, "credential pattern") {
			t.Fatalf("%s: doctor warns about a row marked not a secret:\n%s", kind, out)
		}
		c.lore(kind, "delete", id, "--yes")
		if out := c.loreAny("doctor").stdout; strings.Contains(out, id) || strings.Contains(out, "credential pattern") {
			t.Fatalf("%s: doctor still warns about the deleted row:\n%s", kind, out)
		}
	}
}
