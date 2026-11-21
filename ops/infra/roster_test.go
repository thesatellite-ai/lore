package infra

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeRoster records what reached the roster after validation.
type fakeRoster struct {
	reg    *ActorRegisterIn
	handle string
}

func (f *fakeRoster) ActorRegister(_ context.Context, in ActorRegisterIn) (ActorRegisterOut, error) {
	f.reg = &in
	return ActorRegisterOut{ID: "act_1", Kind: in.Kind, Label: in.Label}, nil
}

func (f *fakeRoster) ActorDeactivate(_ context.Context, h string) (ActorDeactivateOut, error) {
	f.handle = h
	return ActorDeactivateOut{ID: "act_1", Name: "Carol"}, nil
}

func TestActorRegisterInputMatrix(t *testing.T) {
	cases := []struct {
		name     string
		in       ActorRegisterIn
		wantCode errs.Code
		want     ActorRegisterIn
	}{
		{"default human drops agent flags", ActorRegisterIn{Label: "x", Owner: "@a"}, "", ActorRegisterIn{Kind: RegisterKindHuman}},
		{"agent ok", ActorRegisterIn{Kind: RegisterKindAgent, Label: "agent-fe", Owner: "@a"}, "", ActorRegisterIn{Kind: RegisterKindAgent, Label: "agent-fe", Owner: "@a"}},
		{"agent without label", ActorRegisterIn{Kind: RegisterKindAgent}, errs.InvalidInput, ActorRegisterIn{}},
		{"agent label with space", ActorRegisterIn{Kind: RegisterKindAgent, Label: "a b"}, errs.InvalidInput, ActorRegisterIn{}},
		{"agent label with colon", ActorRegisterIn{Kind: RegisterKindAgent, Label: "a:b"}, errs.InvalidInput, ActorRegisterIn{}},
		{"agent label with @", ActorRegisterIn{Kind: RegisterKindAgent, Label: "@a"}, errs.InvalidInput, ActorRegisterIn{}},
		{"unknown kind", ActorRegisterIn{Kind: "team"}, errs.InvalidInput, ActorRegisterIn{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &fakeRoster{}
			_, err := actorRegister(context.Background(), r, c.in)
			if c.wantCode != "" {
				if errs.CodeOf(err) != c.wantCode || r.reg != nil {
					t.Fatalf("code = %v (reached roster: %v), want %v", errs.CodeOf(err), r.reg != nil, c.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if *r.reg != c.want {
				t.Fatalf("roster got %+v, want %+v", *r.reg, c.want)
			}
		})
	}
}

func TestActorDeactivateHandle(t *testing.T) {
	// Every spelling of the handle reaches the roster as "@carol".
	for _, in := range []string{"@carol", "carol", "\t@carol\n"} {
		r := &fakeRoster{}
		if _, err := actorDeactivate(context.Background(), r, ActorDeactivateIn{Handle: in}); err != nil || r.handle != "@carol" {
			t.Fatalf("%q -> %q (%v)", in, r.handle, err)
		}
	}
	for _, in := range []string{"", "@", "  "} {
		r := &fakeRoster{}
		if _, err := actorDeactivate(context.Background(), r, ActorDeactivateIn{Handle: in}); errs.CodeOf(err) != errs.InvalidInput || r.handle != "" {
			t.Fatalf("%q: code %v", in, errs.CodeOf(err))
		}
	}
}

func TestActorDeleteAlwaysRefused(t *testing.T) {
	for _, id := range []string{"", "act_1"} {
		err := actorDeleteRefusal(id)
		if errs.CodeOf(err) != errs.Refused || !strings.Contains(err.Error(), "never deleted") {
			t.Fatalf("id %q: %v", id, err)
		}
	}
}

func TestActorRegisterText(t *testing.T) {
	cases := map[string]ActorRegisterOut{
		"✓ act_1 Alice (@alice) registered in the roster of this repo\n":            {ID: "act_1", Kind: RegisterKindHuman, Name: "Alice", Handle: "alice", Created: true, Where: "this repo"},
		"✓ act_1 Alice already registered (refreshed) in the roster of this repo\n": {ID: "act_1", Kind: RegisterKindHuman, Name: "Alice", Where: "this repo"},
		"✓ act_2 agent agent-fe registered in the roster of agency (agency/org)\n":  {ID: "act_2", Kind: RegisterKindAgent, Label: "agent-fe", Created: true, Where: "agency (agency/org)"},
	}
	for want, o := range cases {
		if got := o.Text(); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}

// TestActorRegisterViaContactText: a register answered by an identity
// contact (TEAM_PROFILES 6.3) says which person and which contact.
func TestActorRegisterViaContactText(t *testing.T) {
	o := ActorRegisterOut{ID: "act_1", Kind: RegisterKindHuman, Name: "Mira Patel", Handle: "mira", Where: "agency (agency/org)", ViaContact: "email:mira.patel@gmail.com"}
	want := "✓ act_1 Mira Patel (@mira) already registered as Mira Patel via identity contact email:mira.patel@gmail.com in the roster of agency (agency/org)\n"
	if got := o.Text(); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	o.ViaContact = ""
	if got := o.Text(); !strings.Contains(got, "already registered (refreshed)") {
		t.Errorf("plain refresh %q", got)
	}
}

// TEAM_PROFILES 5.3: deactivate ends no period or membership, so its line
// is followed by the offboard command for the person as they were named.
func TestActorDeactivateHintsOffboard(t *testing.T) {
	for _, in := range []string{"@carol", "carol", " @carol "} {
		out, err := actorDeactivate(context.Background(), &fakeRoster{}, ActorDeactivateIn{Handle: in})
		if err != nil {
			t.Fatal(err)
		}
		want := "✓ act_1 Carol deactivated (kept on the roster; old rows still resolve)\n  to end their projects and stint too: lore actor offboard --actor @carol\n"
		if got := out.Text(); got != want {
			t.Fatalf("%q: text = %q, want %q", in, got, want)
		}
	}
}

// TestActorRegisterBotKinds (devco:D-868, H-116): the system, cron and
// plugin kinds the schema defines register a bot with a one-word label and
// an owner (default the caller); a bot without a label is refused with a
// hint that names the flag; the line names the owner and the LORE_ACTOR
// value the bot writes with.
func TestActorRegisterBotKinds(t *testing.T) {
	for _, k := range []string{RegisterKindSystem, RegisterKindCron, RegisterKindPlugin} {
		r := &fakeRoster{}
		in := ActorRegisterIn{Kind: k, Label: "nightly-sync", Owner: "@ada"}
		if _, err := actorRegister(context.Background(), r, in); err != nil || r.reg == nil || *r.reg != in {
			t.Fatalf("%s: reached roster %+v, err %v", k, r.reg, err)
		}
		r = &fakeRoster{}
		_, err := actorRegister(context.Background(), r, ActorRegisterIn{Kind: k})
		if errs.CodeOf(err) != errs.InvalidInput || r.reg != nil || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "--kind "+k+" --label ") {
			t.Fatalf("%s without label: %v (hint %q)", k, err, errs.EnvelopeOf(err).Error.Hint)
		}
		if !IsBotKind(k) {
			t.Fatalf("%s is not a bot kind", k)
		}
	}
	for _, k := range []string{RegisterKindHuman, RegisterKindAgent, "hook", ""} {
		if IsBotKind(k) {
			t.Fatalf("%q read as a bot kind", k)
		}
	}
	_, err := actorRegister(context.Background(), &fakeRoster{}, ActorRegisterIn{Kind: "team"})
	if !strings.Contains(err.Error(), "system, cron or plugin") {
		t.Fatalf("unknown kind names only %v", err)
	}
	got := ActorRegisterOut{ID: "act_3", Kind: RegisterKindCron, Label: "nightly-sync", Owner: "Ada (@ada)", Created: true, Where: "agency (agency/org)"}.Text()
	for _, want := range []string{"✓ act_3 cron nightly-sync registered in the roster of agency (agency/org), owned by Ada (@ada)\n",
		"LORE_ACTOR=cron:nightly-sync", "names Ada (@ada) as its owner", "lore actor deactivate --handle @nightly-sync"} {
		if !strings.Contains(got, want) {
			t.Fatalf("text %q lacks %q", got, want)
		}
	}
}
