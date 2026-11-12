//go:build userscen

package userscen

// `lore lint` and `lore cache prune`, as a person cleaning up uses them.
//
// lint (ops/lint):
//   - a clean project: "lint: no findings", --json {"findings":[],"count":0}
//   - references: ${row:<trashed id>} dangling_ref with the trash-restore
//     fix, ${var:<unset>} dangling_ref with the vars-set fix, ${nope:x}
//     unknown_scheme; each fix run as printed clears its finding
//   - --code filters (repeatable); an unknown code is E_INVALID_INPUT
//     (exit 20) listing the known codes
//   - findings are a report, not a failure: exit 0 (help and reference
//     name no failing exit for findings)
//   - --context: no_summary and untagged on a pinned rule, with fixes
//   - a secret in a hand-edited row file: secret_found with a masked
//     preview, never the value, in text and --json
//
// cache prune (ops/cache):
//   - nothing to prune: one line, exit 0
//   - an orphan v0.1.17 project cache: --dry-run lists it with its size
//     and removes nothing; without --yes and no terminal E_NEEDS_CONFIRM
//     (exit 26) and nothing removed; --yes removes it; a second --yes has
//     nothing left; the repo's own cache is never touched

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// lintOut is `lore lint --json`.
type lintOut struct {
	Findings []struct {
		Code    string `json:"code"`
		ID      string `json:"id"`
		Store   string `json:"store"`
		Message string `json:"message"`
		Fix     string `json:"fix"`
	} `json:"findings"`
	Count int `json:"count"`
}

func (p *doctorPerson) lint(t *testing.T, args ...string) lintOut {
	t.Helper()
	res := p.c.MustLore(append([]string{"lint", "--json"}, args...)...)
	var out lintOut
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil || out.Count != len(out.Findings) {
		t.Fatalf("lint --json: %v %s", err, res)
	}
	return out
}

// has reports a finding with code whose message and fix contain the parts.
func (o lintOut) has(code, msg, fix string) bool {
	for _, f := range o.Findings {
		if f.Code == code && strings.Contains(f.Message, msg) && strings.Contains(f.Fix, fix) {
			return true
		}
	}
	return false
}

var memIDRe = regexp.MustCompile(`mem_[0-9a-f]{32}`)

func TestDoctorLint_CleanAndReferences(t *testing.T) {
	p := newDoctorPerson(t)
	if res := p.c.MustLore("lint"); res.Stdout != "lint: no findings\n" {
		t.Fatalf("clean lint text: %q", res.Stdout)
	}
	// compared as JSON: every --json output is indented (surface/cli printJSON)
	if res := p.c.MustLore("lint", "--json"); !sameJSON(res.Stdout, `{"findings":[],"count":0}`) {
		t.Fatalf("clean lint json: %q", res.Stdout)
	}

	tmp := ruleIDRe.FindString(p.c.MustLore("rule", "add", "--body", "a rule that goes away").Stdout)
	mem := memIDRe.FindString(p.c.MustLore("memory", "add", "--body", "see ${row:"+tmp+"} on ${var:deploy_host}; ${nope:x}").Stdout)
	if tmp == "" || mem == "" {
		t.Fatal("ids")
	}
	p.c.MustLore("rule", "delete", tmp, "--yes")

	out := p.lint(t)
	if out.Count != 3 ||
		!out.has("dangling_ref", "body: ${row:"+tmp+"} points at no live row", "lore trash restore "+tmp) ||
		!out.has("dangling_ref", "body: ${var:deploy_host} names no variable", "lore vars set deploy_host <value>") ||
		!out.has("unknown_scheme", "body: ${nope:x} names no known scheme", "write $${…} to keep it literal") {
		t.Fatalf("three reference findings on %s: %+v", mem, out)
	}
	for _, f := range out.Findings {
		if f.ID != mem || f.Store != "repo" {
			t.Fatalf("each finding names the memory and its store: %+v", f)
		}
	}
	// Text form: code, id, store, message, fix line, count; exit 0.
	text := p.c.Lore("lint")
	if text.Code != 0 || !strings.Contains(text.Stdout, "dangling_ref "+mem+" (repo): body: ${var:deploy_host} names no variable\n  fix: lore vars set deploy_host <value>\n") ||
		!strings.HasSuffix(text.Stdout, "3 finding(s)\n") {
		t.Fatalf("lint text with findings (exit 0, a report): %s", text)
	}

	// --code filters, repeatable.
	if o := p.lint(t, "--code", "unknown_scheme"); o.Count != 1 || o.Findings[0].Code != "unknown_scheme" {
		t.Fatalf("--code unknown_scheme: %+v", o)
	}
	if o := p.lint(t, "--code", "dangling_ref", "--code", "unknown_scheme"); o.Count != 3 {
		t.Fatalf("--code twice: %+v", o)
	}
	if o := p.lint(t, "--code", "secret_found"); o.Count != 0 {
		t.Fatalf("--code with no match: %+v", o)
	}
	for _, bad := range []string{"NO_SUCH", "Dangling_Ref", " "} {
		p.c.Lore("lint", "--code", bad).Fails(t, 20, "E_INVALID_INPUT", "lint: unknown code", "known codes: unknown_scheme, dangling_ref")
	}

	// Run the printed fixes: each clears its finding.
	p.c.MustLore("vars", "set", "deploy_host", "example.org")
	p.c.MustLore("trash", "restore", tmp)
	out = p.lint(t)
	if out.Count != 1 || out.Findings[0].Code != "unknown_scheme" {
		t.Fatalf("after vars set and trash restore only the unknown scheme is left: %+v", out)
	}
	p.c.MustLore("memory", "edit", mem, "--body", "see ${row:"+tmp+"} on ${var:deploy_host}; $${nope:x}")
	if out = p.lint(t); out.Count != 0 {
		t.Fatalf("after escaping the literal: %+v", out)
	}
}

func TestDoctorLint_ContextFindings(t *testing.T) {
	p := newDoctorPerson(t)
	id := ruleIDRe.FindString(p.c.MustLore("rule", "list").Stdout)
	out := p.lint(t, "--context")
	if !out.has("no_summary", "pinned rule has no summary", "lore rule edit "+id+" --summary") ||
		!out.has("untagged", "pinned but untagged", "lore rule edit "+id+" --tag <tag> (create a new tag first: lore tag add --name=<tag>)") {
		t.Fatalf("context findings on an untagged pinned rule: %+v", out)
	}
	// Plain lint does not include the context checks.
	if o := p.lint(t); o.has("no_summary", "", "") || o.has("untagged", "", "") {
		t.Fatalf("plain lint runs no context checks: %+v", o)
	}
	p.c.MustLore("rule", "edit", id, "--summary", "Tests before push")
	// The fixes as printed: a new tag is created first, then attached.
	p.c.MustLore("tag", "add", "--name=testing")
	p.c.MustLore("rule", "edit", id, "--tag", "testing")
	if o := p.lint(t, "--context"); o.has("no_summary", "", "") || o.has("untagged", "", "") {
		t.Fatalf("the fixes clear the context findings: %+v", o)
	}
}

func TestDoctorLint_StoredSecretIsMasked(t *testing.T) {
	p := newDoctorPerson(t)
	secret := githubPAT("lint")
	rel := p.ruleFile(t)
	body, _ := p.c.ReadFile(rel)
	// Written past the hooks, as a hand edit or an old binary would.
	p.c.WriteFile(rel, strings.Replace(body, `"body": "Run the tests before you push"`, `"body": "Run the tests; token `+secret+`"`, 1))

	res := p.c.MustLore("lint", "--json")
	var out lintOut
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		t.Fatal(err)
	}
	if !out.has("secret_found", "body looks like a secret (", "lore secrets allow ") {
		t.Fatalf("a stored token is a secret_found finding: %s", res)
	}
	text := p.c.MustLore("lint", "--code", "secret_found")
	if strings.Contains(res.Stdout, secret) || strings.Contains(text.Out(), secret) {
		t.Fatalf("lint prints the secret value: %s\n%s", res, text)
	}
}

func TestDoctorCachePrune_OrphanProjectCache(t *testing.T) {
	p := newDoctorPerson(t)
	nowhere := filepath.Join(p.w.Root, "nowhere")
	none := p.a.Lore(nowhere, "cache", "prune", "--dry-run").OK(t)
	if none.Stdout != "nothing to prune: every cache file belongs to a store a repo on this machine attaches\n" {
		t.Fatalf("nothing to prune: %q", none.Stdout)
	}
	// A v0.1.17 per-project cache of a project no repo here has.
	orphan := p.a.LorePath("cache", "prj_01a1b49c577f795e92a446d6dad2cc7b.db")
	if err := os.WriteFile(orphan, []byte(strings.Repeat("x", 5000)), 0o600); err != nil {
		t.Fatal(err)
	}
	repoCache := p.repoCache()

	dry := p.a.Lore(nowhere, "cache", "prune", "--dry-run").OK(t)
	if !strings.Contains(dry.Stdout, orphan+"  4.9 KB\n") || !strings.Contains(dry.Stdout, "1 file(s), 4.9 KB\n--dry-run: nothing removed") {
		t.Fatalf("--dry-run lists the orphan with its size: %s", dry)
	}
	var plan struct {
		Files []struct {
			Path string `json:"path"`
			Size int    `json:"size"`
		} `json:"files"`
		Count   int  `json:"count"`
		Bytes   int  `json:"bytes"`
		DryRun  bool `json:"dry_run"`
		Removed bool `json:"removed"`
	}
	js := p.a.Lore(nowhere, "cache", "prune", "--dry-run", "--json").OK(t)
	if err := json.Unmarshal([]byte(js.Stdout), &plan); err != nil || plan.Count != 1 || plan.Bytes != 5000 || !plan.DryRun || plan.Removed ||
		len(plan.Files) != 1 || plan.Files[0].Path != orphan {
		t.Fatalf("--dry-run --json: %v %s", err, js)
	}

	p.a.Lore(nowhere, "cache", "prune").Fails(t, 26, "E_NEEDS_CONFIRM", "Delete these cache files?", "lore cache prune --yes")
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("an unconfirmed prune deleted the file: %v", err)
	}
	yes := p.a.Lore(nowhere, "cache", "prune", "--yes").OK(t)
	if !strings.Contains(yes.Stdout, "✓ removed 1 file(s), 4.9 KB") {
		t.Fatalf("prune --yes: %s", yes)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("the orphan is gone: %v", err)
	}
	if _, err := os.Stat(repoCache); err != nil {
		t.Fatalf("prune never touches a repo's own cache: %v", err)
	}
	again := p.a.Lore(nowhere, "cache", "prune", "--yes").OK(t)
	if !strings.HasPrefix(again.Stdout, "nothing to prune") {
		t.Fatalf("a second prune has nothing left: %s", again)
	}
	p.c.MustLore("rule", "list")
}
