package profilestore

// admins.go — who may make the org's admin-only changes, read from the
// roster (open stints with org role admin), and the one refusal every
// admin-only op answers (devco R-6 offboard, devco D-637 company rules
// and policies, devco D-681 every folder of the company store).
//
//	op ──▶ src: the repo, the personal store? ──yes──▶ anyone may write
//	          │ no
//	          ▼
//	       roster of src's own org (else env.Roster) ──▶ CompanyGround(level of src, src, roster's org)?
//	          │ no (another store: a client's, a solo store) ──▶ anyone may write
//	          ▼ yes (the org folder, a store root, or any project folder
//	                 of the roster's own store)
//	       ReadAdmins ──▶ caller admin, or no admin yet ──▶ write
//	                  └─▶ else E_REFUSED naming the admins
//
// Why company ground (devco D-637, D-681): a must rule or a policy of the
// org folder reaches every repo of the org, and one of a project folder
// reaches every repo linked to that project, most of the company's rules
// live there. A member who demotes one to quiet a local warning changes
// what every teammate's agent is told. The roster already makes stints
// and memberships an admin's change; the company store's rules and
// policies, in every folder, follow it. D-637 guarded only the org folder,
// so a member's `rule edit --severity should` on a project folder's must
// rule committed (owner use pass 4, wrong 2).

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

// CompanyGround is the one answer to "is src company ground of org"
// (devco D-681), shared by every role check and every printed fix: the
// org folder and a store root always are (every repo of the org reads
// them); a project folder is when its store is org's own store (the
// roster's, Admins.Org: "acme/web" and the linked folder attached as
// "acme" for org acme); the repo, the personal store and the machine are
// not, nor a folder of another store (a client's, a solo store), whose
// rows the roster has no say over. lv is src's settings level, "" when
// unknown: the name decides then ("<store>/org" and a bare name are the
// org's; "<store>/<folder>" is a folder of <store>).
func CompanyGround(lv policy.Level, src store.Source, org string) bool {
	switch lv {
	case policy.LevelOrg, policy.LevelStoreRoot:
		return true
	case policy.LevelFolder:
		return org != "" && orgOf(src) == org
	case "":
	default: // repo, personal, machine
		return false
	}
	s := string(src)
	switch {
	case src == store.SourceRepo || s == personalStore:
		return false
	case strings.HasSuffix(s, "/"+orgFolder) || !strings.Contains(s, "/"):
		return true
	}
	return org != "" && orgOf(src) == org
}

// RepoStander is the Env upgrade that names the store a write to the repo
// store lands in inside a store clone's folder (wire.App RepoStandsFor;
// "" in a code repo): a rule shown there carries the repo source, and the
// role check must judge the folder it really is (devco D-681).
type RepoStander interface {
	RepoStandsFor(ctx context.Context) (store.Source, error)
}

// placeOf is src as the role check judges it: the store clone folder a
// repo source stands for, and src's settings level ("" when the Env does
// not know it).
func placeOf(ctx context.Context, env PlaceEnv, src store.Source) (store.Source, policy.Level) {
	if src == store.SourceRepo {
		if rs, ok := upgrade.As[RepoStander](env); ok {
			if here, err := rs.RepoStandsFor(ctx); err == nil && here != "" {
				src = here
			}
		}
	}
	if l, ok := upgrade.As[Leveler](env); ok {
		if lv, known := l.StoreLevel(src); known {
			return src, lv
		}
	}
	return src, ""
}

// mayBeGround is false for the places that are never company ground
// whatever the roster says (the repo, the personal store, the machine),
// so a write there never opens the roster.
func mayBeGround(src store.Source, lv policy.Level) bool {
	switch lv {
	case policy.LevelRepo, policy.LevelPersonal, policy.LevelMachine:
		return false
	}
	return src != store.SourceRepo && string(src) != personalStore
}

// OrgRosterer is the Env upgrade that opens the roster of the org named
// org (wire.App RosterOfOrg): ok is false when that org keeps no roster
// this machine reaches. The role check judges a folder by its own store's
// roster (devco D-681): from a company repo the Env's roster is the
// company's, so a folder of another org's store (the ops setup's
// "acmeops/infra") was never company ground of it and every member of the
// ops setup could lower its must rules.
type OrgRosterer interface {
	RosterOfOrg(ctx context.Context, org string) (r Roster, done func(), ok bool, err error)
}

// openRoster opens the roster src is judged by (devco D-681): the Env's
// org roster when it is src's own org's, else the roster of src's own
// store's org when that org keeps one (OrgRosterer), else the Env's. ok is
// false when none is readable (no Store upgrade, no org, the repo's own
// roster): with no roster there are no roles to follow (a solo store, a
// machine with no org), and refusing would lock its owner out of their
// own rules.
func openRoster(ctx context.Context, env PlaceEnv, src store.Source) (Roster, func(), bool) {
	r, done, ok := openEnvRoster(ctx, env)
	org := orgOf(src)
	if org == "" || (ok && orgOf(r.Source()) == org) {
		return r, done, ok
	}
	if or, has := upgrade.As[OrgRosterer](env); has {
		own, ownDone, found, err := or.RosterOfOrg(ctx, org)
		if err == nil && found && own != nil && own.Source() != store.SourceRepo {
			done()
			return own, ownDone, true
		}
		if ownDone != nil {
			ownDone()
		}
	}
	return r, done, ok
}

// openEnvRoster opens the Env's org roster (Store upgrade).
func openEnvRoster(ctx context.Context, env PlaceEnv) (Roster, func(), bool) {
	s, ok := upgrade.As[Store](env)
	if !ok {
		return nil, func() {}, false
	}
	r, done, err := s.Roster(ctx, false)
	if err != nil || r == nil || r.Source() == store.SourceRepo {
		done()
		return nil, func() {}, false
	}
	return r, done, true
}

// RequireAdminAt refuses a member's change to company ground (devco
// D-637, D-681): when src is CompanyGround of the roster src is judged by
// (openRoster: its own store's when it has one), the caller must be an
// admin of it (RequireAdmin). Anything else passes, and so does a roster
// that cannot be opened.
func RequireAdminAt(ctx context.Context, env PlaceEnv, src store.Source, does, line string) error {
	src, lv := placeOf(ctx, env, src)
	if !mayBeGround(src, lv) {
		return nil
	}
	r, done, ok := openRoster(ctx, env, src)
	defer done()
	if !ok || !CompanyGround(lv, src, orgOf(r.Source())) {
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
