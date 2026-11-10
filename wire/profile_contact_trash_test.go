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

// TestProfileRosterContactInTrash: over a real git org roster, the roster
// seam tells a removed (trashed) contact from a gone one, so the contact
// ops answer E_IN_TRASH with `lore trash restore <id>` like every other
// trashed row (DELETION 3.2): live and purged answer nil, trashed answers
// E_IN_TRASH; actor.contact.remove of a removed contact says the same.
func TestProfileRosterContactInTrash(t *testing.T) {
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
	r, done, err := a.Roster(ctx, true)
	if err != nil {
		done()
		t.Fatal(err)
	}
	f := map[string]value.Value{}
	for k, s := range map[string]string{schema.FieldContactActor: mira, schema.FieldContactType: "github",
		schema.FieldContactValue: "octo-personal", schema.FieldContactValueNorm: "octo-personal"} {
		if f[k], err = value.TextOf(s); err != nil {
			t.Fatal(err)
		}
	}
	c, err := r.Create(ctx, schema.TableActorContacts, f)
	if err == nil {
		err = r.InTrash(ctx, schema.TableActorContacts, c.ID) // live: nil
	}
	if err == nil {
		err = r.Delete(ctx, schema.TableActorContacts, c.ID, "test")
	}
	if err != nil {
		done()
		t.Fatal(err)
	}
	err = r.InTrash(ctx, schema.TableActorContacts, c.ID)
	if !errs.Has(err, errs.InTrash) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "lore trash restore "+c.ID.String()) {
		done()
		t.Fatalf("trashed contact: %v, want E_IN_TRASH with the restore hint", err)
	}
	done()
	if _, err := run(t, a, "actor.contact.remove", `{"id":"`+c.ID.String()+`"}`, true); !errs.Has(err, errs.InTrash) {
		t.Fatalf("actor.contact.remove of a removed contact: %v, want E_IN_TRASH", err)
	}
	if _, err := run(t, a, "trash.purge", `{"target":"`+c.ID.String()+`","confirm":true}`, true); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := run(t, a, "actor.contact.remove", `{"id":"`+c.ID.String()+`"}`, true); !errs.Has(err, errs.NotFound) {
		t.Fatalf("actor.contact.remove of a purged contact: %v, want E_NOT_FOUND", err)
	}
}
