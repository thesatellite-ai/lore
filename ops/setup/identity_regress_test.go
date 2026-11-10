package setup

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestRegressM6_IdentityEmailNamesTheCardsGitconfigEmail (polish final3
// m6): with octo-work's GitHub email list unreadable, the GitHub step said
// "could not read octo-work's GitHub emails" while octo-work's card showed
// work@example.org from the gitconfig. github.identity_email now reads the
// same gitconfig link the card shows and names it, still without claiming
// GitHub confirmed it (a skip, never OK, never a warning).
func TestRegressM6_IdentityEmailNamesTheCardsGitconfigEmail(t *testing.T) {
	logins := []string{"octo-personal", "octo-work"}
	incs := []GitIncludeFact{
		{Condition: "gitdir:~/work1/", Folder: "~/work1/", Path: "~/.gitconfig-work", Name: "Pat Example", Email: "work@example.org"},
		{Condition: "gitdir:~/me/", Folder: "~/me/", Path: "~/.gitconfig-me", Name: "Pat Example", Email: "person@example.com"},
	}
	got := GitconfigEmailsOf("octo-work", logins, incs)
	if !slices.Equal(got, []string{"work@example.org"}) {
		t.Fatalf("GitconfigEmailsOf(octo-work) = %v, want [work@example.org] (the card's gitconfig email)", got)
	}
	if other := GitconfigEmailsOf("octo-personal", logins, incs); slices.Contains(other, "work@example.org") {
		t.Fatalf("octo-personal got octo-work's gitconfig email: %v", other)
	}
	st, detail := onboard.IdentityEmailMatch(onboard.IdentityEmailIn{Emails: []string{"work@example.org"}, User: "octo-work", Why: "no scope", Gitconfig: got})
	if st != onboard.StatusSkip {
		t.Fatalf("status %s, want skip (not confirmed by GitHub): %q", st, detail)
	}
	for _, want := range []string{"work@example.org is the email your gitconfig uses for octo-work", "could not be read (no scope)"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q lacks %q", detail, want)
		}
	}
	// An identity email the gitconfig does not tie to the account keeps the plain "could not read".
	_, detail = onboard.IdentityEmailMatch(onboard.IdentityEmailIn{Emails: []string{"person@example.com"}, User: "octo-work", Why: "no scope", Gitconfig: got})
	if !strings.HasPrefix(detail, "could not read octo-work's GitHub emails") {
		t.Errorf("unlinked email: detail %q, want the plain could-not-read line", detail)
	}
}
