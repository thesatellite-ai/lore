package profilestore

// admins.go — who may make the org's admin-only changes, read from the
// roster (open stints with org role admin), and the one refusal every
// admin-only op answers (devco R-6 offboard, devco D-637 company rules
// and policies).
//
//	op ──▶ CompanyPlace(env, src)? ──no──▶ anyone may write
//	          │ yes (the org folder or a store root: every repo of the org reads it)
//	          ▼
//	       env.Roster(read) ──▶ ReadAdmins ──▶ caller admin, or no admin yet ──▶ write
//	                                       └─▶ else E_REFUSED naming the admins
//
// Why company places (devco D-637): a must rule or a policy of the org
// folder reaches every repo of the org, so a member who demotes one to
// quiet a local warning changes what every teammate's agent is told.
// The roster already makes stints and memberships an admin's change; the
// org's own rules and policies follow it. A project folder's rules and
// policies stay every member's to change.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OrgRoleAdmin is the org_role of an admin's stint (ops/team OrgRoleAdmin;
// ops/team imports this package, so the value is repeated here).
const OrgRoleAdmin = "admin"

// Admins is who the roster names as the org's admins.
type Admins struct {
	// Org is the org the roster belongs to ("acme" of "acme/org"), "" when
	// the roster is not an org store's.
	Org string
	// Names are the admins' display names (their @handle when unnamed),
	// sorted.
	Names []string
	// Caller is true when the caller holds an open admin stint.
	Caller bool
}

// May reports whether the caller may make an admin-only change: an admin,
// or anyone while the roster names no admin (an org from before org
// roles; `lore doctor --fix` gives its first admin the role).
func (a Admins) May() bool { return a.Caller || len(a.Names) == 0 }

// Who is "an admin of <org> (<names>)" for a hint that sends a member to
// an admin; "an admin of the org" when the names are unknown.
func (a Admins) Who() string {
	org := "the org"
	if a.Org != "" {
		org = a.Org
	}
	if len(a.Names) == 0 {
		return "an admin of " + org
	}
	return "an admin of " + org + " (" + strings.Join(a.Names, ", ") + ")"
}

// ReadAdmins reads the admins of r's org and whether its caller is one.
func ReadAdmins(ctx context.Context, r Roster) (Admins, error) {
	periods, err := r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return Admins{}, err
	}
	caller := r.Caller().String()
	out := Admins{Org: orgOf(r.Source())}
	admin := map[string]bool{}
	for _, p := range periods {
		if fieldText(p, schema.FieldPeriodEndedOn) != "" || fieldText(p, schema.FieldPeriodOrgRole) != OrgRoleAdmin {
			continue
		}
		id := fieldText(p, schema.FieldPeriodActor)
		admin[id] = true
		out.Caller = out.Caller || (id != "" && id == caller)
	}
	if len(admin) == 0 {
		return out, nil
	}
	actors, err := r.List(ctx, schema.TableActors)
	if err != nil {
		return Admins{}, err
	}
	for _, a := range actors {
		if !admin[a.ID.String()] {
			continue
		}
		name := fieldText(a, schema.FieldActorDisplayName)
		if name == "" {
			name = "@" + fieldText(a, schema.FieldActorHandle)
		}
		out.Names = append(out.Names, name)
		delete(admin, a.ID.String())
	}
	for id := range admin { // a stint whose actor row is not readable here
		out.Names = append(out.Names, id)
	}
	slices.Sort(out.Names)
	return out, nil
}

// RequireAdmin refuses a caller who may not make an admin-only change
// (Admins.May): E_REFUSED saying what only an admin does, naming the
// admins, and the line an admin runs. Nothing is written before it.
func RequireAdmin(ctx context.Context, r Roster, does, line string) error {
	a, err := ReadAdmins(ctx, r)
	if err != nil {
		return err
	}
	if a.May() {
		return nil
	}
	return errs.New(errs.Refused, "only an admin of the org can "+does+", and you are not one on the roster of "+r.Where()+"; nothing was written").
		WithHint("ask " + a.Who() + " to run: " + line)
}

// Leveler is the Env upgrade that says an attached store's settings level
// (wire.App; ops/settings.Leveler is the same method).
type Leveler interface {
	StoreLevel(src store.Source) (policy.Level, bool)
}

// CompanyPlace reports whether a row of src reaches the whole org: the
// org folder (`<store>/org`) or a store's root, whose rows every folder of
// the store reads. A project folder, the repo and the personal store are
// not. The Env's answer wins (a linked folder attached under the bare org
// name is a folder); without one the name decides.
func CompanyPlace(env PlaceEnv, src store.Source) bool {
	if l, ok := upgrade.As[Leveler](env); ok {
		if lv, known := l.StoreLevel(src); known && lv != "" {
			return CompanyLevel(lv)
		}
	}
	s := string(src)
	switch {
	case src == store.SourceRepo || s == personalStore:
		return false
	case strings.HasSuffix(s, "/"+orgFolder):
		return true
	}
	return !strings.Contains(s, "/")
}

// PlaceEnv is what the company-place checks need from an ops package's
// Env: every ops Env (memory, settings, row …) reads a row by ref, which
// is the one method they all share; the optional Leveler and Store
// upgrades are checked with upgrade.As. A named interface keeps
// hand-written code free of `any` (internal/layers TestNoAny).
type PlaceEnv interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// CompanyLevel reports whether a settings level is the org's own (the org
// folder, or a store root every folder reads).
func CompanyLevel(lv policy.Level) bool {
	return lv == policy.LevelOrg || lv == policy.LevelStoreRoot
}

// RequireAdminAt refuses a member's change to a company place (devco
// D-637): when CompanyPlace(env, src) and the Env opens a roster
// (Store), the caller must be an admin of it (RequireAdmin). Anything
// else passes. A roster that cannot be opened passes too: with no org
// roster readable there are no roles to follow (a solo store, a machine
// with no org), and refusing would lock its owner out of their own rules.
func RequireAdminAt(ctx context.Context, env PlaceEnv, src store.Source, does, line string) error {
	if !CompanyPlace(env, src) {
		return nil
	}
	s, ok := upgrade.As[Store](env)
	if !ok {
		return nil
	}
	r, done, err := s.Roster(ctx, false)
	defer done()
	if err != nil || r == nil || r.Source() == store.SourceRepo {
		return nil
	}
	return RequireAdmin(ctx, r, does, line)
}

// personalStore and orgFolder are the personal store's name and the org
// folder's name (wire's attach.ReservedMe / the org folder).
const (
	personalStore = "me"
	orgFolder     = "org"
)

// orgOf is the org of a roster source ("acme" of "acme/org").
func orgOf(src store.Source) string {
	s := string(src)
	if s == "" || src == store.SourceRepo {
		return ""
	}
	org, _, _ := strings.Cut(s, "/")
	return org
}

// fieldText is a field's text, "" when absent or null.
func fieldText(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}
