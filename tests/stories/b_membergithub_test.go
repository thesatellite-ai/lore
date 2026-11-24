//go:build stories && !windows

package stories

// b_membergithub_test.go — story B (devco:D-959, issue #161): a member
// moves from a personal GitHub account to a work one. member edit had no
// --github, --meta github= was refused with a hint that renames the
// person, member add with the new login said "nothing to add" with exit
// 0, and a github contact made primary left member show on the old login.
// Now the member's login IS their primary github contact: member edit
// --github changes it (the old login stays theirs for past commits), add
// refuses naming the edit, contact add --primary moves member show too.
// Every printed command runs as printed.

import (
	"strings"
	"testing"
)

func TestStoryBMemberGitHubLoginChanges(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	const (
		store    = "acme_team"
		personal = "nia-personal"
		work     = "nia-work"
	)
	// hint is the "hint: " line of r, the command a refusal prints.
	hint := func(t *testing.T, out string) []string {
		t.Helper()
		for _, l := range strings.Split(out, "\n") {
			if h, ok := strings.CutPrefix(strings.TrimSpace(l), "hint: "); ok {
				return strings.Fields(strings.TrimPrefix(h, "lore "))
			}
		}
		t.Fatalf("no hint in:\n%s", out)
		return nil
	}

	s.Step("D-959/add-with-another-login-is-refused-naming-the-edit", func() {
		b.person(t, bCTO)
		b.lore(bCTO, "store", "init", "--name", store, "--url", b.store.SSHURL(), "--yes").OK(t)
		b.lore(bCTO, "org", "create", "--org", bOrg, "--store", store, "--own", "github.com/acme/*",
			"--key", "human:"+bCTO.Email, "--name", bCTO.Name, "--handle", "cora", "--yes").OK(t)
		b.lore(bCTO, "member", "add", "--email", bHire.Email, "--name", bHire.Name, "--github", personal, "--role", "member", "--yes").OK(t).
			Says(t, "handle @"+personal)
		for _, dry := range []string{"--dry-run", "--yes"} {
			r := b.lore(bCTO, "member", "add", "--email", bHire.Email, "--github", work, dry).Fails(t, 20, "E_INVALID_INPUT").
				Says(t, "GitHub login "+personal+", not "+work, "lore member edit --who "+bHire.Email+" --github "+work+" --yes").
				Never(t, "nothing to add")
			s.NamedFlags(t, r)
		}
		r := b.lore(bCTO, "member", "edit", "--who", bHire.Email, "--meta", "github="+work, "--yes").Fails(t, 20, "E_INVALID_INPUT").
			Says(t, "github is a roster field").Never(t, "--name item_github")
		s.NamedFlags(t, r)
		b.lore(bCTO, "member", "show", "--who", bHire.Email).OK(t).Says(t, "github:   "+personal)
	})

	s.Step("D-959/member-edit-github-changes-it-and-keeps-the-old-login", func() {
		// the add's refusal hint, run exactly as printed
		r := b.lore(bCTO, "member", "add", "--email", bHire.Email, "--github", work, "--dry-run").Fails(t, 20)
		r = b.lore(bCTO, hint(t, r.Out())...).OK(t).
			Says(t, "earlier GitHub login "+personal+" stays theirs", "the handle @"+personal+" is unchanged")
		s.NamedFlags(t, r)
		b.lore(bCTO, "member", "show", "--who", bHire.Email).OK(t).Says(t, "github:   "+work, "handle:   @"+personal)
		b.lore(bCTO, "member", "list", "--json").OK(t).Says(t, `"github": "`+work+`"`)
		b.lore(bCTO, "actor", "contact", "list", "--actor", bHire.Email).OK(t).
			Says(t, work+" ", personal+" ").Never(t, "E_")
		// the same login again is "already a member", exit 0
		b.lore(bCTO, "member", "add", "--email", bHire.Email, "--github", work, "--dry-run").OK(t).Says(t, "already a member")
	})

	s.Step("D-959/contact-add-primary-moves-member-show-both-ways", func() {
		b.lore(bCTO, "actor", "contact", "add", "--actor", bHire.Email, "--type", "github", "--value", personal,
			"--identity", "--primary", "--yes").OK(t)
		b.lore(bCTO, "member", "show", "--who", bHire.Email).OK(t).Says(t, "github:   "+personal).Never(t, "github:   "+work)
		b.lore(bCTO, "member", "edit", "--who", bHire.Email, "--github", work, "--yes").OK(t)
		b.lore(bCTO, "member", "show", "--who", bHire.Email).OK(t).Says(t, "github:   "+work)
	})
}
