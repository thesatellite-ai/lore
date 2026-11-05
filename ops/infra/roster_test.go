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
		"✓ act_1 Alice (@alice) registered in this repo\n":            {ID: "act_1", Kind: RegisterKindHuman, Name: "Alice", Handle: "alice", Created: true, Where: "this repo"},
		"✓ act_1 Alice already registered (refreshed) in this repo\n": {ID: "act_1", Kind: RegisterKindHuman, Name: "Alice", Where: "this repo"},
		"✓ act_2 agent agent-fe registered in agency org/\n":          {ID: "act_2", Kind: RegisterKindAgent, Label: "agent-fe", Created: true, Where: "agency org/"},
	}
	for want, o := range cases {
		if got := o.Text(); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}
