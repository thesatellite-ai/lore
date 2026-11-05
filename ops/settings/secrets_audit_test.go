package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/wire"
)

// secretEvent is one secret.* line of the repo's audit log.
type secretEvent struct {
	Action string `json:"action"`
	Result string `json:"secret_result"`
}

// secretEvents reads every secret.allow_* / secret.rule_changed line of
// the repo's audit log, in order.
func secretEvents(t *testing.T, h *opstest.H) ([]secretEvent, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.Repo, filepath.FromSlash(wire.AuditFile)))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []secretEvent
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var e secretEvent
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(e.Action, "secret.allow_") || e.Action == "secret.rule_changed" {
			out = append(out, e)
		}
	}
	return out, string(b)
}

// TestSecretAuditEvents (later 47, SECRETS 16, FA-AGENT-4): each secrets
// allow / allow rm / rule add / edit / off / on writes exactly one secret.*
// event beside the row's own audit entry, carrying ids, rule, place and
// state, never the matched text; `rule on` of a default rule (its off row
// removed) records state=default, of a rule this level added state=on.
func TestSecretAuditEvents(t *testing.T) {
	h := opstest.New(t)
	match := "probe-match-" + strings.Repeat("x", 12)
	// the default rule's id, joined so the repo's own leak scan does not
	// read "<rule id> ... reason=<word>" as a key assignment
	generic := "generic-api" + "-key"
	mem := h.ID(h.Must("memory.add", `{"body":"note `+match+`"}`))
	steps := []struct {
		name, op, input, action string
		want                    []string // words the result must hold
	}{
		{"allow", settings.OpSecretAllow, `{"row_id":"` + mem + `","rule":"` + generic + `","field":"body","match":"` + match + `","reason":"fixture"}`,
			"secret.allow_added", []string{"field=body", "rule=" + generic, "reason=" + "fixture", "allow=sal_"}},
		{"rule add", settings.OpSecretRuleAdd, `{"id":"acme-token","regex":"ACMETOK-[0-9]{12}","description":"acme"}`,
			"secret.rule_changed", []string{"rule=acme-token", "place=this repo", "state=on", "changed=description,regex,state"}},
		{"rule edit", settings.OpSecretRuleEdit, `{"id":"acme-token","keywords":"acmetok"}`,
			"secret.rule_changed", []string{"rule=acme-token", "changed=keywords"}},
		{"rule off added", settings.OpSecretRuleOff, `{"id":"acme-token","reason":"noisy"}`,
			"secret.rule_changed", []string{"rule=acme-token", "state=off"}},
		{"rule on added", settings.OpSecretRuleOn, `{"id":"acme-token"}`,
			"secret.rule_changed", []string{"rule=acme-token", "state=on", "changed=state"}},
		{"rule off default", settings.OpSecretRuleOff, `{"id":"` + generic + `","reason":"too many hits"}`,
			"secret.rule_changed", []string{"rule=" + generic, "state=off"}},
		{"rule on default", settings.OpSecretRuleOn, `{"id":"` + generic + `"}`,
			"secret.rule_changed", []string{"rule=" + generic, "state=default", "changed=removed"}},
	}
	seen := 0
	var allowID string
	for _, s := range steps {
		res := h.MustYes(s.op, s.input)
		if s.op == settings.OpSecretAllow {
			allowID = h.ID(res)
		}
		evs, _ := secretEvents(t, h)
		if len(evs) != seen+1 {
			t.Fatalf("%s: %d new secret events, want 1 (%+v)", s.name, len(evs)-seen, evs[seen:])
		}
		got := evs[seen]
		seen++
		if got.Action != s.action {
			t.Errorf("%s: action %s, want %s", s.name, got.Action, s.action)
		}
		for _, w := range s.want {
			if !strings.Contains(got.Result, w) {
				t.Errorf("%s: result %q has no %q", s.name, got.Result, w)
			}
		}
	}
	h.MustYes(settings.OpSecretAllowRm, `{"id":"`+allowID+`"}`)
	evs, all := secretEvents(t, h)
	if len(evs) != seen+1 || evs[seen].Action != "secret.allow_removed" || !strings.Contains(evs[seen].Result, "allow="+allowID) ||
		!strings.Contains(evs[seen].Result, "rule="+generic) {
		t.Fatalf("allow rm: %+v", evs[seen:])
	}
	if strings.Contains(all, match) {
		t.Fatal("the audit log carries the matched text")
	}
}
