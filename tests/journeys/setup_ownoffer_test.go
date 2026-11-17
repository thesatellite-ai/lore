//go:build journeys && !windows

package journeys

// setup_ownoffer_test.go — devco D-526: an admin who runs the guided setup
// inside the org's own repo, with no own: pattern naming it, is offered the
// remote's owner as an own: pattern (github.com/acme-co/*). Without a
// terminal and without --yes nothing is written and the line names the
// exact command; --yes adds it (org config set's path) and whoami then says
// own repo. A member is never offered it.

import (
	"encoding/json"
	"strings"
	"testing"
)

// ooOffer is the own_offer part of setup apply --json.
type ooOffer struct {
	OwnOffer *struct {
		Org     string `json:"org"`
		Pattern string `json:"pattern"`
		Added   bool   `json:"added"`
		Command string `json:"command"`
	} `json:"own_offer"`
	Warnings []string `json:"warnings"`
}

func TestJourneySetupOwnOffer(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	const pattern = "github.com/acme-co/*"
	command := "lore org config set --org " + tlOrg + " --key own --add " + pattern + " --yes"
	apply := func(m *Machine, dir, setup string, extra ...string) ooOffer {
		t.Helper()
		r := m.Lore(dir, append([]string{"setup", "apply", setup, "--step", "this_folder", "--json"}, extra...)...).OK(t)
		var o ooOffer
		if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil {
			t.Fatalf("setup apply --json: %v\n%s", err, r)
		}
		return o
	}
	ownNone := func() {
		t.Helper()
		lee.Lore(tt.repo[tlLee.Email].Dir, "org", "config", "get", "--org", tlOrg).OK(t).Says(t, "own").Never(t, pattern)
	}

	w.Step("D-526/no-terminal-no-yes-writes-nothing-and-names-the-command", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		o := apply(lee, tt.repo[tlLee.Email].Dir, tlLeadSetup)
		if o.OwnOffer == nil || o.OwnOffer.Added || o.OwnOffer.Pattern != pattern || o.OwnOffer.Command != command {
			t.Fatalf("an admin in an unlisted own repo must be offered %s, not added: %+v", pattern, o.OwnOffer)
		}
		if !strings.Contains(strings.Join(o.Warnings, "\n"), "not added (no terminal to ask): "+command) {
			t.Fatalf("the line must name the command: %q", o.Warnings)
		}
		ownNone()
		tt.lore(tlLee, "whoami").OK(t).Says(t, "client repo")
	})

	w.Step("D-526/member-is-never-offered", func() {
		ann := tt.person(tlAnn)
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		if o := apply(ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, "--yes"); o.OwnOffer != nil {
			t.Fatalf("a member got the own: offer: %+v", o.OwnOffer)
		}
		ownNone()
	})

	w.Step("D-526/yes-adds-it-and-whoami-says-own", func() {
		o := apply(lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, "--yes")
		if o.OwnOffer == nil || !o.OwnOffer.Added {
			t.Fatalf("--yes must add %s: %+v %q", pattern, o.OwnOffer, o.Warnings)
		}
		lee.Lore(tt.repo[tlLee.Email].Dir, "org", "config", "get", "--org", tlOrg).OK(t).Says(t, pattern)
		tt.lore(tlLee, "whoami").OK(t).Says(t, "own repo").Never(t, "client repo")
		if o := apply(lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, "--yes"); o.OwnOffer != nil {
			t.Fatalf("a repo an own: pattern names is offered again: %+v", o.OwnOffer)
		}
	})
}
