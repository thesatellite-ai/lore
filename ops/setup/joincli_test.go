package setup

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestJoinSetupName (B-10): a join by store url names the setup after the
// url's owner, so the page and its "This folder" line offer that member
// setup instead of "solo setup personal".
func TestJoinSetupName(t *testing.T) {
	for _, c := range []struct{ org, url, want string }{
		{"", "git@git.acme.example:acme/team-store.git", "acme"},
		{"", "https://git.acme.example/Acme/team-store.git", "acme"},
		{"agency", "git@git.acme.example:acme/team-store.git", "agency"},
		{"", "/srv/git/team-store.git", "team-store"},
		{"", "", ""},
	} {
		if got := joinSetupName(c.org, c.url); got != c.want {
			t.Errorf("joinSetupName(%q, %q) = %q, want %q", c.org, c.url, got, c.want)
		}
	}
}

// TestJoinCommands (B-10): the lines a join with no terminal prints make
// a member setup step by step: kind member with the name, identity with
// the git email, github skipped (personal_store needs it), the join
// answer; optional steps with nothing to say (tools) are left out.
func TestJoinCommands(t *testing.T) {
	plan := PlanOut{Steps: []PlanStep{
		{ID: onboard.StepKind, Kinds: []onboard.Kind{onboard.KindMember}, Values: onboard.Answers{"kind": {Text: "member"}}},
		{ID: onboard.StepIdentity, Kinds: []onboard.Kind{onboard.KindMember}, Needs: []onboard.StepID{onboard.StepKind}, Values: onboard.Answers{"name": {Text: "Sam O'Neil"}}},
		{ID: onboard.StepGitHub, Kinds: []onboard.Kind{onboard.KindMember}, Optional: true},
		{ID: onboard.StepPersonalStore, Kinds: []onboard.Kind{onboard.KindMember}, Needs: []onboard.StepID{onboard.StepGitHub}, Values: onboard.Answers{"personal_store": {Text: "local"}}},
		{ID: onboard.StepJoin, Kinds: []onboard.Kind{onboard.KindMember}},
		{ID: onboard.StepTeam, Kinds: []onboard.Kind{onboard.KindAdmin}},
		{ID: onboard.StepTools, Kinds: []onboard.Kind{onboard.KindMember}, Optional: true},
		{ID: onboard.StepSummary, Kinds: []onboard.Kind{onboard.KindMember}},
	}}
	got := strings.Join(joinCommands("acme", "git@git.acme.example:acme/team-store.git", joinIdentity{Email: "sam@acme.example"}, plan), "\n")
	want := strings.Join([]string{
		`lore setup apply --setup acme --step kind --answers '{"kind":"member","name":"acme"}'`,
		`lore setup apply --setup acme --step identity --answers '{"email":"sam@acme.example","name":"Sam O'\''Neil"}'`,
		`lore setup apply --setup acme --step github --answers '{}'`,
		`lore setup apply --setup acme --step personal_store --answers '{"personal_store":"local"}'`,
		`lore setup apply --setup acme --step join --answers '{"invite":"git@git.acme.example:acme/team-store.git"}'`,
	}, "\n")
	if got != want {
		t.Fatalf("join lines:\n%s\nwant:\n%s", got, want)
	}
}

// TestHereForJoin (B-10): the page `lore setup join` opened offers the
// member setup being joined for a folder no setup covers, not the solo
// setup ResolveHere guessed; other requests keep the guess.
func TestHereForJoin(t *testing.T) {
	h := onboard.Here{State: onboard.HereNoMatch, Proposal: onboard.HereProposal{Kind: onboard.KindSolo, Name: "personal", Folders: []string{"~/code/x/**"}}}
	if got := HereForJoin(context.Background(), h); got.Proposal.Kind != onboard.KindSolo {
		t.Fatalf("no join: proposal %+v changed", got.Proposal)
	}
	got := HereForJoin(WithJoin(context.Background(), JoinContext{Setup: "acme"}), h)
	if got.Proposal.Kind != onboard.KindMember || got.Proposal.Name != "acme" || got.Proposal.Folders[0] != "~/code/x/**" {
		t.Fatalf("join: proposal %+v, want member acme for ~/code/x/**", got.Proposal)
	}
	if line := onboard.HereLine(got); !strings.Contains(line, "offers a member setup acme") {
		t.Fatalf("line %q", line)
	}
}

// TestJoinCommandsNeverOfferAnotherOrgsEmail (devco N-4): a git email that
// is another org's identity on this machine is replaced in the identity
// line, also when the plan prefilled it, so the line asks for this org's
// address instead of carrying the first org's.
func TestJoinCommandsNeverOfferAnotherOrgsEmail(t *testing.T) {
	plan := PlanOut{Steps: []PlanStep{
		{ID: onboard.StepIdentity, Kinds: []onboard.Kind{onboard.KindMember}, Values: onboard.Answers{"email": {Text: "ngozi@first.example"}, "name": {Text: "Ngozi"}}},
		{ID: onboard.StepJoin, Kinds: []onboard.Kind{onboard.KindMember}},
	}}
	id := joinIdentity{Email: "<your email in second>", Avoid: "ngozi@first.example", Note: "note"}
	got := strings.Join(joinCommands("second", "git@git.second.example:second/store.git", id, plan), "\n")
	if strings.Contains(got, "ngozi@first.example") || !strings.Contains(got, `"email":"<your email in second>"`) {
		t.Fatalf("identity line carries another org's address:\n%s", got)
	}
	if out := (JoinOut{Commands: []string{"x"}, IdentityNote: id.Note}).Text(); !strings.Contains(out, "note") {
		t.Fatalf("Text leaves out the identity note:\n%s", out)
	}
}
