package wire

// setupprobe_roster.go — identity.roster and identity.period (ONB 6.1):
// the setup's person on the org roster, and their open company period.
//
// Why here: before this, the prober skipped both checks ("not checked
// here yet"), so `lore doctor` printed "roster – · period –" for a person
// `lore actor show` listed with a roster row and an open employee period.
// They now read the same rows actor show reads: the actors and
// actor_periods rows of the org's roster store (the org store holding the
// org folder, F13), with the read-time period of TP-22 when no period row
// was written yet (teamprofile.PeriodsOf, as actor show's periodsOf).
//
//	ProbeRequest ─▶ org (answer, else setup) + identity emails
//	             ─▶ rosterLookup (once per org and emails per Probe batch)
//	                 orgFolderStore (config.yaml) ─▶ openOrgFolderApp (read only)
//	                 ─▶ resolveOnce(email) ─▶ actor row ─▶ PeriodsOf
//	             ─▶ roster: ok "@handle" | fail "not on the roster"
//	             ─▶ period: ok "<type> since <day>" | fail "no open period"
//
// The roster store is always opened on its own (openDetached), never
// through the repo the command runs in: doctor only reads (ONB 6.1), and
// opening the repo's state in a repo lore is not set up in writes .lore/
// there (A13 bug 3). The same org store a linked repo attaches is the
// one opened, so the answer does not depend on the folder doctor runs in
// or on which of the org's several stores comes first.

import (
	"context"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/schema"
)

// Roster check details (user words: they show in doctor --all and --json).
const (
	// skipNoOrgEntry: the setup names an org this machine has no orgs[]
	// entry for yet (the setup page before the team step is saved).
	skipNoOrgEntry = "the org is not set up on this machine yet"
	// skipNoRosterStore follows the org's name: its orgs[] entry marks no
	// store as holding the org folder (org_folder), so there is no roster
	// to read here.
	skipNoRosterStore = " lists no store holding its roster (org_folder in ~/.lore/config.yaml; lore org refresh fills it from the team's store)"
	// skipNotOnRoster: identity.period needs the roster row identity.roster
	// did not find.
	skipNotOnRoster = "not on the roster; identity.roster says why"
	// detailOnRoster is identity.roster's ok detail for a row with no handle.
	detailOnRoster = "on the roster"
	// periodSince joins a period's type and its first day ("employee since 2026-01-05").
	periodSince = " since "
	// rosterKeySep joins the org and the emails of a rosterLookup memo key.
	rosterKeySep = "\x00"
)

// rosterLook is what the roster says about one setup's person.
type rosterLook struct {
	// skip is a StatusSkip reason (no org entry yet); err a read that
	// failed. Both empty: found tells whether a row matched.
	skip    string
	err     error
	found   bool
	email   string // the address that matched, else the first one asked
	actor   row.Row
	periods []teamprofile.Period
}

// rosterMemo shares one roster read between identity.roster and
// identity.period of the same setup in one Probe batch: both run at once
// (opsdoctor.Parallel), and opening the org store twice for the same
// rows doubled the cost of every doctor --all of an org setup.
type rosterMemo struct {
	mu sync.Mutex
	by map[string]*rosterOnce
}

// rosterOnce is one memo entry: the first caller reads, the others wait.
type rosterOnce struct {
	once sync.Once
	look rosterLook
}

type rosterMemoKey struct{}

// withRosterMemo is ctx with an empty roster memo for one Probe batch.
func withRosterMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, rosterMemoKey{}, &rosterMemo{by: map[string]*rosterOnce{}})
}

// rosterLookup reads the roster for org and emails once per batch (a ctx
// without the memo reads every time).
func (sp setupProber) rosterLookup(ctx context.Context, org string, emails []string) rosterLook {
	m, ok := ctx.Value(rosterMemoKey{}).(*rosterMemo)
	if !ok {
		return sp.readRoster(ctx, org, emails)
	}
	key := org + rosterKeySep + strings.Join(emails, rosterKeySep)
	m.mu.Lock()
	e := m.by[key]
	if e == nil {
		e = &rosterOnce{}
		m.by[key] = e
	}
	m.mu.Unlock()
	e.once.Do(func() { e.look = sp.readRoster(ctx, org, emails) })
	return e.look
}

// readRoster opens org's roster store alone and finds the first of emails
// on it (a bare email resolves through the identity contacts and the
// stable key, TEAM_PROFILES 6.3). No sync: doctor reads the store as this
// machine has it, like actor show; `lore sync` brings a newer roster.
func (sp setupProber) readRoster(ctx context.Context, org string, emails []string) rosterLook {
	look := rosterLook{email: emails[0]}
	one, found, err := orgFolderStore(sp.a.opts.Home, org)
	if err != nil {
		look.err = err
		return look
	}
	if !found {
		if cfg, _, cerr := orgcfg.LoadConfig(sp.a.opts.Home); cerr == nil && cfg.FindOrg(org) == nil {
			look.skip = skipNoOrgEntry
			return look
		}
		// An entry that names no store holding the org folder has no roster
		// this machine can read (a member entry written before its store
		// was known): nothing to check, as before the roster was read.
		look.skip = "org " + org + skipNoRosterStore
		return look
	}
	x, err := openOrgFolderApp(ctx, sp.a.opts, org, one)
	if err != nil {
		look.err = err
		return look
	}
	defer func() { _ = x.Close() }()
	st, err := x.rosterState(ctx, one.Name, false)
	if err != nil {
		look.err = err
		return look
	}
	pr := &profileRoster{app: x, st: st, at: newRosterAt(one.Name, org+" "+one.Folder+"/")}
	for _, e := range emails {
		a, rerr := pr.resolveOnce(ctx, e)
		if errs.Has(rerr, errs.NotFound) || errs.Has(rerr, errs.InTrash) {
			continue
		}
		if rerr != nil {
			look.err = rerr
			return look
		}
		rows, lerr := pr.List(ctx, schema.TableActorPeriods)
		if lerr != nil {
			look.err = lerr
			return look
		}
		ps, perr := teamprofile.PeriodsOf(a, rows)
		if perr != nil {
			look.err = perr
			return look
		}
		look.found, look.email, look.actor, look.periods = true, e, a, ps
		return look
	}
	return look
}

// rosterRequest is the org and identity emails a roster check asks about,
// or the skip result when one is missing.
func (sp setupProber) rosterRequest(r opssetup.ProbeRequest) (string, []string, *opssetup.ProbeResult) {
	org := pick(r.Answers, onboard.QIDOrg, r.Setup.Org)
	if org == "" {
		s := skip(skipNoOrg)
		return "", nil, &s
	}
	var emails []string
	for _, e := range sp.identityEmails(r) {
		if strings.Contains(e, "@") {
			emails = append(emails, e)
		}
	}
	if len(emails) == 0 {
		s := skip(skipNoEmail)
		return "", nil, &s
	}
	return org, emails, nil
}

// rosterCheck is identity.roster and identity.period.
func (sp setupProber) rosterCheck(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	org, emails, s := sp.rosterRequest(r)
	if s != nil {
		return *s
	}
	look := sp.rosterLookup(ctx, org, emails)
	v := map[string]string{onboard.VarOrg: org, onboard.VarEmail: look.email}
	switch {
	case look.skip != "":
		return skip(look.skip)
	case look.err != nil:
		return result(onboard.StatusError, "could not read the "+org+" roster: "+readFailure(look.err), v)
	}
	if r.Check.ID == onboard.CheckIdentityPeriod {
		return periodResult(look, org, v)
	}
	if !look.found {
		return result(onboard.StatusFail, look.email+" is not on the "+org+" roster", v)
	}
	detail := detailOnRoster
	if h := fieldText(look.actor, actorFieldHandle); h != "" {
		detail = rosterHandleMark + h
	}
	return result(onboard.StatusOK, detail, v)
}

// periodResult is identity.period of a roster read: ok with the open
// period ("employee since 2026-01-05"), fail when every period ended.
func periodResult(look rosterLook, org string, v map[string]string) opssetup.ProbeResult {
	if !look.found {
		return skip(skipNotOnRoster)
	}
	for i := len(look.periods) - 1; i >= 0; i-- {
		if p := look.periods[i]; p.Open() {
			return result(onboard.StatusOK, p.Type+periodSince+p.StartedOn.Format(value.DateLayout), v)
		}
	}
	if n := len(look.periods); n > 0 {
		last := look.periods[n-1]
		return result(onboard.StatusFail, "no open period in "+org+": the last one ("+last.Type+") ended "+last.EndedOn.Format(value.DateLayout), v)
	}
	return result(onboard.StatusFail, "no period in "+org+" yet", v)
}

// readFailure is a failed roster read for a doctor line: the lore message
// and its hint (the next step), never the raw cause, which may hold a
// path or a url (errs.EnvelopeOf).
func readFailure(err error) string {
	b := errs.EnvelopeOf(err).Error
	if b.Hint == "" {
		return b.Message
	}
	return b.Message + "; " + b.Hint
}
