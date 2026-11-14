package wire

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestEntryAnswersCarryTheSavedIdentity: a finished setup has no progress
// rows, so saving its team or join step again (the page's edit) found no
// identity answers and was refused with "the admin needs an identity
// email" while the step's checks passed. The roster write now starts from
// the saved entry's identity and GitHub account.
func TestEntryAnswersCarryTheSavedIdentity(t *testing.T) {
	e := onboard.SetupEntry{
		Identity: onboard.SetupIdentity{Key: "email:mira@agency.example", Name: "Mira Doe", Handle: "mira", Emails: []string{"mira@old.example"}},
		GitHub:   onboard.SetupGitHub{User: "mira-gh"},
	}
	got := entryAnswers(e)
	for id, want := range map[string]string{onboard.QIDEmail: "mira@agency.example", onboard.QIDName: "Mira Doe", onboard.QIDHandle: "mira", onboard.QIDGitHubAccount: "mira-gh"} {
		if got[id].Text != want {
			t.Errorf("answer %s = %q, want %q", id, got[id].Text, want)
		}
	}
	if !slices.Equal(got[onboard.QIDEmails].List, []string{"mira@old.example"}) {
		t.Errorf("answer %s = %v", onboard.QIDEmails, got[onboard.QIDEmails].List)
	}
	if n := len(entryAnswers(onboard.SetupEntry{})); n != 0 {
		t.Errorf("an entry not written yet gives %d answers, want none", n)
	}
}
