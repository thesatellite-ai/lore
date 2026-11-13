package wire

// setupprobe_memberjoin.go — invite.found for a member's join without an
// invite line (`lore setup join --invite <org-or-store-url>`, B2-13). Before this
// the check skipped ("not checked here"), so `lore setup check --step
// join` said ready for an email not on the roster and only apply refused;
// the setup page, which checks first, showed a green step that then
// failed. The check now asks the roster exactly as apply does
// (team.CheckMember on the org folder store):
//
//	answer ─▶ invite line ─▶ skip (the invite ops answer it, as before)
//	       ─▶ org entry here (name, or its store url) ─▶ CheckMember on the
//	          local roster (no network)
//	       ─▶ a store url no entry has ─▶ org.import with Admit =
//	          CheckMember, then always taken out again (errProbeAdmitted),
//	          the same read apply's import makes; nothing stays in
//	          ~/.lore/config.yaml
//	       ─▶ a name no entry has ─▶ fail E_JOIN_ORG_UNKNOWN's message

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/orgcmd"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/team"
)

// errProbeAdmitted ends a check's org.import after the roster said yes,
// so the import takes its entry out again (a check writes nothing).
var errProbeAdmitted = errors.New("setup check: on the roster (the import is taken back)")

// orgForStoreURL is the orgs[] entry name holding the store at url, ""
// when no entry does (storeKey compares the url forms).
func orgForStoreURL(home, url string) string {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return ""
	}
	want, _ := storeKey(url)
	for _, o := range cfg.Orgs {
		for _, st := range o.Stores {
			if k, ok := storeKey(st.URL); ok && k == want {
				return o.Name
			}
		}
	}
	return ""
}

// memberJoinFound is invite.found of a join step whose answer is an org
// name or store url (see the file comment).
func (sp setupProber) memberJoinFound(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	answer := answerText(r.Answers, onboard.QIDInvite)
	if answer == "" || strings.Contains(answer, onboard.InvitePrefix) {
		return skip(notHereDetail(r.Check.ID))
	}
	// the same addresses apply joins with: the setup's saved progress
	// (the identity step answered before this one), this step on top
	ans, aerr := setupApplier{a: sp.a}.setupAnswers(ctx, r.Setup, r.Answers)
	if aerr != nil {
		return result(onboard.StatusError, readFailure(aerr), nil)
	}
	var emails []string
	for _, e := range append([]string{answerText(ans, onboard.QIDEmail)}, ans[onboard.QIDEmails].List...) {
		if e = strings.TrimSpace(e); strings.Contains(e, "@") && !slices.Contains(emails, e) {
			emails = append(emails, e)
		}
	}
	if len(emails) == 0 {
		return skip(skipNoEmail)
	}
	admit := func(ctx context.Context, entry string) error {
		return team.CheckMember(ctx, orgRosterStore{a: sp.a, org: entry}, entry, emails)
	}
	var err error
	switch org := answer; {
	case !strings.ContainsAny(answer, ":/"):
		if _, oerr := sp.a.TeamOrigin(ctx, answer); oerr != nil {
			return result(onboard.StatusFail, errs.EnvelopeOf(opssetup.UnknownOrgErr(answer)).Error.Message, nil)
		}
		err = admit(ctx, org)
	case orgForStoreURL(sp.a.opts.Home, answer) != "":
		err = admit(ctx, orgForStoreURL(sp.a.opts.Home, answer))
	default:
		host, ok := sp.a.OrgHost(ctx)
		if !ok {
			return skip(notHereDetail(r.Check.ID))
		}
		name, _, _ := strings.Cut(emails[0], "@")
		name = cmp.Or(answerText(ans, onboard.QIDName), name)
		_, err = host.Import(ctx, orgcmd.ImportIn{URL: answer, Key: humanKeyOf(emails[0]), Name: name, Handle: answerText(ans, onboard.QIDHandle), SkipRegister: true,
			Admit: func(ctx context.Context, entry string) error {
				if aerr := admit(ctx, entry); aerr != nil {
					return aerr
				}
				return errProbeAdmitted
			}}, true)
		if errors.Is(err, errProbeAdmitted) {
			err = nil
		}
	}
	switch {
	case err == nil:
		return result(onboard.StatusOK, detailOnRoster, nil)
	case errs.Has(err, errs.NotFound):
		return result(onboard.StatusFail, readFailure(err), map[string]string{onboard.VarEmail: emails[0], onboard.VarFixVariant: string(onboard.FixMemberJoin)})
	}
	return result(onboard.StatusError, readFailure(err), nil)
}

// humanKeyOf is the stable key of a person by email (human:<email>).
func humanKeyOf(email string) string { return "human:" + email }
