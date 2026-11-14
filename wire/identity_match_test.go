package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// Identity keys of the identity matching tests (TEAM_PROFILES 12): Mira's
// org key, her personal email (an identity contact of her actor), Omar.
const (
	imMiraKey     = "human:mira@agency.example"
	imMiraSecond  = "human:Mira.Patel@Gmail.com"
	imMiraEmail   = "mira.patel@gmail.com"
	imOmarKey     = "human:omar@agency.example"
	imUnknownKey  = "human:nobody@else.example"
	imContactMira = "acn_01a1a59b891c70b789144cd3e711fb01"
	imContactOmar = "acn_01a1a59b891c70b789144cd3e711fb02"
)

// imWorld is one org roster store shared by several Apps (one per clone).
type imWorld struct{ orgURL string }

func newIMWorld(t *testing.T) imWorld {
	return imWorld{orgURL: folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)}
}

// app opens an App in a fresh org repo (not a client repo) whose identity
// chain answers key.
func (w imWorld) app(t *testing.T, key string) *App {
	t.Helper()
	t.Setenv(identity.EnvHuman, "1") // a person, whatever agent the test runs under
	a, err := New(Options{RepoDir: gitRepo(t), Home: t.TempDir(), FlagActor: key,
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: rosterStore, URL: w.orgURL, Folder: "org"}}, nil
		},
		Org: func(context.Context) (*identity.Org, error) {
			return &identity.Org{Name: "agency", Identity: identity.Entry{Key: key}}, nil
		},
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func imActorID(t *testing.T, key string) ids.ID[ids.Actor] {
	t.Helper()
	id, err := identity.ActorID(key)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seed writes Mira's and Omar's roster rows and the contact rows given
// (contact id ─▶ actor key, email, identity flag) from an admin clone.
func (w imWorld) seed(t *testing.T, contacts map[string]imContact) {
	t.Helper()
	a := w.app(t, imMiraKey)
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	for key, name := range map[string]string{imMiraKey: "Mira Patel", imOmarKey: "Omar"} {
		f := textValues(t, map[string]string{"kind": schema.ActorKindHuman, "display_name": name, "stable_key": key,
			"handle": strings.TrimSuffix(strings.TrimPrefix(key, "human:"), "@agency.example")})
		if _, err = eng.CreateWithID(background(), rosterStore, schema.TableActors, imActorID(t, key).Any(), f); err != nil && !errs.Has(err, errs.Stale) {
			t.Fatal(err)
		}
	}
	for id, c := range contacts {
		cid, perr := ids.ParseAny(id)
		if perr != nil {
			t.Fatal(perr)
		}
		f := textValues(t, map[string]string{schema.FieldContactActor: imActorID(t, c.owner).String(), schema.FieldContactType: "email",
			schema.FieldContactValue: c.email, schema.FieldContactValueNorm: strings.ToLower(c.email)})
		f[schema.FieldContactIdentity] = value.BoolOf(c.identity)
		if _, err = eng.CreateWithID(background(), rosterStore, schema.TableActorContacts, cid, f); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.Close()
}

type imContact struct {
	owner, email string
	identity     bool
}

// actorRowsWithKey lists the actor rows of src whose stable_key is key.
func actorRowsWithKey(t *testing.T, a *App, src store.Source, key string) []string {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := eng.List(background(), src, schema.TableActors)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if !r.Deleted && fieldText(r, actorFieldKey) == key {
			out = append(out, r.ID.String())
		}
	}
	return out
}

// TP-4 / 6.3: an open whose key is Mira's second email (an identity
// contact of her actor) writes as Mira: the engine's actor is hers, the
// actor row written into a new store is hers (her stable_key), and no
// actor row keyed by the second email appears anywhere.
func TestCanonicalActorRebindsSecondEmail(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newIMWorld(t)
	w.seed(t, map[string]imContact{imContactMira: {imMiraKey, "Mira.Patel@gmail.com", true}})
	mira := imActorID(t, imMiraKey)
	b := w.app(t, imMiraSecond)
	eng, err := b.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	if eng.Actor() != mira {
		t.Fatalf("writing actor = %s, want Mira %s", eng.Actor(), mira)
	}
	got, err := b.Actor(background())
	if err != nil || got != mira {
		t.Fatalf("App.Actor = %s %v, want %s", got, err, mira)
	}
	if err = b.EnsureActor(background(), store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	if rows := actorRowsWithKey(t, b, store.SourceRepo, imMiraKey); len(rows) != 1 || rows[0] != mira.String() {
		t.Fatalf("repo actor rows with Mira's key = %v, want her one row", rows)
	}
	for _, src := range []store.Source{store.SourceRepo, rosterStore} {
		if rows := actorRowsWithKey(t, b, src, imMiraSecond); len(rows) != 0 {
			t.Fatalf("%s: an actor row keyed by the second email: %v", src, rows)
		}
		if _, _, gerr := eng.Get(background(), engine.Ref{Store: src, Table: schema.TableActors, ID: imActorID(t, imMiraSecond).Any()}); gerr == nil {
			t.Fatalf("%s: the second email's own actor id was written", src)
		}
	}
	line, err := b.IdentityContactLine(background())
	if err != nil || line != "identity contact: "+imMiraEmail+" is an identity contact of Mira Patel ("+mira.String()+")" {
		t.Fatalf("whoami line = %q %v", line, err)
	}
	// G-14: whoami's identity line names the same actor, never the id the
	// second email would derive (an actor no row uses).
	if got := (&standaloneWhoami{app: fixedApp{b}}).reboundActor(background()); got != mira.String() {
		t.Fatalf("whoami actor = %q, want Mira %s (not %s)", got, mira, imActorID(t, imMiraSecond))
	}
}

// 6.5 target 1 and the fail-closed cases: a contact not flagged for
// identity, a contact held by two people, and an unknown key all leave
// the identity chain's own actor (no silent rebind).
func TestCanonicalActorNoRebind(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	cases := []struct {
		name     string
		key      string
		contacts map[string]imContact
	}{
		{"identity false", imMiraSecond, map[string]imContact{imContactMira: {imMiraKey, imMiraEmail, false}}},
		{"held by two people", imMiraSecond, map[string]imContact{imContactMira: {imMiraKey, imMiraEmail, true}, imContactOmar: {imOmarKey, imMiraEmail, true}}},
		{"no contact", imUnknownKey, map[string]imContact{imContactMira: {imMiraKey, imMiraEmail, true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newIMWorld(t)
			w.seed(t, tc.contacts)
			b := w.app(t, tc.key)
			eng, err := b.Engine(background())
			if err != nil {
				t.Fatal(err)
			}
			if own := imActorID(t, tc.key); eng.Actor() != own {
				t.Fatalf("writing actor = %s, want the key's own %s", eng.Actor(), own)
			}
			line, err := b.IdentityContactLine(background())
			if err != nil {
				t.Fatal(err)
			}
			if got := (&standaloneWhoami{app: fixedApp{b}}).reboundActor(background()); got != "" {
				t.Fatalf("no rebind, yet whoami names actor %s", got)
			}
			if tc.name == "held by two people" {
				if !strings.Contains(line, "Mira Patel") || !strings.Contains(line, "Omar") {
					t.Fatalf("ambiguous whoami line = %q, want both names", line)
				}
				found, derr := b.DoctorIdentityContacts(background())
				if derr != nil || len(found) != 1 || !strings.Contains(found[0].Message, "email:"+imMiraEmail) {
					t.Fatalf("doctor = %+v %v, want one duplicate finding", found, derr)
				}
				return
			}
			if line != "" {
				t.Fatalf("whoami line = %q, want none", line)
			}
			if found, derr := b.DoctorIdentityContacts(background()); derr != nil || len(found) != 0 {
				t.Fatalf("doctor = %+v %v, want clean", found, derr)
			}
		})
	}
}

// 6.3: `lore actor register` by Mira's second email answers her actor,
// already registered, and writes no second actor.
func TestRegisterIdentityContactMatch(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newIMWorld(t)
	w.seed(t, map[string]imContact{imContactMira: {imMiraKey, imMiraEmail, true}})
	b := w.app(t, imMiraSecond)
	out, err := b.ActorRegister(background(), opsinfra.ActorRegisterIn{})
	if err != nil {
		t.Fatal(err)
	}
	mira := imActorID(t, imMiraKey)
	if out.ID != mira.String() || out.Created || out.Name != "Mira Patel" {
		t.Fatalf("register = %+v, want Mira's actor, not created", out)
	}
	if rows := actorRowsWithKey(t, b, rosterStore, imMiraSecond); len(rows) != 0 {
		t.Fatalf("register wrote a second actor: %v", rows)
	}
	// someone whose key is no identity contact still registers as before
	c := w.app(t, imUnknownKey)
	out, err = c.ActorRegister(background(), opsinfra.ActorRegisterIn{})
	if err != nil || !out.Created || out.ID != imActorID(t, imUnknownKey).String() {
		t.Fatalf("plain register = %+v %v", out, err)
	}
}

// 6.3: the contact forms of an actor reference resolve in the profile
// ops (ResolveActor) and in ops/task (ActorByContact).
func TestResolveActorIdentityContact(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newIMWorld(t)
	w.seed(t, map[string]imContact{imContactMira: {imMiraKey, imMiraEmail, true}})
	b := w.app(t, imOmarKey)
	r, done, err := b.Roster(background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	mira := imActorID(t, imMiraKey)
	for _, ref := range []string{"email:" + imMiraEmail, "Mira.Patel@Gmail.com", "human:" + imMiraEmail, "mira@agency.example", "@mira", mira.String()} {
		x, rerr := r.ResolveActor(background(), ref)
		if rerr != nil || x.ID != mira.Any() {
			t.Errorf("ResolveActor(%q) = %s %v, want Mira", ref, x.ID, rerr)
		}
	}
	if _, rerr := r.ResolveActor(background(), "email:nobody@else.example"); !errs.Has(rerr, errs.NotFound) {
		t.Errorf("unknown contact: %v, want E_NOT_FOUND", rerr)
	}
	id, ok, err := b.ActorByContact(background(), "email:"+imMiraEmail)
	if err != nil || !ok || id != mira {
		t.Fatalf("ActorByContact = %s %v %v", id, ok, err)
	}
	if _, ok, err = b.ActorByContact(background(), "@mira"); err != nil || ok {
		t.Fatalf("a handle is no contact form: %v %v", ok, err)
	}
}
