//go:build journeys && !windows

package journeys

// join_rosterprefill_test.go — `lore setup join` prefills the joining
// person's name and GitHub account from the org's roster (devco D-487).
// The join lines carried global git's user.name as the name and this
// machine's gh login as the GitHub account, though the admin had added
// the person with both; they now carry the roster's values and say
// where each came from. A person the roster has no name for keeps git's.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJourneyJoinPrefillsFromRoster(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	url := tt.store.SSHURL()

	w.Step("day1/lee-adds-ann-with-name-and-github", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, url, tlLee, []string{tlFolders})
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--name", "Ann Roster", "--github", "ann-roster", "--role", "member", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
	})

	w.Step("day1/ann-joins-with-the-roster-values", func() {
		ann := tt.person(tlAnn)
		ann.setGitIdentity(t, "annie-git", tlAnn.Email) // global git's name is not the roster's
		dir := tt.repo[tlAnn.Email].Dir
		var out struct {
			Commands    []string `json:"commands"`
			PrefillNote string   `json:"prefill_note"`
		}
		if err := json.Unmarshal([]byte(ann.Lore(dir, "setup", "join", "--invite", url, "--json").OK(t).Stdout), &out); err != nil {
			t.Fatalf("setup join --json: %v", err)
		}
		lines := strings.Join(out.Commands, "\n")
		for _, want := range []string{`"name":"Ann Roster"`, `"github_account":"ann-roster"`} {
			if !strings.Contains(lines, want) {
				t.Errorf("join lines lack %s:\n%s", want, lines)
			}
		}
		if strings.Contains(lines, "annie-git") {
			t.Errorf("join lines carry global git's name:\n%s", lines)
		}
		if !strings.Contains(out.PrefillNote, "name from the roster of") || !strings.Contains(out.PrefillNote, "GitHub account from the roster of") {
			t.Errorf("prefill_note does not say where the values came from: %q", out.PrefillNote)
		}
		ann.Lore(dir, "setup", "join", "--invite", url).OK(t).Says(t, "Prefilled: name from the roster of")
	})

	w.Step("day1/bob-not-named-on-the-roster-keeps-git", func() {
		tt.lore(tlLee, "member", "add", tlBob.Email, "--role", "member", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		bob := tt.person(tlBob)
		dir := tt.repo[tlBob.Email].Dir
		r := bob.Lore(dir, "setup", "join", "--invite", url).OK(t)
		r.Says(t, "name from git config --global user.name")
	})
}
