package settings_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// policyCase is one key of the loosening matrix: base is set at the
// higher level, then each of the nearer level's values is tried.
type policyCase struct {
	key     string
	base    string
	looser  []string // refused at the nearer level (E_POLICY_DENIED)
	tighter []string // accepted at the nearer level (equal counts)
}

// policyCases covers every shape (rank, flag, limit, limit with ceiling,
// unset-default limit, set).
var policyCases = []policyCase{
	{key: "secrets.on_detect", base: "ask", looser: []string{"redact"}, tighter: []string{"ask", "refuse"}},
	{key: "secrets.agent_reveal", base: "ask", looser: []string{"allow"}, tighter: []string{"ask"}},
	{key: "secrets.use_repo_gitleaks", base: "false", looser: []string{"true"}, tighter: []string{"false"}},
	{key: "meta.max_bytes", base: "100", looser: []string{"101", "16384"}, tighter: []string{"100", "0", "50"}},
	{key: "meta.warn_bytes", base: "100", looser: []string{"101", "4096"}, tighter: []string{"100", "0", "50"}},
	{key: "meta.max_depth", base: "3", looser: []string{"4", "6"}, tighter: []string{"3", "1"}},
	{key: "tasks.max_depth", base: "3", looser: []string{"4", "8"}, tighter: []string{"3", "1"}},
	{key: "trash.retention_days", base: "30", looser: []string{"31"}, tighter: []string{"30", "7"}},
	{key: "resources.warn_bytes", base: "2000", looser: []string{"2001", "4096"}, tighter: []string{"2000", "100"}},
	// An allow list only narrows: levels intersect (RQ2).
	{key: "resources.allowed_in", base: "store,repo", looser: []string{"store,repo,client", "client"}, tighter: []string{"store,repo", "store"}},
	// A set never loosens: every level's items join.
	{key: "agents.person_only", base: "task.delete", tighter: []string{"rule.add", "task.delete,memory.add"}},
}

// TestPolicyLooseningMatrix: a nearer level may only tighten a higher one,
// per shape and for every pair of levels the repo can see. The
// agency/org → agency pair is the org-attached folder: `--to team` names
// the linked folder under a plain name, and its rows count at the folder
// level (from the attached store, later.md orgcore2 / r4), so the company
// folder above it limits it.
func TestPolicyLooseningMatrix(t *testing.T) {
	pairs := []struct{ higher, nearer string }{
		{"kb", "repo"},
		{"kb/org", "kb/web"},
		{"kb", "kb/web"},
		{"agency/org", "agency"},
		{"agency", "repo"},
		{"kb", "agency"},
	}
	for _, p := range pairs {
		t.Run(strings.ReplaceAll(p.higher+"_to_"+p.nearer, "/", "-"), func(t *testing.T) {
			h := multiStore(t)
			for _, c := range policyCases {
				h.MustYes(settings.OpPolicySet, setIn(c.key, c.base, p.higher))
				for _, v := range c.looser {
					_, err := h.RunYes(settings.OpPolicySet, setIn(c.key, v, p.nearer))
					if !errs.Has(err, errs.PolicyDenied) {
						t.Errorf("%s=%s at %s under %s=%s at %s: %v, want E_POLICY_DENIED", c.key, v, p.nearer, c.key, c.base, p.higher, err)
						continue
					}
					if !strings.Contains(err.Error(), p.higher+" sets ") {
						t.Errorf("refusal does not name the higher place: %v", err)
					}
				}
				for _, v := range c.tighter {
					if _, err := h.RunYes(settings.OpPolicySet, setIn(c.key, v, p.nearer)); err != nil {
						t.Errorf("%s=%s at %s under %s at %s: %v", c.key, v, p.nearer, c.base, p.higher, err)
					}
				}
			}
		})
	}
}

// TestPolicyHigherMayBeLooser: the reverse order is allowed (a higher
// level is never limited by a nearer one) and the stricter value stays
// effective. Before the level fix an org-attached folder counted as the
// store root, so `--to team` refuse then `--to company` ask was refused
// as looser and show labelled the row store_root.
func TestPolicyHigherMayBeLooser(t *testing.T) {
	h := multiStore(t)
	h.MustYes(settings.OpPolicySet, setIn("secrets.on_detect", "refuse", "agency"))
	h.MustYes(settings.OpPolicySet, setIn("secrets.on_detect", "ask", "agency/org"))
	h.MustYes(settings.OpPolicySet, setIn("meta.max_bytes", "10", "repo"))
	h.MustYes(settings.OpPolicySet, setIn("meta.max_bytes", "500", "kb"))
	rules := showRules(t, h, "")
	if r := rules["secrets.on_detect"]; r.Value != "refuse" || r.Level != "folder" || r.Store != "agency" || !strings.HasPrefix(r.Source, "folder agency (pol_…") {
		t.Errorf("on_detect %+v", r)
	}
	if r := rules["meta.max_bytes"]; r.Value != "10" || r.Level != "repo" || !strings.HasPrefix(r.Source, "repo this repo (pol_…") {
		t.Errorf("max_bytes %+v", r)
	}
	// Unset the nearer row: the higher (looser) one applies again.
	h.MustYes(settings.OpPolicyUnset, `{"key":"secrets.on_detect","to":"agency"}`)
	if r := showRules(t, h, "secrets.on_detect")["secrets.on_detect"]; r.Value != "ask" || r.Level != "org" {
		t.Errorf("after unset %+v", r)
	}
}

// TestPolicySetRefusals: invalid input never writes.
func TestPolicySetRefusals(t *testing.T) {
	h := multiStore(t)
	cases := []struct {
		in   string
		code errs.Code
	}{
		{setIn("secrets.whatever", "x", ""), errs.InvalidInput},
		{setIn("", "x", ""), errs.InvalidInput},
		{setIn("secrets.on_detect", "bogus", ""), errs.InvalidInput},
		{setIn("secrets.on_detect", "", ""), errs.InvalidInput},
		{setIn("secrets.on_detect", "REFUSE", ""), errs.InvalidInput},
		{setIn("secrets.use_repo_gitleaks", "maybe", ""), errs.InvalidInput},
		{setIn("meta.max_bytes", "-1", ""), errs.InvalidInput},
		{setIn("meta.max_bytes", "ten", ""), errs.InvalidInput},
		{setIn("tasks.max_depth", "9", ""), errs.InvalidInput},
		{setIn("secrets.on_detect", "refuse", "nowhere"), errs.NotFound},
	}
	for _, c := range cases {
		if _, err := h.RunYes(settings.OpPolicySet, c.in); !errs.Has(err, c.code) {
			t.Errorf("%s: %v, want %s", c.in, err, c.code)
		}
	}
	// policy set and unset always ask first.
	h.Fails(settings.OpPolicySet, setIn("secrets.on_detect", "refuse", ""), errs.NeedsConfirm)
	h.Fails(settings.OpPolicyUnset, `{"key":"secrets.on_detect"}`, errs.NeedsConfirm)
	for k, r := range showRules(t, h, "") {
		if r.Source != "default" {
			t.Errorf("%s written by a refused set: %+v", k, r)
		}
	}
	if _, err := h.RunYes(settings.OpPolicyUnset, `{"key":"meta.max_bytes"}`); !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "meta.max_bytes is not set in this repo") {
		t.Errorf("unset of an unset key: %v", err)
	}
}

// TestPolicySetUpdatesOneRow: setting a key twice at one level edits the
// same row (one policies row per key and place).
func TestPolicySetUpdatesOneRow(t *testing.T) {
	h := multiStore(t)
	a := h.ID(h.MustYes(settings.OpPolicySet, setIn("meta.max_bytes", "50", "")))
	res := h.MustYes(settings.OpPolicySet, setIn("meta.max_bytes", "40", ""))
	if b := h.ID(res); b != a {
		t.Errorf("second set wrote a new row %s (first %s)", b, a)
	}
	want := "✓ meta.max_bytes = 40 (this repo, pol_…" + a[len(a)-4:] + ")\n"
	if got := opstest.Text(res); got != want {
		t.Errorf("text %q want %q", got, want)
	}
	// A set key's items are split on commas and trimmed (" rule.add,," is
	// one item), printed joined by ", ".
	if got := opstest.Text(h.MustYes(settings.OpPolicySet, setIn("agents.person_only", "task.delete, rule.add,,", "kb/web"))); !strings.HasPrefix(got, "✓ agents.person_only = task.delete, rule.add (kb/web, pol_…") {
		t.Errorf("set text %q", got)
	}
	if r := showRules(t, h, "agents")["agents.person_only"]; r.Value != "task.delete, rule.add" || r.Level != "folder" {
		t.Errorf("set show %+v", r)
	}
}

// TestPolicyShowText: the CLI_REFERENCE 3.11 lines (key, value or
// "(unset)", source), sorted by key, a prefix filter, and the empty case.
func TestPolicyShowText(t *testing.T) {
	h := multiStore(t)
	// Q-R2-3 added context.targets (unset by default: detect from files);
	// XQ2 added batch.preview_rows (default 20); RQ2/RQ4 resources.allowed_in, warn_bytes;
	// pass 2: resources.stale_after, secrets.password_pair_prose_filter (SECRETS 3.4), tasks.urgency.*; fix-all: tasks.auto_start_parent, tasks.max_children, trash.purge_on_delete, meta.max_key_length, meta.max_keys_per_registry, meta.render_keys_budget (FA-META-11), meta.scope_aliases (FA-META-12); wave 3: tasks.triage_owner (FB-TASK-3), time.client_key (FB-TASK-4).
	want := "agents.person_only\t(unset)\tdefault\nbatch.preview_rows\t20\tdefault\ncontext.budget.claude_code\t32000\tdefault\ncontext.budget.codex\t16000\tdefault\ncontext.budget.copilot\t16000\tdefault\ncontext.budget.cursor\t16000\tdefault\ncontext.budget.default\t16000\tdefault\ncontext.budget.dir\t8000\tdefault\ncontext.budget.gemini_cli\t32000\tdefault\ncontext.budget.generic\t16000\tdefault\ncontext.budget.path_file\t8000\tdefault\ncontext.chain_max.codex\t32768\tdefault\ncontext.dir_files\ttrue\tdefault\ncontext.dir_min_entries\t3\tdefault\ncontext.entry_max\t1500\tdefault\ncontext.full_file\ttrue\tdefault\ncontext.hook_cap\t9000\tdefault\ncontext.hooks.prompt_search\tfalse\tdefault\ncontext.targets\t(unset)\tdefault\ncontext.warn_at\t80\tdefault\ndelete.require_reason\tfalse\tdefault\nmeta.max_bytes\t16384\tdefault\nmeta.max_depth\t4\tdefault\nmeta.max_key_length\t64\tdefault\nmeta.max_keys_per_registry\t500\tdefault\nmeta.render_keys_budget\t6144\tdefault\nmeta.resource_hint_values\t30\tdefault\nmeta.scope_aliases\t(unset)\tdefault\nmeta.stale_after\t168\tdefault\nmeta.warn_bytes\t4096\tdefault\nresources.allowed_in\tstore, repo, client\tdefault\nresources.max_bytes\t16384\tdefault\nresources.stale_after\t24\tdefault\nresources.warn_bytes\t4096\tdefault\n" +
		"routing.client_repos\t(unset)\tdefault\nsecrets.agent_reveal\task\tdefault\nsecrets.manager_timeout\t5\tdefault\nsecrets.on_detect\task\tdefault\nsecrets.password_pair_prose_filter\ttrue\tdefault\n" +
		"secrets.use_repo_gitleaks\ttrue\tdefault\ntasks.auto_start_parent\tfalse\tdefault\ntasks.auto_watch\ttrue\tdefault\ntasks.estimate_unit\tpoints\tdefault\ntasks.history_max\t200\tdefault\ntasks.max_children\t200\tdefault\ntasks.max_depth\t4\tdefault\ntasks.parent_autoclose\tsuggest\tdefault\ntasks.sla.high\t(unset)\tdefault\ntasks.sla.low\t(unset)\tdefault\ntasks.sla.medium\t(unset)\tdefault\ntasks.sla.urgent\t(unset)\tdefault\ntasks.stale_days\t(unset)\tdefault\ntasks.triage_owner\t(unset)\tdefault\ntasks.urgency.age\t(unset)\tdefault\ntasks.urgency.blocking\t(unset)\tdefault\ntasks.urgency.due\t(unset)\tdefault\ntasks.urgency.in_progress\t(unset)\tdefault\ntasks.urgency.mission_target\t(unset)\tdefault\ntasks.urgency.next_tag\t(unset)\tdefault\ntasks.urgency.priority.high\t(unset)\tdefault\ntasks.urgency.priority.low\t(unset)\tdefault\ntasks.urgency.priority.medium\t(unset)\tdefault\ntasks.urgency.priority.urgent\t(unset)\tdefault\ntasks.urgency.proposed\t(unset)\tdefault\ntasks.urgency.sla_risk\t(unset)\tdefault\ntasks.week_end\tfri\tdefault\ntime.client_key\tclient\tdefault\ntime.client_share\tnone\tdefault\ntime.coalesce_gap\t2\tdefault\ntime.long_span\t600\tdefault\ntime.max_entry\t1440\tdefault\ntrash.purge_on_delete\tfalse\tdefault\ntrash.retention_days\t(unset)\tdefault\n"
	if got := opstest.Text(h.Must(settings.OpPolicyShow, ``)); got != want {
		t.Errorf("defaults:\n%s\nwant\n%s", got, want)
	}
	if got := opstest.Text(h.Must(settings.OpPolicyShow, `{"id":"zzz"}`)); got != "(no matching policy keys)\n" {
		t.Errorf("no match %q", got)
	}
	if got := opstest.Text(h.Must(settings.OpPolicyShow, `{"id":"secrets.on"}`)); got != "secrets.on_detect\task\tdefault\n" {
		t.Errorf("prefix %q", got)
	}
}

func setIn(key, value, to string) string {
	return `{"key":` + opstest.Quote(key) + `,"value":` + opstest.Quote(value) + `,"to":` + opstest.Quote(to) + `}`
}

// showRules is policy.show's rules by key.
func showRules(t *testing.T, h *opstest.H, prefix string) map[string]settings.PolicyRule {
	t.Helper()
	var out settings.PolicyOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpPolicyShow, `{"id":`+opstest.Quote(prefix)+`}`))), &out); err != nil {
		t.Fatal(err)
	}
	m := map[string]settings.PolicyRule{}
	for _, r := range out.Data {
		m[r.Key] = r
	}
	return m
}

// TestEstimateUnitNote (TSQ4, OV59): changing tasks.estimate_unit asks
// first like every policy set, and once confirmed says how many estimates
// were not converted; the same unit again, or a project without
// estimates, prints no such line.
func TestEstimateUnitNote(t *testing.T) {
	h := opstest.New(t)
	const line = "were not converted"
	if got := opstest.Text(h.MustYes(settings.OpPolicySet, setIn("tasks.estimate_unit", "hours", ""))); strings.Contains(got, line) {
		t.Fatalf("no estimates yet, got a note: %q", got)
	}
	h.Seed("tasks", map[string]string{"title": `"a"`, "estimate": `"3"`})
	h.Seed("tasks", map[string]string{"title": `"b"`})
	h.Fails(settings.OpPolicySet, setIn("tasks.estimate_unit", "days", ""), errs.NeedsConfirm)
	got := opstest.Text(h.MustYes(settings.OpPolicySet, setIn("tasks.estimate_unit", "days", "")))
	if !strings.Contains(got, "1 task estimate(s) "+line+": they now read as days, not hours") {
		t.Fatalf("unit change: %q", got)
	}
	if got = opstest.Text(h.MustYes(settings.OpPolicySet, setIn("tasks.estimate_unit", "days", ""))); strings.Contains(got, line) {
		t.Fatalf("same unit again, got a note: %q", got)
	}
}
