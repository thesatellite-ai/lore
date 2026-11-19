package profilestore

// judge.go — who may change each place a hint names, for a hint that
// names many places at once (the budget warning: every pinned row and
// the place the budget is set; devco D-681). RequireAdminAt judges one
// write by the roster of the place's own store; a hint must give the
// same answer per place, or it prints a member a command the write then
// refuses (from the ops store clone an ops folder's must rule was printed
// as the member's own fix, judged by the company roster).
//
//	place ─▶ placeOf (a repo source in a store clone: the folder it is)
//	      ─▶ never ground (repo, personal, machine) ─▶ anyone
//	      ─▶ openRoster (its own store's org, else the Env's), once per org
//	      ─▶ CompanyGround of that roster ─▶ its admins decide

import (
	"context"

	"github.com/thesatellite-ai/lore/store"
)

// Judge answers RequireAdminAt's question for many places without writing
// anything: whether a place is company ground and whose admins may change
// it. It opens each org's roster once, so one Judge serves one render.
type Judge struct {
	env   PlaceEnv
	byOrg map[string]judged
}

// judged is one org's roster answer: its admins, and ok false when no
// roster was readable (no roles to follow, RequireAdminAt passes).
type judged struct {
	admins Admins
	ok     bool
}

// NewJudge is a Judge over env (wire.App in the render host).
func NewJudge(env PlaceEnv) *Judge {
	return &Judge{env: env, byOrg: map[string]judged{}}
}

// At reports whether src is company ground of the roster src is judged by
// (the answer RequireAdminAt gives the write) and that roster's admins.
// company false means anyone may change src; the zero Admins then. With
// no readable roster the org folder and a store root are still company
// ground, with the zero Admins (no roles to follow: May is true, as the
// write's check passes).
func (j *Judge) At(ctx context.Context, src store.Source) (company bool, admins Admins) {
	if j == nil || j.env == nil {
		return false, Admins{}
	}
	src, lv := placeOf(ctx, j.env, src)
	if !mayBeGround(src, lv) {
		return false, Admins{}
	}
	key := orgOf(src)
	got, seen := j.byOrg[key]
	if !seen {
		got = j.read(ctx, src)
		j.byOrg[key] = got
	}
	if !got.ok {
		return CompanyGround(lv, src, ""), Admins{}
	}
	if !CompanyGround(lv, src, got.admins.Org) {
		return false, Admins{}
	}
	return true, got.admins
}

// read opens the roster src is judged by and reads its admins.
func (j *Judge) read(ctx context.Context, src store.Source) judged {
	r, done, ok := openRoster(ctx, j.env, src)
	defer done()
	if !ok {
		return judged{}
	}
	ad, err := ReadAdmins(ctx, r)
	if err != nil {
		return judged{}
	}
	return judged{admins: ad, ok: true}
}
