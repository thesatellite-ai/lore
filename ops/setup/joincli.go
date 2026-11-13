package setup

// joincli.go — `lore setup join` without the page (B-10). The join line
// `lore member add` prints is often run where no page can be used: over
// ssh, in a script, by a coding agent. There setup.join used to serve the
// page for 30 idle minutes and offer a "solo setup personal" for the
// folder. Now:
//
//	setup.join ─▶ JoinOut.Web.Setup  the setup to make (the org, or the
//	                                 store url's owner: acme for
//	                                 git@host:acme/team-store.git)
//	           ─▶ JoinOut.Commands   the `lore setup apply` lines that make
//	                                 the same member setup the page does,
//	                                 answers prefilled (name and email from
//	                                 git, the plan's values)
//	cmd/lore  ─▶ a terminal (or --web): the lines, then the page
//	          ─▶ no terminal: the lines only, exit 0 (as bare `lore setup`)
//	the page's "This folder" proposal ─▶ a member setup of that name
//	                                     (HereForJoin), never solo

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// joinFallbackName is the setup name when the answer gives none (a store
// url with no owner segment whose repo name is no valid setup name).
const joinFallbackName = "work"

// joinPlaceholderEmail stands in the identity line when git has no email:
// the step refuses it, so the person sees where their address goes.
const joinPlaceholderEmail = "<your email>"

// joinSetupName is the setup name a join answer suggests: an invite's or
// a named org, else the store url's owner (acme for
// git@git.acme.example:acme/team-store.git), else its repo name; "" when
// none is a valid setup name.
func joinSetupName(org, storeURL string) string {
	if onboard.ValidSetupName(org) {
		return org
	}
	if storeURL == "" {
		return ""
	}
	cands := []string{}
	if g, _, ok := onboard.ParseGitURL(storeURL); ok {
		cands = append(cands, g.Owner, g.Repo)
	}
	cands = append(cands, strings.TrimSuffix(path.Base(strings.TrimRight(storeURL, "/")), ".git"))
	for _, c := range cands {
		if c = strings.ToLower(c); onboard.ValidSetupName(c) {
			return c
		}
	}
	return ""
}

// joinCommandSkip are the plan steps the join lines leave out: the folder
// report and the summary save nothing (the page shows them).
var joinCommandSkip = []onboard.StepID{onboard.StepThisFolder, onboard.StepSummary}

// joinCommands are the `lore setup apply` lines that make setup name as a
// member joining with answer, in plan order: every required step, an
// optional one the plan has values for, and an optional one a later line
// needs (github, skipped with {}). email is the identity step's address.
func joinCommands(name, answer, email string, plan PlanOut) []string {
	include := map[onboard.StepID]bool{}
	steps := []PlanStep{}
	for _, st := range plan.Steps {
		if !slices.Contains(st.Kinds, onboard.KindMember) || slices.Contains(joinCommandSkip, st.ID) {
			continue
		}
		steps = append(steps, st)
		include[st.ID] = !st.Optional || len(st.Values) > 0
	}
	for i := len(steps) - 1; i >= 0; i-- {
		if include[steps[i].ID] {
			for _, n := range steps[i].Needs {
				include[n] = true
			}
		}
	}
	var out []string
	for _, st := range steps {
		if !include[st.ID] {
			continue
		}
		ans := onboard.Answers{}
		for k, v := range st.Values {
			ans[k] = v
		}
		switch st.ID {
		case onboard.StepKind:
			ans[onboard.QIDKind] = onboard.Answer{Text: string(onboard.KindMember)}
			ans[onboard.QIDName] = onboard.Answer{Text: name}
		case onboard.StepIdentity:
			if strings.TrimSpace(ans[onboard.QIDEmail].Text) == "" {
				if email == "" {
					email = joinPlaceholderEmail
				}
				ans[onboard.QIDEmail] = onboard.Answer{Text: email}
			}
		case onboard.StepJoin:
			ans[onboard.QIDInvite] = onboard.Answer{Text: answer}
		}
		out = append(out, "lore setup apply --setup "+name+" --step "+string(st.ID)+" --answers "+shellQuote(answersJSON(ans)))
	}
	return out
}

// answersJSON is a step's answers as the --answers JSON ({} when none).
func answersJSON(a onboard.Answers) string {
	b, err := json.Marshal(a)
	if err != nil {
		return "{}" // Answers always marshal; {} keeps the line runnable
	}
	return string(b)
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// joinEmail is the address the identity line carries: the invite's,
// else git's user.email (what a member is matched on the roster by).
func (e engine) joinEmail(ctx context.Context, inviteEmail string) string {
	if inviteEmail != "" {
		return inviteEmail
	}
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return ""
	}
	p, err := pr.Prefill(ctx, onboard.PrefillGitUserEmail, onboard.SetupEntry{})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(p.Value.Text)
}

// JoinContext is the join a setup page serves (`lore setup join`): the
// setup it makes and its org ("" until the store is read).
type JoinContext struct {
	Setup string
	Org   string
}

// joinCtxKey carries a JoinContext on a page request's context.
type joinCtxKey struct{}

// WithJoin marks ctx as a request of the page `lore setup join` opened,
// so setup.list's folder proposal is that member setup (HereForJoin).
func WithJoin(ctx context.Context, j JoinContext) context.Context {
	if j.Setup == "" {
		return ctx
	}
	return context.WithValue(ctx, joinCtxKey{}, j)
}

// HereForJoin is h with its proposal made the member setup of the join
// ctx carries (unchanged without one): someone joining an org is offered
// that org's member setup for this folder, never a solo one.
func HereForJoin(ctx context.Context, h onboard.Here) onboard.Here {
	j := JoinOf(ctx)
	return h.ForJoin(j.Setup, j.Org)
}

// JoinOf is the join ctx carries (WithJoin), zero when none.
func JoinOf(ctx context.Context) JoinContext {
	j, _ := ctx.Value(joinCtxKey{}).(JoinContext)
	return j
}
