package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestLeakBlockedExplainAndHints (check leaks|pr-body --explain on cmd/lore):
// without --explain a text finding shows the matched note and its length;
// with it, every finding names its rule, full detail and the internal
// row's source. A PR body refusal hints at removing the text before
// posting (not the live-ref hint), and an actor-projection finding gives
// no fix hint (`lore actor delete` is refused; its detail says what to
// remove).
func TestLeakBlockedExplainAndHints(t *testing.T) {
	text := leakcheck.Finding{Rule: leakcheck.RuleText, Where: LeakWherePRBody, RowID: "mem_1", Words: 9, Label: "store agency: memories/mem_1", Detail: "9 words match internal note mem_1"}
	actor := leakcheck.Finding{Rule: leakcheck.RuleActor, Where: "push .lore/data/actors/act_1.json", RowID: "act_1", Path: ".lore/data/actors/act_1.json", Detail: "actor file carries stable_key, email: remove them"}
	cases := []struct {
		name     string
		where    string
		fs       []leakcheck.Finding
		explain  bool
		must     []string
		mustNot  []string
		wantHint string // substring; "" = no hint at all
	}{
		{"pr body, plain", LeakWherePRBody, []leakcheck.Finding{text}, false,
			[]string{"matches internal note mem_1 (9 words)"}, []string{"[text rule]", "store agency"}, "Remove the internal text before posting"},
		{"pr body, explain", LeakWherePRBody, []leakcheck.Finding{text}, true,
			[]string{"[text rule] 9 words match internal note mem_1 store agency: memories/mem_1"}, []string{"(9 words)"}, "Remove the internal text before posting"},
		{"actor finding only: no hint", LeakWherePush, []leakcheck.Finding{actor}, false,
			[]string{"actor file carries stable_key, email"}, []string{"actor delete"}, ""},
		{"actor finding, explain", LeakWherePush, []leakcheck.Finding{actor}, true,
			[]string{"[actor-projection rule] actor file carries"}, []string{"actor delete"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := leakBlocked(c.where, c.fs, c.explain)
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.LeakBlocked {
				t.Fatalf("got %v, want E_LEAK_BLOCKED", err)
			}
			all := e.Message + "\n" + e.Hint
			for _, m := range c.must {
				if !strings.Contains(e.Message, m) {
					t.Errorf("message lacks %q:\n%s", m, e.Message)
				}
			}
			for _, m := range c.mustNot {
				if strings.Contains(all, m) {
					t.Errorf("unexpected %q in:\n%s", m, all)
				}
			}
			if c.wantHint == "" && e.Hint != "" || c.wantHint != "" && !strings.Contains(e.Hint, c.wantHint) {
				t.Errorf("hint %q, want %q", e.Hint, c.wantHint)
			}
		})
	}
}
