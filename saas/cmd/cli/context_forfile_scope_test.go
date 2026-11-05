package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// ovRow is a knowledge row of kind with the given text fields.
func ovRow(t *testing.T, kind string, prefix ids.Prefix, n byte, fields map[string]string) knowledgeRow {
	t.Helper()
	id, err := ids.ParseAny(string(prefix) + "_019a0000000070008000000000000" + "00" + string("0123456789abcdef"[n]))
	if err != nil {
		t.Fatal(err)
	}
	fs := map[string]value.Value{}
	for k, s := range fields {
		v, verr := value.TextOf(s)
		if verr != nil {
			t.Fatal(verr)
		}
		fs[k] = v
	}
	return knowledgeRow{kind: kind, r: renderRow{Row: row.Row{ID: id, Fields: fs}}}
}

// ovPick is a plain pick of k (what add() would make).
func ovPick(k knowledgeRow) contextPick {
	return contextPick{ID: k.r.ID.String(), Kind: k.kind, group: groupRules}
}

// TestApplyOverrides (CONTEXT_BUDGET 6.2.2, OV50): a picked narrower rule
// with --overrides turns the wider rule into a one-line note: in place
// when the wider one was picked too, appended once when it was not (it
// sits in the root core); a rule nobody picked overrides nothing; the
// note's id is never the wide rule's own id (the hook's seen set must not
// mute it elsewhere); two narrow rules overriding one wide rule give one
// note.
func TestApplyOverrides(t *testing.T) {
	wide := ovRow(t, "rule", ids.PrefixRule, 1, map[string]string{"body": "use tabs"})
	narrow := ovRow(t, "rule", ids.PrefixRule, 2, map[string]string{"body": "use spaces", schema.FieldOverrides: wide.r.ID.String()})
	narrow2 := ovRow(t, "rule", ids.PrefixRule, 3, map[string]string{"body": "two spaces", schema.FieldOverrides: wide.r.ID.String()})
	other := ovRow(t, "rule", ids.PrefixRule, 4, map[string]string{"body": "unrelated"})
	rows := []knowledgeRow{wide, narrow, narrow2, other}
	note := overrideNotePrefix + wide.r.ID.String()
	cases := []struct {
		name  string
		picks []knowledgeRow
		want  []string // resulting pick ids in order
	}{
		{name: "both picked: note in place", picks: []knowledgeRow{wide, narrow, other}, want: []string{note, narrow.r.ID.String(), other.r.ID.String()}},
		{name: "wide not picked: note appended", picks: []knowledgeRow{narrow, other}, want: []string{narrow.r.ID.String(), other.r.ID.String(), note}},
		{name: "narrow not picked: nothing", picks: []knowledgeRow{wide, other}, want: []string{wide.r.ID.String(), other.r.ID.String()}},
		{name: "two narrow rules: one note", picks: []knowledgeRow{narrow, narrow2}, want: []string{narrow.r.ID.String(), narrow2.r.ID.String(), note}},
		{name: "no picks", picks: nil, want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var picks []contextPick
			for _, k := range c.picks {
				picks = append(picks, ovPick(k))
			}
			got := applyOverrides(picks, rows)
			var gotIDs []string
			for _, p := range got {
				gotIDs = append(gotIDs, p.ID)
				if p.ID == note && (p.Kind != "override" || !strings.Contains(p.line, wide.r.ID.String()) || p.Bytes != len(p.line)) {
					t.Fatalf("note pick %+v", p)
				}
			}
			if strings.Join(gotIDs, ",") != strings.Join(c.want, ",") {
				t.Fatalf("picks %v, want %v", gotIDs, c.want)
			}
		})
	}
}

// TestScopeChainPicks (6.2.8, 6.8 step 2, OV50): rules and hotfixes of the
// folders covering the file are added innermost folder first, tagged
// "scope dir:<p>"; notes and rows of folders outside the chain are not;
// when the calling tool loads folder files itself (claude_code, cursor,
// gemini_cli) and that folder's LORE.md exists, one "in context already"
// line naming the ids replaces the rows; codex and a person get the rows.
func TestScopeChainPicks(t *testing.T) {
	scpInner, scpOuter, scpOther := "scp_inner", "scp_outer", "scp_other"
	path := map[string]string{scpInner: "src/booking/payments", scpOuter: "src/booking", scpOther: "web"}
	rInner := ovRow(t, "rule", ids.PrefixRule, 1, map[string]string{schema.FieldScope: scpInner})
	hOuter := ovRow(t, "hotfix", ids.PrefixHotfix, 2, map[string]string{schema.FieldScope: scpOuter})
	rOther := ovRow(t, "rule", ids.PrefixRule, 3, map[string]string{schema.FieldScope: scpOther})
	mInner := ovRow(t, "memory", ids.PrefixMemory, 4, map[string]string{schema.FieldScope: scpInner})
	rRoot := ovRow(t, "rule", ids.PrefixRule, 5, map[string]string{})
	rows := []knowledgeRow{rOther, rRoot, mInner, hOuter, rInner}
	root := t.TempDir()
	for _, d := range []string{"src/booking"} { // only the outer folder has a rendered LORE.md
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(d), dirFileName), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chain := []string{"src/booking/payments", "src/booking"}
	cases := []struct {
		tool string
		want []string // "<id> <why>" or "line <id>"
	}{
		{tool: "", want: []string{rInner.r.ID.String() + " scope dir:src/booking/payments", hOuter.r.ID.String() + " scope dir:src/booking"}},
		{tool: toolprofile.NameCodex, want: []string{rInner.r.ID.String() + " scope dir:src/booking/payments", hOuter.r.ID.String() + " scope dir:src/booking"}},
		{tool: toolprofile.NameClaudeCode, want: []string{rInner.r.ID.String() + " scope dir:src/booking/payments", "line dir:src/booking"}},
		{tool: toolprofile.NameCursor, want: []string{rInner.r.ID.String() + " scope dir:src/booking/payments", "line dir:src/booking"}},
	}
	for _, c := range cases {
		t.Run("tool="+c.tool, func(t *testing.T) {
			fs := fileScope{rel: "src/booking/payments/pay.go", chain: chain, path: path, root: root, tool: c.tool}
			var got []string
			scopeChainPicks(fs, rows, func(k knowledgeRow, why string) {
				got = append(got, k.r.ID.String()+" "+why)
			}, func(p contextPick) {
				if !strings.Contains(p.line, "in context already: src/booking/"+dirFileName) || !strings.Contains(p.line, hOuter.r.ID.String()) || !strings.Contains(p.line, "1 entries") {
					t.Fatalf("line %q", p.line)
				}
				got = append(got, "line "+p.ID)
			})
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("got %v\nwant %v", got, c.want)
			}
		})
	}
	// unknown repo root: a loading tool still gets the rows (fail open to the full text)
	fs := fileScope{chain: chain, path: path, tool: toolprofile.NameClaudeCode}
	n := 0
	scopeChainPicks(fs, rows, func(knowledgeRow, string) { n++ }, func(contextPick) { t.Fatal("no root: no line") })
	if n != 2 {
		t.Fatalf("%d rows without a root, want 2", n)
	}
	if fs.inChain("src/booking") != 1 || fs.inChain("web") != -1 {
		t.Fatal("inChain")
	}
}
