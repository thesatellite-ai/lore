//go:build stories && !windows

package stories

// O_orgfile_format_test.go — devco:D-873 (owner org-setup item 14, bug
// hunt H-120) on the owner-shaped machine (story O): lore-org.yaml had no
// format version, so a lore could not tell a file a newer lore wrote from
// one it understands and read either without a word (patterns under a
// renamed key were dropped from the org entry). Now the file carries
// lore_org: the admin's org file is written with it, a file without it
// reads as format 1 and the next write through lore stamps it, and a
// newer format is refused by refresh, config get and set (E_OLD_LORE,
// lore upgrade) and named by doctor, while the org entry keeps what it
// last copied. Every name, email and url is made up.

import (
	"os"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestStoryOOrgFileFormat(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	// a repo linked to the org's store: doctor runs the org checks there
	ada.LoreIn(pc, "org", "link", "--org", oOrg, "--project", "platform", "--yes").OK(t)
	ada.LoreIn(pc, "sync").OK(t)

	ed := ada.CloneTo(team, ada.OrgDir("org-edit"))
	// orgFile reads the org store's lore-org.yaml as pushed.
	orgFile := func() (string, string) {
		ed.Git("fetch", "-q", "origin")
		ed.Git("reset", "-q", "--hard", "origin/main")
		for _, f := range []string{"org/lore-org.yaml", oOrg + "/lore-org.yaml", "lore-org.yaml"} {
			if raw, ok := ed.ReadFile(f); ok {
				return f, raw
			}
		}
		t.Fatalf("the org store has no lore-org.yaml: %s", ed.Git("ls-files"))
		return "", ""
	}
	push := func(f, body, msg string) {
		ed.WriteFile(f, body)
		ed.CommitAll(msg)
		ed.Git("push", "-q", "origin", "HEAD:main")
	}

	s.Step("D-873/admin-setup-writes-the-format", func() {
		f, raw := orgFile()
		oCheck(t, "D-873/new-file-versioned", strings.HasPrefix(raw, "lore_org: 1\n"), "%s written by the admin setup does not start with lore_org: 1:\n%s", f, raw)
	})

	s.Step("D-873/unversioned-reads-as-1-next-write-stamps", func() {
		f, raw := orgFile()
		push(f, strings.Replace(raw, "lore_org: 1\n", "", 1), "an org file written before lore_org existed")
		ada.LoreIn(pc, "org", "refresh", "--org", oOrg).OK(t).Never(t, "E_OLD_LORE", "E_CONFIG_INVALID")
		ada.LoreIn(pc, "org", "config", "get", "--org", oOrg).OK(t).Says(t, "own:")
		ada.LoreIn(pc, "org", "config", "set", "--org", oOrg, "--key", "repos", "--add", "github.com/"+oOwner+"/client-*", "--push", "--yes").OK(t)
		_, raw = orgFile()
		oCheck(t, "D-873/next-write-stamps", strings.HasPrefix(raw, "lore_org: 1\n") && strings.Contains(raw, "client-*"),
			"org config set did not stamp lore_org: 1 as the first key of an unversioned file:\n%s", raw)
	})

	s.Step("D-873/newer-format-refused-and-named", func() {
		f, raw := orgFile()
		// what a later lore with a changed file format could write: a
		// version this one does not know, the patterns under a renamed key
		newer := strings.Replace(strings.Replace(raw, "lore_org: 1\n", "lore_org: 2\n", 1), "\nown:\n", "\nown_repos:\n", 1)
		push(f, newer, "org file format 2")
		before := ed.Head()
		said := func(r teamharness.Result) bool {
			return r.Code != 0 && strings.Contains(r.Out(), "E_OLD_LORE") && strings.Contains(r.Out(), "lore_org: 2") && strings.Contains(r.Out(), "lore upgrade")
		}
		r := ada.LoreIn(pc, "org", "refresh", "--org", oOrg)
		oCheck(t, "D-873/refresh-refuses", said(r), "org refresh read a lore_org: 2 file without E_OLD_LORE and lore upgrade:\n%s", r)
		r = ada.LoreIn(pc, "org", "config", "get", "--org", oOrg)
		oCheck(t, "D-873/config-get-refuses", said(r), "org config get read a lore_org: 2 file:\n%s", r)
		r = ada.LoreIn(pc, "org", "config", "set", "--org", oOrg, "--key", "repos", "--add", "github.com/"+oOwner+"/other-*", "--push", "--yes")
		oCheck(t, "D-873/config-set-refuses", said(r), "org config set wrote a lore_org: 2 file:\n%s", r)
		orgFile()
		oCheck(t, "D-873/nothing-written", ed.Head() == before, "a refused org config set still changed the org store")
		cfg, _ := os.ReadFile(ada.LorePath("config.yaml"))
		oCheck(t, "D-873/entry-kept", strings.Contains(string(cfg), "github.com/"+oOwner+"/client-*"),
			"the org entry lost what it copied after a newer org file:\n%s", cfg)
		d := ada.LoreIn(pc, "doctor")
		oCheck(t, "D-873/doctor-names-it", strings.Contains(d.Out(), "org "+oOrg+": ") && strings.Contains(d.Out(), "lore_org: 2") && strings.Contains(d.Out(), "lore upgrade"),
			"doctor does not name the org file of a newer format:\n%s", d)
	})
}
