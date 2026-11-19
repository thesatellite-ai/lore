//go:build stories && !windows

package stories

// WU_opsground_test.go — story WU, devco D-681 (ops store): a folder of
// the ops setup's store is judged by the ops store's own roster. The ops
// store's remote matches the company's own pattern (the owner's shape:
// both live under one GitHub owner), so in the ops store clone the Env
// opens the company's roster; the role check judged "acmeops/infra" by
// it, found it was not the company's folder, and let every member of the
// ops setup lower its must rules.
//
//	D-681/ops-folder: in the ops store clone's folder a member of the ops
//	       roster is refused a demotion, a pin and a policy write, naming
//	       the ops roster; the ops admin runs the printed command

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// Names of the ops ground story (made up).
const (
	wogOpsOrg    = "acmeops"
	wogOpsSetup  = "acmeops-admin"
	wogOpsJoin   = "acmeops"
	wogOpsSlug   = "github.com/acme-co/lore-ops" // under the company's own pattern
	wogToolsSlug = "github.com/acmeops-co/tools"
	wogFolder    = "infra"
	wogFolders   = "~/ops/**"
)

func TestStoryWUOpsFolderGround(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WU"}
	store := s.Remote(wStoreSlug)
	ostore := s.Remote(wogOpsSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	tools := s.Seed(s.Remote(wogToolsSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(pc.Dir, "member", "add", "--email", wubMember.Email, "--name", wubMember.Name, "--handle", "mo", "--role", "member", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wubFolder).OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wubFolder, "--own", "--yes").OK(t)
	m.Lore(wc.Dir, "sync").OK(t)
	tc := m.Clone(tools, "tools")
	m.MoveClone(t, tc, filepath.Join(m.Home, "ops", "tools"))
	journeys.StartTeam(t, m, tc.Dir, wogOpsSetup, wogOpsOrg, ostore.SSHURL(), wAdmin, []string{wogFolders})
	m.Lore(tc.Dir, "member", "add", "--email", wubMember.Email, "--name", wubMember.Name, "--handle", "mo", "--role", "member", "--yes").OK(t)
	m.Lore(tc.Dir, "store", "folder", "add", "team", wogFolder).OK(t)
	inInfra := wogOpsOrg + "/" + wogFolder
	ops := wubRuleID.FindString(m.Lore(tc.Dir, "rule", "add", "--to", inInfra, "--severity", "must", "--body", "infra changes go through a reviewed plan", "--yes").OK(t).Out())
	m.Lore(tc.Dir, "sync").OK(t)

	mo := s.Persona(wubMember.Name, wubMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, mo, wubMember.Login)
	mc := fCodeClone(s, mo, web, "web")
	journeys.Join(t, mo, mc.Dir, wubJoin, store.SSHURL(), wubMember, []string{wFolders})
	mt := mo.Clone(tools, "tools")
	mo.MoveClone(t, mt, filepath.Join(mo.Home, "ops", "tools"))
	journeys.Join(t, mo, mt.Dir, wogOpsJoin, ostore.SSHURL(), wubMember, []string{wogFolders})

	s.Step("D-681/ops-folder-member-refused-admin-ok", func() {
		mo.Lore(mt.Dir, "sync").OK(t)
		infra := filepath.Join(opsCloneDir(t, mo), wogFolder)
		mo.Lore(infra, "rule", "edit", "--id", ops, "--severity", "should").Refused(t, "E_REFUSED", "only an admin of the org", wogOpsOrg+"/org", wAdmin.Name, "nothing was written")
		mo.Lore(infra, "pin", "set", "--id", ops, "--context", "line").Refused(t, "E_REFUSED", "only an admin of the org", "lore pin set --id "+ops+" --context line")
		mo.Lore(mt.Dir, "policy", "set", "--key", wubKey, "--value", "30000", "--to", inInfra, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", wogOpsOrg+"/org")
		mo.Lore(infra, "rule", "show", "--id", ops).OK(t).Says(t, "must")
		// the ops admin may, where the member was refused
		m.Lore(tc.Dir, "sync").OK(t)
		minfra := filepath.Join(opsCloneDir(t, m), wogFolder)
		m.Lore(minfra, "rule", "edit", "--id", ops, "--severity", "should").OK(t)
	})

	s.Step("D-681/role-matrix-member-refused-admin-runs-printed", func() {
		// one cell per (place, write): the member is refused with the
		// ask-an-admin hint naming the place's own admins, and the admin
		// runs the printed line as printed
		m.Lore(tc.Dir, "sync").OK(t)
		mo.Lore(mt.Dir, "sync").OK(t)
		minfra := filepath.Join(opsCloneDir(t, m), wogFolder)
		moinfra := filepath.Join(opsCloneDir(t, mo), wogFolder)
		type place struct {
			to, org                                 string
			adminRows, adminPolicy, memRows, memPol string
		}
		for i, p := range []place{
			{wOrg + "/org", wOrg, wc.Dir, wc.Dir, mc.Dir, mc.Dir},
			{wOrg + "/" + wubFolder, wOrg, wc.Dir, wc.Dir, mc.Dir, mc.Dir},
			{inInfra, wogOpsOrg, minfra, tc.Dir, moinfra, mt.Dir},
		} {
			mark := "matrix cell rule " + strconv.Itoa(i)
			id := wubRuleID.FindString(m.Lore(p.adminPolicy, "rule", "add", "--to", p.to, "--severity", "must", "--body", mark, "--yes").OK(t).Out())
			m.Lore(p.adminPolicy, "sync").OK(t)
			mo.Lore(p.memRows, "sync").OK(t)
			ask := "ask an admin of " + p.org + " (" + wAdmin.Name + ")"
			type cell struct {
				write    string
				dir      string
				adminDir string
				args     []string
			}
			cells := []cell{
				{"pin set", p.memRows, p.adminRows, []string{"pin", "set", "--id", id, "--context", "line"}},
				{"policy set", p.memPol, p.adminPolicy, []string{"policy", "set", "--key", wubKey, "--value", "40000", "--to", p.to, "--yes"}},
				{"rule edit --severity", p.memRows, p.adminRows, []string{"rule", "edit", "--id", id, "--severity", "should"}},
			}
			for _, c := range cells {
				r := mo.Lore(c.dir, c.args...).Refused(t, "E_REFUSED", "only an admin of the org", ask)
				line := wogPrintedLine(t, r.Out())
				m.Lore(c.adminDir, wubFields(t, line)...).OK(t)
			}
			// bulk --where: the admin's demotion above made it a should rule,
			// so a fresh must rule is the bulk cell's
			bulk := "matrix bulk rule " + strconv.Itoa(i)
			m.Lore(p.adminPolicy, "rule", "add", "--to", p.to, "--severity", "must", "--body", bulk, "--yes").OK(t)
			m.Lore(p.adminPolicy, "sync").OK(t)
			mo.Lore(p.memRows, "sync").OK(t)
			mo.Lore(p.memRows, "rule", "delete", "--where", "body~="+bulk, "--yes").Refused(t, "E_REFUSED", "only an admin of the org", ask, "nothing was written")
			m.Lore(p.adminRows, "sync").OK(t)
			m.Lore(p.adminRows, "rule", "delete", "--where", "body~="+bulk, "--yes").OK(t)
		}
	})
}

// opsCloneDir is the ops store clone under the machine's lore home.
func opsCloneDir(t *testing.T, m *journeys.Machine) string {
	t.Helper()
	dir := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-ops")
	got, _ := filepath.Glob(dir)
	if len(got) != 1 {
		all, _ := filepath.Glob(filepath.Join(m.Home, ".lore", "stores", "*", "*"))
		t.Fatalf("no ops store clone at %s; clones: %v", dir, all)
	}
	return got[0]
}

// wogPrintedLine is the command a refusal hands the member to pass on
// ("… to run: lore …"), failing when there is none.
func wogPrintedLine(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, " to run: ")
	if !ok {
		t.Fatalf("no command for the admin in:\n%s", out)
	}
	line, _, _ := strings.Cut(rest, "\n")
	return strings.TrimSpace(line)
}
