package link

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// Moved from saas/cmd/cli/usecases_misc_test.go with link_cmds.go.
func TestLikelySHA(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"abc1234":               true,
		"deadbeefcafe1234":      true,
		strings.Repeat("a", 40): true,
		"abc123":                false, // too short
		strings.Repeat("a", 41): false, // too long
		"ABC1234":               false, // git prints lowercase
		"xyz1234":               false,
		"abc-1234":              false,
		"HEAD":                  false,
	} {
		if got := likelySHA(s); got != want {
			t.Fatalf("likelySHA(%q) = %v", s, got)
		}
	}
}

// Moved from saas/cmd/cli/usecases_misc_test.go: v0.1.17's prefix → table
// map, kept for the tables not on the core yet.
func TestLegacyTables(t *testing.T) {
	t.Parallel()
	want := map[ids.Prefix]row.Table{
		"tsk": "tasks", "mem": "memories", "msn": "missions", "tlt": "task_lists",
		"pln": "plans", "dec": "decisions", "rul": "rules", "hfx": "hotfixes",
		"pat": "patterns", "pbk": "playbooks", "prm": "prompts", "ann": "architecture_notes",
		"bhv": "behaviours", "ckr": "cookbook_recipes", "inc": "incidents", "sgg": "suggestions",
		"tpr": "taste_prefs", "snp": "snapshots", "wfl": "workflows", "wsp": "workspaces",
		"hnd": "handoffs", "rem": "reminders", "run": "runs", "tdc": "tech_docs", "cmt": "comments",
	}
	for p, tbl := range want {
		if legacyTables[p] != tbl {
			t.Fatalf("%s → %q, want %s", p, legacyTables[p], tbl)
		}
	}
	if len(legacyTables) != len(want) {
		t.Fatalf("legacyTables has %d entries, want %d", len(legacyTables), len(want))
	}
}

// v0.1.17's refusals (TestUC_MiscTableForOpaqueID): an id whose prefix no
// table uses, or text with no prefix, is "unknown ID prefix".
func TestTableForIDRefusesUnknownPrefix(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"xyz_0123", "noprefix", "_abc"} {
		if _, _, _, err := tableForID(reg, bad); err == nil || !strings.Contains(err.Error(), "unknown ID prefix") {
			t.Fatalf("%q must be rejected: %v", bad, err)
		}
	}
	for p, want := range map[ids.Prefix]row.Table{"mem": "memories", "tsk": "tasks", "cmt": "comments", "inc": "incidents"} {
		got, _, _, err := tableForID(reg, string(p)+"_01a1a5ee14c6775f91d655148a6193f3")
		if err != nil || got != want {
			t.Fatalf("%s → %q %v, want %s", p, got, err, want)
		}
	}
}
