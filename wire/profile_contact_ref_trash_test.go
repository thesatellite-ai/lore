package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestProfileRosterContactRefInTrash: over a real git org roster, an actor
// reference in contact form (github:, email:, human:<email>, a bare email)
// whose only holder is a removed (trashed) identity contact answers
// E_IN_TRASH with `lore trash restore <contact id>` (DELETION 3.2, the
// rule liveContact applies to a contact id); a live holder always wins
// (a live duplicate on another person, a person's own stable key), and a
// purged or never-written contact stays E_NOT_FOUND. One op-level check:
// actor.contact.list on such a reference says the same.
func TestProfileRosterContactRefInTrash(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	t.Setenv("LORE_HUMAN", "1") // a person runs it: profile writes are refused to agents (TS-4)
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "agency/notes")
	seed := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, seed, opsstores.OpInit, `{"name":"notes","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init the org store: %v", err)
	}
	a := openWith(t, gitRepo(t), Options{
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: rosterStore, URL: url, Folder: "org"}}, nil
		},
		Org: func(context.Context) (*identity.Org, error) {
			return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail, Handle: "alice"}}, nil
		},
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
	})
	withProject(t, a)
	ctx := background()
	mira, _, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "human:mira@example.com", Name: "Mira Patel", Handle: "mira"})
	if err != nil {
		t.Fatal(err)
	}
	omar, _, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "human:omar@example.com", Name: "Omar", Handle: "omar"})
	if err != nil {
		t.Fatal(err)
	}
	r, done, err := a.Roster(ctx, true)
	if err != nil {
		done()
		t.Fatal(err)
	}
	// add writes an identity contact; trashed moves it into the trash.
	ids := map[string]string{}
	add := func(name, actor, typ, val string, trashed bool) {
		t.Helper()
		f := map[string]value.Value{}
		for k, s := range map[string]string{schema.FieldContactActor: actor, schema.FieldContactType: typ,
			schema.FieldContactValue: val, schema.FieldContactValueNorm: strings.ToLower(val)} {
			if f[k], err = value.TextOf(s); err != nil {
				t.Fatal(err)
			}
		}
		f[schema.FieldContactIdentity] = value.BoolOf(true)
		c, err := r.Create(ctx, schema.TableActorContacts, f)
		if err == nil && trashed {
			err = r.Delete(ctx, schema.TableActorContacts, c.ID, "test")
		}
		if err != nil {
			done()
			t.Fatalf("%s: %v", name, err)
		}
		ids[name] = c.ID.String()
	}
	add("gh live", mira, "github", "live-login", false)
	add("gh trashed", mira, "github", "gone-login", true)
	add("gh dup trashed", mira, "github", "dup-login", true)
	add("gh dup live", omar, "github", "dup-login", false)
	add("gh purged", mira, "github", "purged-login", true)
	add("mail live", mira, "email", "mira2@example.org", false)
	add("mail trashed", mira, "email", "old@example.org", true)
	add("mail dup trashed", mira, "email", "shared@example.org", true)
	add("mail dup live", omar, "email", "shared@example.org", false)
	add("mail purged", mira, "email", "purged@example.org", true)
	add("own key trashed", omar, "email", "mira@example.com", true) // mira's own stable key wins
	done()
	for _, name := range []string{"gh purged", "mail purged"} {
		if _, err := run(t, a, "trash.purge", `{"target":"`+ids[name]+`","confirm":true}`, true); err != nil {
			t.Fatalf("purge %s: %v", name, err)
		}
	}

	const notFound, inTrash = "E_NOT_FOUND", "E_IN_TRASH"
	cases := []struct {
		ref  string
		want string // an actor id, notFound, or inTrash
		of   string // the trashed contact inTrash names
	}{
		{"github:live-login", mira, ""},
		{"github:Gone-Login", inTrash, "gh trashed"},
		{"github:dup-login", omar, ""},
		{"github:purged-login", notFound, ""},
		{"github:never-login", notFound, ""},
		{"email:mira2@example.org", mira, ""},
		{"mira2@example.org", mira, ""},
		{"email:old@example.org", inTrash, "mail trashed"},
		{"human:Old@example.org", inTrash, "mail trashed"},
		{"old@example.org", inTrash, "mail trashed"},
		{"email:shared@example.org", omar, ""},
		{"shared@example.org", omar, ""},
		{"email:purged@example.org", notFound, ""},
		{"purged@example.org", notFound, ""},
		{"human:never@example.org", notFound, ""},
		{"never@example.org", notFound, ""},
		{"mira@example.com", mira, ""},
		{"human:mira@example.com", mira, ""},
	}
	r, done, err = a.Roster(ctx, false)
	if err != nil {
		done()
		t.Fatal(err)
	}
	defer done()
	for _, tc := range cases {
		x, err := r.ResolveActor(ctx, tc.ref)
		switch tc.want {
		case inTrash:
			id := ids[tc.of]
			if !errs.Has(err, errs.InTrash) || !strings.Contains(err.Error(), id) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "lore trash restore "+id) {
				t.Errorf("%s: %v, want E_IN_TRASH naming %s with the restore hint", tc.ref, err, id)
			}
		case notFound:
			if !errs.Has(err, errs.NotFound) {
				t.Errorf("%s: %v, want E_NOT_FOUND", tc.ref, err)
			}
		default:
			if err != nil || x.ID.String() != tc.want {
				t.Errorf("%s: %v %v, want %s", tc.ref, x.ID, err, tc.want)
			}
		}
	}
	if _, err := run(t, a, "actor.contact.list", `{"actor":"github:gone-login"}`, true); !errs.Has(err, errs.InTrash) {
		t.Fatalf("actor.contact.list github:gone-login: %v, want E_IN_TRASH", err)
	}
	// `--assign github:<login>` resolves through App.ActorByContact: the same
	// rule (trashed-only → E_IN_TRASH, live duplicate wins, unknown → no match).
	if _, found, err := a.ActorByContact(ctx, "github:gone-login"); found || !errs.Has(err, errs.InTrash) {
		t.Fatalf("ActorByContact github:gone-login: found=%v %v, want E_IN_TRASH", found, err)
	}
	if id, found, err := a.ActorByContact(ctx, "github:dup-login"); err != nil || !found || id.String() != omar {
		t.Fatalf("ActorByContact github:dup-login: %v found=%v %v, want the live holder", id, found, err)
	}
	if _, found, err := a.ActorByContact(ctx, "github:never-was"); found || err != nil {
		t.Fatalf("ActorByContact github:never-was: found=%v %v, want no match", found, err)
	}
}
