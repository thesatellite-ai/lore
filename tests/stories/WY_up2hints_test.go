//go:build stories && !windows

package stories

// WY_up2hints_test.go — story WY, devco D-587..D-592 (owner use pass 2,
// hints that did not work or looped): an org admin stands in the home
// folder, in an own repo of the org not linked to a folder, in a client
// repo of the org not linked to a folder, and in a folder of the store's
// clone, and follows every next step lore prints, where it printed it.
//
//	day1: StartTeam in platform, own: github.com/acme-co/*, repos:
//	      github.com/client-co/*, web (own) and shop (client) cloned unlinked
//	D-587: whoami in shop names the command that installs the guard there
//	       (org link), never `lore fix`, which refuses in an unlinked repo
//	D-588: a resource in acme/org whose project field is sync names web:
//	       reads in web suggest `--project sync`, never `--project org`
//	D-643: the same with project a select field (its val_ id in v, the
//	       name in v_name): `--project sync`, never org or the val_ id
//	D-589: a mistyped id in the home folder (edit, show, trash restore):
//	       "in no store of any setup", where lore looked, no --setup/--store
//	       hint loop, never a flag the command was not given
//	D-590: show of a row deleted in its folder: "deleted from <org>/<folder>
//	       on <date>" with the trash list command, never "it is in" and
//	       never a branch hint outside a git checkout; a purged row (its
//	       folder's _purged.json lists it) answers E_PURGED, never the
//	       trash list or restore (it is gone for good)
//	D-591: a read refused in shop names --store acme/<folder> for reads
//	D-592: every printed next step above runs where printed and neither
//	       refuses nor loops back to the first hint
//	D-653: a write with no --to in web names --to acme/sync and the same
//	       org link the read hint names, never <org> or <folder>

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// wyShopSlug is a client repo of acme (the org config's repos: pattern).
const wyShopSlug = "github.com/client-co/shop"

// wyAPISlug is an own repo of acme named by a resource whose project field
// is a select (devco D-643); wySelKind is that resource's kind.
const (
	wyAPISlug = "github.com/acme-co/api"
	wySelKind = "svc_repo"
)

// wyRulID is a rule id in a command's output.
var wyRulID = regexp.MustCompile(`rul_[0-9a-f]+`)

func TestStoryWYUp2Hints(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WY"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(wyShopSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/client-co/*", "--yes").OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	sc := m.Clone(shop, "shop")
	m.MoveClone(t, sc, filepath.Join(m.Home, "code", "shop"))
	inSync := wOrg + "/sync"
	add := m.Lore(m.Home, "memory", "add", "--body", "the deploy key rotates monthly", "--to", inSync, "--setup", wSetup, "--yes").OK(t)
	mem := wuMemID.FindString(add.Out())
	if mem == "" {
		t.Fatalf("memory add printed no id: %s", add.Out())
	}

	s.Step("D-591/client-read-refusal-names-store", func() {
		// (search there reads the personal store; trash list has none to read)
		r := m.Lore(sc.Dir, "trash", "list").Refused(t, "--store "+wOrg+"/<folder>")
		wyFollow(t, m, sc.Dir, r, "devco:D-592 search@client")
		m.Lore(sc.Dir, "search", "--query", "deploy", "--store", inSync).OK(t).Says(t, mem)
	})

	s.Step("D-587/unlinked-client-whoami-names-org-link", func() {
		r := m.Lore(sc.Dir, "whoami").OK(t).Says(t, "guards OFF", "lore org link --org "+wOrg+" --project <folder>").Never(t, "lore fix here")
		wyFollow(t, m, sc.Dir, r, "devco:D-592 whoami@client")
		// the named command, with a folder, installs the guard
		m.Lore(sc.Dir, "org", "link", "--org", wOrg, "--project", "sync", "--yes").OK(t)
		m.Lore(sc.Dir, "whoami").OK(t).Says(t, "guards on").Never(t, "guards OFF")
	})

	s.Step("D-588/resource-project-field-names-the-folder", func() {
		m.Lore(m.Home, "resource", "kind", "add", "--kind", wuRepoKind, "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		for _, f := range []string{"url", "project"} {
			m.Lore(m.Home, "resource", "field", "add", "--kind", wuRepoKind, "--field", f, "--type", "text", "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		}
		m.Lore(m.Home, "resource", "add", "--kind", wuRepoKind, "--name", wuRepoRes, "--set", "url="+web.SSHURL(), "--set", "project=sync", "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		r := m.Lore(wc.Dir, "memory", "list").OK(t).Says(t, "lore org link --org "+wOrg+" --project sync --own", mem).Never(t, "--project org")
		m.Lore(wc.Dir, "search", "--query", "deploy").OK(t).Says(t, "--project sync --own", mem).Never(t, "--project org")
		wyFollow(t, m, wc.Dir, r, "devco:D-592 memory list@own-unlinked")
	})

	s.Step("D-643/select-project-field-names-the-folder", func() {
		// the owner's resource kind keeps project as a closed value (a
		// select): the row stores its val_ id in v and the name in v_name
		api := s.Seed(s.Remote(wyAPISlug), func(dir string) { PlainRepo(t, dir) })
		ac := m.Clone(api, "api")
		m.MoveClone(t, ac, filepath.Join(m.Home, "code", "api"))
		m.Lore(m.Home, "resource", "kind", "add", "--kind", wySelKind, "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		m.Lore(m.Home, "resource", "field", "add", "--kind", wySelKind, "--field", "url", "--type", "text", "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		m.Lore(m.Home, "resource", "field", "add", "--kind", wySelKind, "--field", "project", "--type", "text", "--values", "platform,sync", "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		m.Lore(m.Home, "resource", "add", "--kind", wySelKind, "--name", "api_repo", "--set", "url="+api.SSHURL(), "--set", "project=sync", "--to", wOrg+"/org", "--setup", wSetup, "--yes").OK(t)
		root := m.LorePath("stores", "acme-co", "lore-team")
		rows, _ := filepath.Glob(filepath.Join(wFindDir(t, root, "org"), "resources", "*.json"))
		sel := false
		for _, p := range rows {
			if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), `"v_name"`) && strings.Contains(string(b), "api_repo") {
				sel = true
			}
		}
		if !sel {
			t.Fatalf("the select-valued resource row has no v_name (the shape D-643 needs): %v", rows)
		}
		hint := "lore org link --org " + wOrg + " --project sync --own"
		r := m.Lore(ac.Dir, "memory", "list").OK(t).Says(t, hint, mem).Never(t, "--project org", "--project val_")
		m.Lore(ac.Dir, "whoami").OK(t).Never(t, "--project org", "--project val_")
		wyFollow(t, m, ac.Dir, r, "devco:D-643 memory list@own-unlinked-select")
		// the printed link, run where printed, links the repo to sync
		m.Lore(ac.Dir, "org", "link", "--org", wOrg, "--project", "sync", "--own", "--yes").OK(t)
		m.Lore(ac.Dir, "whoami").OK(t).Says(t, wOrg+"/sync").Never(t, "link it:")
	})

	// devco:D-653: a write with no --to in that unlinked own repo named
	// placeholders ("--to <org>/<folder>", "org link --org <org> --project
	// <folder>") while the read hint there names the exact link: the write
	// hint names the same org link and the exact --to, and both work where
	// printed (the link is checked by D-587's step, so only --to runs here).
	s.Step("D-653/unlinked-own-write-hint-names-the-folder", func() {
		r := m.Lore(wc.Dir, "memory", "add", "--body", "a note written with no target", "--yes").
			Refused(t, "--to "+inSync, "lore org link --org "+wOrg+" --project sync --own").
			Never(t, "<org>", "<folder>", "--project org", "wire:")
		wyFollow(t, m, wc.Dir, r, "devco:D-653 memory add@own-unlinked")
		m.Lore(wc.Dir, "memory", "add", "--body", "a note written with no target", "--to", inSync, "--yes").OK(t)
	})

	s.Step("D-589/mistyped-id-in-home", func() {
		bad := wyMistype(mem)
		for _, args := range [][]string{
			{"memory", "edit", "--id", bad, "--body", "x"},
			{"memory", "show", "--id", bad},
			{"trash", "restore", "--id", bad},
			{"memory", "edit", "--id", bad, "--body", "x", "--setup", wSetup},
			{"trash", "restore", "--id", bad, "--setup", wSetup},
		} {
			r := m.Lore(m.Home, args...).Refused(t, bad, "in no store of any setup", inSync).
				Never(t, "--store does not apply", "works on the folder it runs in", "add --setup", "sync peek", "landed on")
			wyFollow(t, m, m.Home, r, "devco:D-592 "+strings.Join(args[:2], " ")+"@home")
		}
		// a real id still works from the home folder
		m.Lore(m.Home, "memory", "show", "--id", mem).OK(t).Says(t, "the deploy key rotates monthly")
	})

	s.Step("D-590/deleted-row-show", func() {
		out := m.Lore(m.Home, "rule", "add", "--body", "never deploy on fridays", "--to", inSync, "--setup", wSetup, "--yes").OK(t).Out()
		rul := wyRulID.FindString(out)
		if rul == "" {
			t.Fatalf("rule add printed no id: %s", out)
		}
		m.Lore(m.Home, "rule", "delete", "--id", rul, "--yes").OK(t)
		m.LoreIn(pc, "sync").OK(t)
		root := m.LorePath("stores", "acme-co", "lore-team")
		places := []string{m.Home, wFindDir(t, root, "org"), pc.Dir}
		// in the trash: E_IN_TRASH names trash show and restore, which work
		for _, dir := range places {
			r := m.Lore(dir, "rule", "show", "--id", rul).Refused(t, "is in the trash").Never(t, "it is in", "sync peek", "landed on")
			wyFollow(t, m, dir, r, "devco:D-592 rule show trashed@"+filepath.Base(dir))
		}
		// purged: a tombstone in acme/sync listed in its _purged.json, the
		// shape of the owner's row (LEFT: purge writes the tombstone and
		// the list, then fails E_INTERNAL "this machine's safety copies of
		// them could not be removed"; the tombstone is what this step reads)
		t.Logf("trash purge:\n%s", m.Lore(pc.Dir, "trash", "purge", "--id", rul, "--confirm"))
		m.LoreIn(pc, "sync").OK(t)
		trash := "lore trash list --store " + inSync
		for _, dir := range places {
			r := m.Lore(dir, "rule", "show", "--id", rul).Refused(t, "E_PURGED", rul, "purged rows never come back").
				Never(t, "it is in", "sync peek", "landed on", trash, "trash restore", "was deleted from")
			wyFollow(t, m, dir, r, "devco:D-592 rule show purged@"+filepath.Base(dir))
		}
		// a tombstone the purge list does not name (a move, or an older
		// lore's hard delete): where and when it was deleted, and the
		// folder's trash list
		syncDir := wFindDir(t, root, "sync")
		if err := os.WriteFile(filepath.Join(syncDir, "_purged.json"), []byte(`{"_v":1,"ids":[]}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		deleted := regexp.MustCompile(regexp.QuoteMeta(rul+" was deleted from "+inSync+" on ") + `\d{4}-\d{2}-\d{2}`)
		for _, dir := range []string{m.Home, wFindDir(t, root, "org")} {
			r := m.Lore(dir, "rule", "show", "--id", rul).Refused(t, trash).Never(t, "it is in", "sync peek", "landed on", "E_PURGED")
			if !deleted.MatchString(r.Out()) {
				t.Errorf("devco:D-590 rule show of a deleted row in %s does not say where and when it was deleted:\n%s", dir, r)
			}
			wyFollow(t, m, dir, r, "devco:D-592 rule show deleted@"+filepath.Base(dir))
		}
	})
}

// wyMistype is id with its last hex digit changed: well formed, held by
// no store.
func wyMistype(id string) string {
	last := id[len(id)-1]
	next := byte('0')
	if last == '0' {
		next = '1'
	}
	return id[:len(id)-1] + string(next)
}

// wyFollow runs every concrete `lore …` command r printed (hint: lore …,
// `lore …` in backticks, restore:, link it:) in dir, where it was
// printed: it must not be refused as a wrong hint (wuBadRefusal), refuse
// with E_NOT_A_STORE, or answer with the first hint again (a loop).
func wyFollow(t *testing.T, m *journeys.Machine, dir string, r teamharness.Result, id string) {
	t.Helper()
	first := wyHint(r.Out())
	for _, c := range wyPrinted(r.Out()) {
		if c[0] == "org" && len(c) > 1 && c[1] == "link" || c[0] == "sync" {
			continue // changes the repo or reaches a remote: checked by its own step
		}
		pr := m.Lore(dir, c...)
		bad := wuBadRefusal(pr.Code, pr.Out())
		switch {
		case bad != "":
		case pr.Code != 0 && strings.Contains(pr.Out(), "E_NOT_A_STORE"):
			bad = "E_NOT_A_STORE"
		case pr.Code != 0 && first != "" && wyHint(pr.Out()) == first:
			bad = "loops back to the first hint"
		}
		if bad != "" {
			t.Errorf("%s: printed `lore %s`, which fails where printed (%s):\n%s", id, strings.Join(c, " "), bad, pr)
		}
	}
}

// wyHint is the hint line of out, "" when none.
func wyHint(out string) string {
	i := strings.Index(out, "hint:")
	if i < 0 {
		return ""
	}
	h, _, _ := strings.Cut(out[i:], "\n")
	return h
}

// wyPrinted is every concrete command out names: the guard markers plus
// every `lore …` in backticks and after "link it:".
func wyPrinted(out string) [][]string {
	var cmds [][]string
	seen := map[string]bool{}
	for _, mk := range append(append([]string{}, wuMarkers...), "`", "link it: ") {
		for _, c := range wuPrinted(out, mk) {
			k := strings.Join(c, "\x00")
			if len(c) == 0 || seen[k] {
				continue
			}
			seen[k] = true
			cmds = append(cmds, c)
		}
	}
	return cmds
}
