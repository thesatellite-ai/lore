//go:build stories && !windows

package stories

// WU_up3budget_test.go — owner use pass 3 wrong 3 and weak 11 (devco
// D-636, D-637) on a repo linked to an org folder: the context budget
// warning `lore sync` prints named no place, so its raise fell to --to
// repo there and was refused (E_STORE_READ_ONLY); and it told every member
// to demote a company must rule, which only an admin of the org may do.
//
//	D-636: each printed fix runs exactly as printed, in the linked repo
//	D-637: a member is told which admin to ask about a company row, never
//	       given its command; a member's demotion of a company must rule or
//	       policy write to the org folder is refused; a folder's stays theirs
//	D-664: `pin set --context` / `pin unset` of a company must rule is an
//	       admin's change
//	D-681: every folder of the company store is company ground: a member
//	       in a linked project-folder repo is refused a demotion, a pin and
//	       a policy write there, and the warning labels those commands as
//	       the admin's
//	D-694: a member's lore branch share|isolate (their --undo, and every
//	       branches.* policy) is refused in a company folder like any
//	       policy there (the D-681 exemption is reversed); a member adds
//	       rules only in their own team's folder
//	D-658: a budget set at the store root names that store in its raise
//	       (never the literal `<store>`), and the raise runs as printed
//	D-725: the over-budget warning goes only to who can fix it (an admin
//	       of the folder), once per sync; the default budget (32000) holds
//	       a real folder's pinned part

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Names of the budget story (made up).
const (
	wubFolder = "web"
	wubKey    = "context.budget.internal"
	wubJoin   = "acme"
	wubRoot   = "kb" // the whole store attached in a repo set up with lore init (D-658)
	// wubWarn is the over-budget warning's own words (D-725: an admin's only)
	wubWarn = "pinned rules and hotfixes of the internal stores are"
)

// wubRuleID finds a rule id in a list.
var wubRuleID = regexp.MustCompile(`rul_[0-9a-f]+`)

// wubMember is the member of story WU's budget walk.
var wubMember = journeys.Person{Name: "Mo Member", Email: "mo@example.test", Login: "mo-gh"}

func TestStoryWUBudgetWarn(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WU"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(pc.Dir, "member", "add", "--email", wubMember.Email, "--name", wubMember.Name, "--handle", "mo", "--role", "member", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wubFolder).OK(t)
	// devco D-694: a member adds rules and notes only in the folder of a
	// team they are on; Mo is on @web, whose folder is web
	m.Lore(pc.Dir, "team", "add", "--handle", "@"+wubFolder, "--members", "mo").OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wubFolder, "--own", "--yes").OK(t)
	inWeb := wOrg + "/" + wubFolder
	m.Lore(wc.Dir, "rule", "add", "--to", wOrg+"/org", "--severity", "must", "--body", strings.Repeat("every company service logs in UTC and keeps request ids. ", 40), "--yes").OK(t)
	m.Lore(wc.Dir, "policy", "set", "--key", wubKey, "--value", "1000", "--to", inWeb, "--yes").OK(t)
	m.Lore(wc.Dir, "sync").OK(t)
	mo := s.Persona(wubMember.Name, wubMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, mo, wubMember.Login)
	mc := fCodeClone(s, mo, web, "web")
	journeys.Join(t, mo, mc.Dir, wubJoin, store.SSHURL(), wubMember, []string{wFolders})
	company := wubRuleIn(t, m, wc.Dir, wOrg+"/org")

	s.Step("D-636/admin-fix-works-where-printed", func() {
		m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "the web app ships behind a flag", "--yes").OK(t)
		r := m.Lore(wc.Dir, "sync").OK(t)
		raise := wubPrinted(t, r.Out(), "raise the budget with `")
		if !strings.Contains(raise, "--to "+inWeb) {
			t.Fatalf("devco:D-636: the raise names no linked folder: %s\n%s", raise, r)
		}
		wubRun(t, m, wc.Dir, raise).Says(t, wubKey)
		// an admin may lower the company rule: it is offered as a command
		if lower := wubPrinted(t, r.Out(), "take a row out of the pinned part with `"); !strings.Contains(lower, company) {
			t.Errorf("devco:D-637: the admin is not offered the company rule's command: %s", lower)
		}
	})

	s.Step("D-725/only-admins-are-warned-once", func() {
		// devco D-725 (owner, replaces D-637's ask-an-admin warning): a
		// member of a company folder may change neither its pinned rows
		// nor its budget, so the over-budget warning is not theirs: every
		// sync warned every member. The admin of the folder gets it once
		// per sync, with commands that run as printed.
		m.Lore(wc.Dir, "policy", "set", "--key", wubKey, "--value", "1000", "--to", inWeb, "--yes").OK(t)
		m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "web errors carry a request id", "--yes").OK(t)
		aout := m.Lore(wc.Dir, "sync").OK(t).Out()
		if n := strings.Count(aout, wubWarn); n != 1 {
			t.Fatalf("devco:D-725: the admin's sync warns %d times, want once:\n%s", n, aout)
		}
		raise := wubPrinted(t, aout, "raise the budget with `")
		out := mo.Lore(mc.Dir, "sync").OK(t).Out()
		for _, bad := range []string{wubWarn, "lore policy set", "lore rule edit", "the admin would run"} {
			if strings.Contains(out, bad) {
				t.Fatalf("devco:D-725: a member's sync prints %q (a fix only an admin may make):\n%s", bad, out)
			}
		}
		// --report gives the member the sizes and no command
		mo.Lore(mc.Dir, "render", "--report").OK(t).Says(t, "the pinned part alone is", "changed by an admin of the org").Never(t, "lore policy set", "lore rule edit")
		// what the admin was told is the admin's: refused for the member, runs for the admin
		mo.Lore(mc.Dir, wubFields(t, raise)...).Refused(t, "E_REFUSED", "only an admin of the org")
		wubRun(t, m, wc.Dir, raise).Says(t, wubKey)
	})

	s.Step("D-658/store-root-budget-names-the-store", func() {
		// a repo set up here with the whole store attached (its root): a
		// budget set there is a store-root value, and its raise printed
		// the literal `--to <store>`. It names the store and runs exactly
		// as printed.
		m.Lore(pc.Dir, "init", "--yes").OK(t)
		m.Lore(pc.Dir, "store", "add", "--name", wubRoot, "--url", store.SSHURL(), "--yes").OK(t)
		m.Lore(pc.Dir, "rule", "add", "--severity", "must", "--body", strings.Repeat("the platform keeps its migrations reversible. ", 30), "--yes").OK(t)
		m.Lore(pc.Dir, "policy", "set", "--key", wubKey, "--value", "900", "--to", wubRoot, "--yes").OK(t)
		r := m.Lore(pc.Dir, "render").OK(t)
		if strings.Contains(r.Out(), "<store") {
			t.Fatalf("devco:D-658: the budget fix prints a placeholder store:\n%s", r)
		}
		raise := wubPrinted(t, r.Out(), "raise the budget with `")
		if !strings.Contains(raise, "--to "+wubRoot+" ") {
			t.Fatalf("devco:D-658: the store-root raise does not name the store %s: %s\n%s", wubRoot, raise, r)
		}
		wubRun(t, m, pc.Dir, raise).Says(t, wubKey)
	})

	s.Step("D-659/member-cannot-bulk-change-company-rows", func() {
		// every bulk and generic path runs the one-row forms' role check
		all := "created_at>2000-01-01"
		mo.Lore(mc.Dir, "rule", "add", "--to", inWeb, "--severity", "should", "--body", "web pages keep one h1", "--yes").OK(t)
		r := mo.Lore(mc.Dir, "rule", "delete", "--where", all, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", company, wAdmin.Name, "nothing was written")
		mo.Lore(mc.Dir, "rule", "delete", "--where", "severity=must", "--dry-run").Refused(t, "E_REFUSED", company)
		mo.Lore(mc.Dir, "rule", "archive", "--where", all).Refused(t, "E_REFUSED", "only an admin of the org", company)
		mo.Lore(mc.Dir, "row", "move", "--id", company, "--to", inWeb, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", company)
		md := mo.Lore(mc.Dir, "row", "markdown", "--id", company).OK(t).Out()
		if !strings.Contains(md, "severity: must") {
			t.Fatalf("no severity line in the view:\n%s", md)
		}
		mo.Lore(mc.Dir, "row", "edit", "--id", company, "--markdown", strings.Replace(md, "severity: must", "severity: should", 1), "--yes").Refused(t, "E_REFUSED", "only an admin of the org")
		// a company critical hotfix: one-row and bulk archive/delete too
		m.Lore(wc.Dir, "hotfix", "add", "--to", wOrg+"/org", "--title", "pager rotates at noon", "--body", "page the next on call at noon UTC", "--severity", "critical", "--yes").OK(t)
		m.Lore(wc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "sync").OK(t)
		hfx := regexp.MustCompile(`hfx_[0-9a-f]+`).FindString(mo.Lore(mc.Dir, "hotfix", "list", "--store", wOrg+"/org").OK(t).Out())
		mo.Lore(mc.Dir, "hotfix", "archive", "--id", hfx).Refused(t, "E_REFUSED", "only an admin of the org")
		mo.Lore(mc.Dir, "hotfix", "delete", "--id", hfx, "--yes").Refused(t, "E_REFUSED", "only an admin of the org")
		mo.Lore(mc.Dir, "hotfix", "archive", "--where", "severity=critical").Refused(t, "E_REFUSED", hfx)
		mo.Lore(mc.Dir, "rule", "show", "--id", company).OK(t).Says(t, "must")
		// the member's own way on runs exactly as printed and leaves the company rule out
		narrow := wubPrinted(t, strings.ReplaceAll(r.Out(), "--dry-run", "--dry-run`"), "only the rest: ")
		got := mo.Lore(mc.Dir, wubFields(t, narrow)...).OK(t).Says(t, "would delete")
		if strings.Contains(got.Out(), company) {
			t.Fatalf("devco:D-659: the narrowed bulk form still names the company rule:\n%s", got)
		}
		// the admin's printed line is the admin's to run
		m.Lore(wc.Dir, "rule", "delete", "--where", "severity=must", "--dry-run").OK(t).Says(t, company)
	})

	s.Step("D-664/member-cannot-pin-company-rows", func() {
		// `pin set --context line` cuts a company must rule to one line in
		// every repo of the org: an admin's change, as its demotion is
		mo.Lore(mc.Dir, "pin", "set", "--id", company, "--context", "line").Refused(t, "E_REFUSED", "only an admin of the org", company, wAdmin.Name, "nothing was written", "lore pin set --id "+company+" --context line")
		mo.Lore(mc.Dir, "pin", "set", "--id", company, "--context", "full").Refused(t, "E_REFUSED", "only an admin of the org")
		// the admin's printed line is the admin's to run, and the pin is theirs to take off
		m.Lore(wc.Dir, "pin", "set", "--id", company, "--context", "line").OK(t).Says(t, "pinned")
		m.Lore(wc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "pin", "unset", "--id", company).Refused(t, "E_REFUSED", "only an admin of the org", "lore pin unset --id "+company)
		m.Lore(wc.Dir, "pin", "unset", "--id", company).OK(t)
		// devco D-681: a project folder's must rule is company ground too
		own := wubRuleID.FindString(mo.Lore(mc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "web forms validate on the server", "--yes").OK(t).Out())
		mo.Lore(mc.Dir, "pin", "set", "--id", own, "--context", "line").Refused(t, "E_REFUSED", "only an admin of the org", "lore pin set --id "+own+" --context line")
	})

	s.Step("D-637/member-cannot-demote-company-rows", func() {
		mo.Lore(mc.Dir, "rule", "edit", "--id", company, "--severity", "should").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name, "nothing was written")
		mo.Lore(mc.Dir, "rule", "delete", "--id", company, "--yes").Refused(t, "E_REFUSED", "only an admin of the org")
		mo.Lore(mc.Dir, "rule", "archive", "--id", company).Refused(t, "E_REFUSED", "only an admin of the org")
		mo.Lore(mc.Dir, "policy", "set", "--key", wubKey, "--value", "30000", "--to", wOrg+"/org", "--yes").Refused(t, "E_REFUSED", "only an admin of the org")
		mo.Lore(mc.Dir, "rule", "show", "--id", company).OK(t).Says(t, "must")
		// devco D-681: a project folder's must rule and policy are company ground too
		web := wubRuleID.FindString(m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "web releases are tagged", "--yes").OK(t).Out())
		m.Lore(wc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "rule", "edit", "--id", web, "--severity", "should").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name, "nothing was written")
		mo.Lore(mc.Dir, "policy", "set", "--key", wubKey, "--value", "30000", "--to", inWeb, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name)
		mo.Lore(mc.Dir, "rule", "show", "--id", web).OK(t).Says(t, "must")
		// a should rule stays every member's
		should := wubRuleID.FindString(mo.Lore(mc.Dir, "rule", "add", "--to", inWeb, "--severity", "should", "--body", "web copy uses sentence case", "--yes").OK(t).Out())
		mo.Lore(mc.Dir, "rule", "edit", "--id", should, "--severity", "may").OK(t)
		// the admin still may
		m.Lore(wc.Dir, "rule", "edit", "--id", company, "--severity", "should").OK(t)
	})

	s.Step("D-694/branch-choice-is-the-admins", func() {
		// devco D-694 (owner use pass 5, reverses the D-681 exemption): a
		// branch's mode in a company folder is a policy for everyone in
		// it, so a member's lore branch share|isolate, their --undo and a
		// policy set|unset of any branches.* key are refused with the
		// ask-an-admin hint; the admin may, with --yes.
		mo.Lore(mc.Dir, "branch", "share", "--branch", "feat/mo-share", "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name, "nothing was written")
		mo.Lore(mc.Dir, "branch", "isolate", "--branch", "feat/mo-own", "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name, "nothing was written")
		mo.Lore(mc.Dir, "policy", "set", "--key", "branches.shared", "--value", "feat/mo-share,feat/mo-two", "--to", inWeb, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name)
		mo.Lore(mc.Dir, "policy", "set", "--key", "branches.main_like", "--value", "feat/*", "--to", inWeb, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name)
		mo.Lore(mc.Dir, "policy", "set", "--key", wubKey, "--value", "30000", "--to", inWeb, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name)
		m.Lore(wc.Dir, "branch", "share", "--branch", "feat/mo-share", "--yes").OK(t).Says(t, "feat/mo-share", "branches.shared")
		m.Lore(wc.Dir, "branch", "isolate", "--branch", "feat/mo-own", "--yes").OK(t).Says(t, "feat/mo-own", "branches.isolated")
		m.Lore(wc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "branch", "share", "--branch", "feat/mo-share", "--undo", "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name)
		mo.Lore(mc.Dir, "branch", "isolate", "--branch", "feat/mo-own", "--undo", "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name)
		mo.Lore(mc.Dir, "policy", "unset", "--key", "branches.isolated", "--to", inWeb, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wAdmin.Name)
		m.Lore(wc.Dir, "branch", "share", "--branch", "feat/mo-share", "--undo", "--yes").OK(t).Says(t, "removed the share choice")
		m.Lore(wc.Dir, "policy", "unset", "--key", "branches.isolated", "--to", inWeb, "--yes").OK(t)
	})

	s.Step("D-725/default-budget-holds-a-real-folder", func() {
		// devco D-725: the default 8000 sat under the pinned part of most
		// real folders (17-23 KB in 300-row folders); with no budget set,
		// a folder pinning ~20 KB renders with no warning for anyone
		m.Lore(pc.Dir, "policy", "unset", "--key", wubKey, "--to", wubRoot, "--yes").OK(t)
		m.Lore(wc.Dir, "policy", "unset", "--key", wubKey, "--to", inWeb, "--yes").OK(t)
		m.Lore(wc.Dir, "policy", "show", "--id", wubKey).OK(t).Says(t, "32000\tdefault")
		m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", strings.Repeat("every web page is served with a strict content security policy. ", 300), "--yes").OK(t)
		m.Lore(wc.Dir, "sync").OK(t).Never(t, wubWarn)
		mo.Lore(mc.Dir, "sync").OK(t).Never(t, wubWarn)
		m.Lore(wc.Dir, "render", "--report").OK(t).Says(t, wubKey+" 32000").Never(t, "the pinned part alone is")
	})
}

// wubPrinted is the command printed after marker (up to the closing
// backtick), failing when none is.
func wubPrinted(t *testing.T, out, marker string) string {
	t.Helper()
	i := strings.Index(out, marker)
	if i < 0 {
		t.Fatalf("no %q in:\n%s", marker, out)
	}
	rest := out[i+len(marker):]
	end := strings.Index(rest, "`")
	if end < 0 {
		t.Fatalf("unclosed command after %q:\n%s", marker, out)
	}
	return rest[:end]
}

// wubRun runs a printed `lore …` command in dir, where it was printed,
// and wants it to succeed (the owner's GUARD).
func wubRun(t *testing.T, m *journeys.Machine, dir, cmd string) teamharness.Result {
	t.Helper()
	return m.Lore(dir, strings.Fields(strings.TrimPrefix(cmd, "lore "))...).OK(t)
}

// wubFields splits a printed `lore …` command the way a POSIX shell does
// for the single quotes lore prints, without the leading "lore".
func wubFields(t *testing.T, cmd string) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	in, has := false, false
	for _, c := range strings.TrimPrefix(strings.TrimSpace(cmd), "lore ") {
		switch {
		case c == '\'':
			in, has = !in, true
		case c == ' ' && !in:
			if has {
				out = append(out, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteRune(c)
			has = true
		}
	}
	if in {
		t.Fatalf("unbalanced quote in %q", cmd)
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

// wubRuleIn is the id of the first rule kept in place, as listed in dir.
func wubRuleIn(t *testing.T, m *journeys.Machine, dir, place string) string {
	t.Helper()
	r := m.Lore(dir, "rule", "list", "--store", place).OK(t)
	if got := wubRuleID.FindString(r.Out()); got != "" {
		return got
	}
	t.Fatalf("no rule in %s:\n%s", place, r)
	return ""
}
