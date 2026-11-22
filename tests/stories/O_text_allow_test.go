//go:build stories && !windows

package stories

// O_text_allow_test.go — devco:D-906 (#129) on the owner-shaped machine
// (story O): an internal decision records approved banner wording word for
// word, and the client repo ships that wording. The text rule refused the
// commit and nothing could allow it. Now the refusal prints the exact
// `lore leaks allow` line; the allow is kept with the note in the org
// store (the client repo stays untouched), names its reason and author in
// `lore check leaks` (the commit hook's output) and in `lore doctor`, the
// push passes, and it covers that passage in that file only.

import (
	"regexp"
	"testing"
)

func TestStoryOTextAllow(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	app := s.Seed(s.Remote("github.com/acme-client/checkout"), func(dir string) { PlainRepo(t, dir) })
	const (
		folder  = "client_checkout"
		wording = "Your order has been received and a support agent will contact you within two business days."
		reason  = "approved public copy"
	)
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("checkout"))
	ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
	ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
	ada.Lore(ac.Dir, "decision", "add", "--title", "Banner wording approved",
		"--body", "Approved wording for the banner: \""+wording+"\" Do not change it without sign-off.", "--yes").OK(t)

	var note, hash string
	s.Step("D-906/the-refusal-prints-the-allow-line", func() {
		ac.WriteFile("copy.txt", wording+"\n")
		ac.Git("add", "copy.txt")
		r := ac.TryGit("commit", "-q", "-m", "Add the submit copy")
		if r.Code == 0 {
			t.Fatalf("a commit quoting the internal note passed without an allow: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "copy.txt:1", "lore leaks allow --note dec_", "--file copy.txt --match-hash ")
		m := regexp.MustCompile(`lore leaks allow --note (dec_[0-9a-f]{32}) --file copy\.txt --match-hash ([0-9]+) --reason`).FindStringSubmatch(r.Out())
		if m == nil {
			t.Fatalf("the refusal prints no allow line: %s", r)
		}
		note, hash = m[1], m[2]
	})

	s.Step("D-906/the-allow-is-kept-with-the-note-and-the-commit-names-it", func() {
		before := ac.Git("status", "--porcelain", "--untracked-files=all")
		ada.Lore(ac.Dir, "leaks", "allow", "--note", note, "--file", "copy.txt", "--match-hash", hash, "--reason", reason, "--yes").OK(t).
			Says(t, "may ship in copy.txt", "saved in "+oOrg+"/"+folder)
		if after := ac.Git("status", "--porcelain", "--untracked-files=all"); after != before {
			t.Fatalf("the allow wrote into the client repo:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		r := ac.TryGit("commit", "-q", "-m", "Add the submit copy")
		if r.Code != 0 {
			t.Fatalf("the allowed passage is still refused: %s", r)
		}
		r.Says(t, "copy.txt:1 matches internal note "+note, "allowed by sal_", reason)
		if p := ac.TryGit("push", "-q", "origin", "HEAD"); p.Code != 0 {
			t.Fatalf("the push of the allowed passage is refused: %s", p)
		}
		ada.Lore(ac.Dir, "doctor").Says(t, "text allow sal_", "a passage of internal note "+note+" may ship in copy.txt ("+reason, "lore secrets allow rm --id sal_")
	})

	s.Step("D-906/the-allow-covers-that-file-only", func() {
		ac.WriteFile("other.txt", wording+"\n")
		ac.Git("add", "other.txt")
		r := ac.TryGit("commit", "-q", "-m", "Copy it elsewhere")
		if r.Code == 0 {
			t.Fatalf("an allow for copy.txt let the passage through in other.txt: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "other.txt:1")
	})
}
