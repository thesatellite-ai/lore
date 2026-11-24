package ask

// idents_test.go — bug hunt H-230 (devco D-971, Sync benchmark set 5
// S5-06 and S5-11): the short row naming the asked thing as a code
// identifier, and the on-topic hotfix, rank above rows sharing only the
// question's common words.

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// TestIdentParts: camelCase, PascalCase, acronyms, dotted and kebab
// identifiers split into their words; plain words, versions and words
// with an apostrophe do not.
func TestIdentParts(t *testing.T) {
	t.Parallel()
	for w, want := range map[string][]string{
		"ErrGroupNameTaken": {"err", "role", "key", "reserved"},
		"JobTimeLimit":     {"max", "run", "duration"},
		"role.Create":        {"role", "create"},
		"HTTPServer":         {"http", "server"},
		"max-run-duration":   {"max", "run", "duration"},
		"parseV2Feed":        {"parse", "v2", "feed"},
		"role":               nil,
		"Role":               nil,
		"ROLE":               nil,
		"v0.1.47":            nil,
		"2026-11-02":         nil,
		"falcon's":           nil,
	} {
		if got := identParts(w); !slices.Equal(got, want) {
			t.Errorf("identParts(%q) = %q, want %q", w, got, want)
		}
	}
	for text, want := range map[string]int{
		"Plain prose. Ends here, with a well known word": 0,
		"role.Create returns ErrGroupNameTaken":         2,
		"the HTTPServer and max-run-duration":            2,
	} {
		if got := len(identsOf(text)); got != want {
			t.Errorf("identsOf(%q) holds %d identifiers, want %d", text, got, want)
		}
	}
	toks := tokensOf("role.Create returns ErrGroupNameTaken")
	for _, w := range []string{"ErrGroupNameTaken", "reserved", "key", "role", "create", "role.create"} {
		if !toks[w] {
			t.Errorf("tokensOf holds no %q: %v", w, toks)
		}
	}
}

// set5Rows is the S5-06 and S5-11 shape: many notes sharing the common
// words of both questions, a decision sharing more of them, two short
// memories naming the error identifiers, an off-topic hotfix sharing
// "sync workflow run" and the on-topic hotfix naming JobTimeLimit.
func set5Rows() data {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	var rows []textRow
	for i := range 30 {
		rows = append(rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_n%02d", i), Place: "kb/app", At: at.Add(time.Duration(i) * time.Minute),
			Body: fmt.Sprintf("Note %d: the workspace role screen lists each role, its key and the system flag; the sync workflow run page shows the client error for each run.", i)})
	}
	rows = append(rows,
		textRow{Table: "decisions", ID: "dec_roles", Place: "kb/app", At: at.Add(time.Hour), Title: "Workspace roles: system roles plus custom roles",
			Body: "Each workspace gets the system roles owner, admin and member when it is created; a custom role has its own key, unique in the workspace, and system roles are never deleted."},
		textRow{Table: "memories", ID: "mem_reserved", Place: "kb/app", At: at,
			Body: "role.Create returns ErrGroupNameTaken (409) when the new key collides with a seeded key (owner, admin, member)."},
		textRow{Table: "memories", ID: "mem_sysedit", Place: "kb/app", At: at,
			Body: "role.Update and role.Delete return ErrBuiltinGroupLocked for a seeded role; only custom roles change."},
		textRow{Table: "hotfixes", ID: "hfx_errmap", Place: "kb/app", At: at.Add(time.Hour), Title: "Workflow client error map",
			Body: "The sync workflow client maps each run error of the engine (cancelled, failed, timed out) to a typed sync error; a run that timed out was shown as an internal error until the map listed it."},
		textRow{Table: "hotfixes", ID: "hfx_duration", Place: "kb/app", At: at, Title: "JobTimeLimit raised to 1 h",
			Body: "Long catalog imports hit the 30 min JobTimeLimit and stopped mid-batch; JobTimeLimit is now 1 h in the workflow options."},
	)
	return data{rows: rows}
}

// TestIdentifierRowsRank (S5-06): both memories naming the asked errors
// are in the top three, above the decision sharing more common words.
func TestIdentifierRowsRank(t *testing.T) {
	t.Parallel()
	q := parseQuestion("what happens when a custom role uses a reserved role key or someone edits a system role", now)
	ids := rowIDs(build(q, set5Rows(), 3, 0).Rows)
	if len(ids) < 2 || !slices.Contains(ids[:2], "mem_reserved") || !slices.Contains(ids[:2], "mem_sysedit") {
		t.Errorf("top rows %v: want mem_reserved and mem_sysedit first", ids)
	}
}

// TestOnTopicHotfixFirst (S5-11): the hotfix naming JobTimeLimit leads
// the answer about the max run duration, above the off-topic hotfix and
// the notes sharing "sync workflow run".
func TestOnTopicHotfixFirst(t *testing.T) {
	t.Parallel()
	q := parseQuestion("what is the max run duration of a sync workflow run", now)
	if ids := rowIDs(build(q, set5Rows(), 3, 0).Rows); len(ids) == 0 || ids[0] != "hfx_duration" {
		t.Errorf("top rows %v: want hfx_duration first", ids)
	}
}

// TestQuestionIdentifierFindsSpelledRow: a question writing the
// identifier finds the row that spells its words out, and the row naming
// it still leads.
func TestQuestionIdentifierFindsSpelledRow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d := data{rows: []textRow{
		{Table: "memories", ID: "mem_spelled", Place: "kb/app", At: at, Body: "The max run duration of an import is one hour."},
		{Table: "memories", ID: "mem_other", Place: "kb/app", At: at, Body: "An import run writes a report."},
	}}
	ids := rowIDs(build(parseQuestion("what is JobTimeLimit", now), d, 0, 0).Rows)
	if len(ids) != 1 || ids[0] != "mem_spelled" {
		t.Errorf("rows %v: want only mem_spelled", ids)
	}
}

// TestRareWordsCount: a question word at most a quarter of the scored
// rows hold adds a point to each row holding it; a word most rows hold
// adds none, and among fewer than rareFloor rows no word does.
func TestRareWordsCount(t *testing.T) {
	t.Parallel()
	q := parseQuestion("why is the export batch throttled", now)
	var all []scored
	for i := range 10 {
		all = append(all, scored{row: &textRow{ID: fmt.Sprintf("mem_c%d", i)}, score: 6, matched: []string{"export", "batch"}})
	}
	all = append(all, scored{row: &textRow{ID: "mem_rare"}, score: 6, matched: []string{"export", "throttled"}})
	small := slices.Clone(all[len(all)-rareFloor+1:])
	rareBonus(q, all)
	for _, s := range all {
		want := 6
		if s.row.ID == "mem_rare" {
			want = 7
		}
		if s.score != want {
			t.Errorf("%s: score %d, want %d", s.row.ID, s.score, want)
		}
	}
	rareBonus(q, small)
	for _, s := range small {
		if s.score != 6 {
			t.Errorf("%s among %d rows: score %d, want 6", s.row.ID, len(small), s.score)
		}
	}
}
