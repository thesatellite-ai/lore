//go:build stories && !windows

package stories

// O_identity_gaps_test.go — devco:D-714 (second part) on the owner-shaped
// machine (story O): the two author gaps one rule for every org-store
// commit left open.
//   - lore doctor store_identity compared only the clone's user.email with
//     the org identity: a git handle as user.name beside the org email was
//     not flagged, so hand commits there differed from lore's own;
//   - an org with no identity signed its org-file commits (org link,
//     org config, …) "lore <lore@localhost>" while every row commit in the
//     same store carried the clone's git identity (FLAG4).

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// oDoctorFix is the exact fix line doctor prints for store_identity.
var oDoctorFix = regexp.MustCompile(`git -C \S+ config user\.email \S+ && git -C \S+ config user\.name ('[^']*'|\S+)`)

func TestStoryOIdentityGaps(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	notes := s.Seed(s.Remote("github.com/"+oOwner+"/notes-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	nc := ada.CloneTo(notes, ada.OrgDir("notes-app"))
	clone := oStoreClone(ada, oOwner, "lore-team")
	const handle = "ada-git-handle" // a git user.name beside the right org email
	ada.Lore(pc.Dir, "init", "--non-interactive", "--name=platform").OK(t)
	ada.Lore(pc.Dir, "memory", "add", "--body", "deploys go out on tuesdays", "--to", oOrg).OK(t)

	s.Step("D-714/doctor-flags-a-git-handle-beside-the-org-email", func() {
		s.Git(clone, "config", "user.name", handle)
		s.Git(clone, "config", "user.email", oAdminOrg.Email)
		r := ada.Lore(pc.Dir, "doctor")
		out := r.Out()
		oCheck(t, "D-714/doctor-names-both", strings.Contains(out, "store_identity") && strings.Contains(out, handle+" <"+oAdminOrg.Email+">") && strings.Contains(out, oAdminOrg.Name+" <"+oAdminOrg.Email+">"),
			"a store clone with user.name %s beside the org email: want a store_identity finding naming %s <%s> and the org identity %s <%s>, got:\n%s", handle, handle, oAdminOrg.Email, oAdminOrg.Name, oAdminOrg.Email, r)
		fix := oDoctorFix.FindString(out)
		oCheck(t, "D-714/doctor-prints-the-fix", fix != "", "doctor printed no git config fix line for both keys:\n%s", r)
		if fix == "" {
			return
		}
		// the printed fix runs as printed, for the person it is printed to
		cmd := exec.Command("sh", "-c", fix)
		cmd.Env = os.Environ()
		if h := ada.Env("HOME"); h != "" {
			cmd.Env = append(cmd.Env, "HOME="+h)
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("doctor's fix %q failed: %v\n%s", fix, err, b)
		}
		oCheck(t, "D-714/fix-sets-both", strings.TrimSpace(s.Git(clone, "config", "user.name")) == oAdminOrg.Name && strings.TrimSpace(s.Git(clone, "config", "user.email")) == oAdminOrg.Email,
			"after the printed fix the clone's git user is %q <%s>", s.Git(clone, "config", "user.name"), s.Git(clone, "config", "user.email"))
		ada.Lore(pc.Dir, "doctor").Never(t, "store_identity")
		// lore doctor --fix leaves the name right too (lore's own clone
		// identity repair may already have reset it, as the run says)
		s.Git(clone, "config", "user.name", handle)
		ada.Lore(pc.Dir, "doctor", "--fix")
		oCheck(t, "D-714/doctor-fix-sets-the-name", strings.TrimSpace(s.Git(clone, "config", "user.name")) == oAdminOrg.Name,
			"lore doctor --fix left user.name %q", s.Git(clone, "config", "user.name"))
		ada.Lore(pc.Dir, "doctor").Never(t, "store_identity")
	})

	s.Step("D-714/an-org-without-identity-signs-org-files-as-the-clone-git-user", func() {
		ada.Lore(nc.Dir, "init", "--non-interactive", "--name=notes-app").OK(t)
		cfgPath := ada.LorePath("config.yaml")
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		stripped, n := oDropOrgIdentity(string(raw), oOrg)
		if n == 0 {
			t.Fatalf("fixture: no identity under org %s in config.yaml:\n%s", oOrg, raw)
		}
		if err := os.WriteFile(cfgPath, []byte(stripped), 0o600); err != nil {
			t.Fatal(err)
		}
		const gitName, gitEmail = "Ada Lovelace", "ada@clone.example"
		s.Git(clone, "config", "user.name", gitName)
		s.Git(clone, "config", "user.email", gitEmail)
		base := strings.TrimSpace(s.Git(clone, "rev-parse", "HEAD"))
		r := ada.Lore(nc.Dir, "org", "link", "--org", oOrg, "--project", "client_notes", "--yes")
		log := strings.TrimSpace(s.Git(clone, "log", "--format=%an <%ae>|%cn <%ce>|%s", "--branches", "^"+base))
		if log == "" {
			t.Fatalf("fixture: org link made no store commit:\n%s\n%s", r, s.Git(clone, "log", "--oneline", "-3", "--branches"))
		}
		for line := range strings.SplitSeq(log, "\n") {
			f := strings.SplitN(line, "|", 3)
			want := gitName + " <" + gitEmail + ">"
			oCheck(t, "D-714/no-identity-signs-as-git", f[0] == want && f[1] == want,
				"org without an identity: the store commit %q is by %s / %s, want the clone's git identity %s (as every row commit there), never lore@localhost", f[2], f[0], f[1], want)
		}
	})
}

// oDropOrgIdentity removes the identity block of org name from a
// config.yaml text, as a machine whose org entry never got one; n is the
// number of lines removed.
func oDropOrgIdentity(cfg, name string) (string, int) {
	lines := strings.Split(cfg, "\n")
	var out []string
	inOrg, n := false, 0
	idIndent := -1
	for _, l := range lines {
		trim := strings.TrimLeft(l, " ")
		indent := len(l) - len(trim)
		if strings.HasPrefix(trim, "- name: ") {
			inOrg = strings.TrimSpace(strings.TrimPrefix(trim, "- name: ")) == name
			idIndent = -1
		}
		if idIndent >= 0 {
			if trim != "" && indent > idIndent {
				n++
				continue
			}
			idIndent = -1
		}
		if inOrg && strings.HasPrefix(trim, "identity:") {
			idIndent = indent
			n++
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n"), n
}
