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
	"cmp"
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
// a named org, else the store url's owner when the url is a forge's
// owner/repo (acme for git@git.acme.example:acme/team-store.git), else
// its repo name; "" when none is a valid setup name. A deeper path is a
// folder on a self-hosted server (ssh://git.example.com/srv/git/team.git),
// whose first segment (srv) names a disk, never the org (devco P-8).
func joinSetupName(org, storeURL string) string {
	if onboard.ValidSetupName(org) {
		return org
	}
	if storeURL == "" {
		return ""
	}
	cands := []string{}
	if g, _, ok := onboard.ParseGitURL(storeURL); ok && g.Owner != "" && urlPathDepth(storeURL) == 2 {
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

// urlPathDepth is the number of path segments of a store url
// (2 for git@host:acme/store.git and https://host/acme/store).
func urlPathDepth(storeURL string) int {
	p := storeURL
	if _, rest, ok := strings.Cut(p, "://"); ok {
		_, p, _ = strings.Cut(rest, "/")
	} else if _, rest, ok := strings.Cut(p, ":"); ok {
		p = rest
	}
	return len(strings.FieldsFunc(p, func(r rune) bool { return r == '/' }))
}

// joinCommandSkip are the plan steps the join lines leave out: the folder
// report and the summary save nothing (the page shows them).
var joinCommandSkip = []onboard.StepID{onboard.StepThisFolder, onboard.StepSummary}

// joinCommands are the `lore setup apply` lines that make setup name as a
// member joining with answer, in plan order: every required step, an
// optional one the plan has values for, and an optional one a later line
// needs (github, skipped with {}). id is the identity step's address: its
// Email fills an empty answer, and replaces a prefilled one equal to
// id.Avoid (another org's identity on this machine, devco N-4).
func joinCommands(name, answer string, id joinIdentity, plan PlanOut) []string {
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
			if have := strings.TrimSpace(ans[onboard.QIDEmail].Text); have == "" || (id.Avoid != "" && strings.EqualFold(have, id.Avoid)) {
				email := id.Email
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
// <, > and & read as typed: json escapes them (\u003cyour email\u003e for
// the "<your email>" placeholder, which a person cannot read), and the
// escape is undone here; inside a JSON string both spellings are the same
// value.
func answersJSON(a onboard.Answers) string {
	b, err := json.Marshal(a)
	if err != nil {
		return "{}" // Answers always marshal; {} keeps the line runnable
	}
	return htmlUnescaper.Replace(string(b))
}

// htmlUnescaper undoes json's HTML escapes.
var htmlUnescaper = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&")

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// joinIdentity is the address the identity line of a join carries.
type joinIdentity struct {
	// Email is the address the line answers with ("" = the placeholder).
	Email string
	// Avoid is an address the line must never carry: this machine's git
	// email when it is already another org's identity here (N-4).
	Avoid string
	// Note says why the line carries a placeholder instead of the git
	// email ("" when it does not); JoinOut.Text prints it.
	Note string
}

// joinEmail is the identity of a join of org: the invite's address, else
// git's email. A git email that is already the identity of ANOTHER org on
// this machine is never offered (devco N-4: a person in two orgs has one
// address in each, docs/team/use-cases/two-companies.md; the line carried
// the first org's address, the join failed and told the admin to add that
// address to the second org's roster): the line then asks for the address
// this org's admin added, and Note says why.
func (e engine) joinEmail(ctx context.Context, inviteEmail, org string) joinIdentity {
	if inviteEmail != "" {
		return joinIdentity{Email: inviteEmail}
	}
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return joinIdentity{}
	}
	p, err := pr.Prefill(ctx, onboard.PrefillGitUserEmail, onboard.SetupEntry{})
	if err != nil {
		return joinIdentity{}
	}
	email := strings.TrimSpace(p.Value.Text)
	other := e.orgOwningEmail(ctx, email, org)
	if other == "" {
		return joinIdentity{Email: email}
	}
	where := cmp.Or(org, "this org")
	ph := "<your email in " + where + ">"
	return joinIdentity{Email: ph, Avoid: email,
		Note: "Note: your git email " + email + " is your identity in org " + other + " on this machine, and each org keeps its own identity, so the identity line asks for the address the admin of " + where + " added you with (lore member list run by them shows it): replace " + ph + " with it before you run the line."}
}

// orgOwningEmail is the org entry on this machine, other than org, whose
// identity is email (its key human:<email>, or one of its addresses); ""
// when none or when the config does not read.
func (e engine) orgOwningEmail(ctx context.Context, email, org string) string {
	if email == "" {
		return ""
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return ""
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(cfg.OrgIdentities))
	for n := range cfg.OrgIdentities {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if strings.EqualFold(n, org) {
			continue
		}
		id := cfg.OrgIdentities[n]
		if strings.EqualFold(strings.TrimPrefix(id.Key, humanKeyPrefix), email) || slices.ContainsFunc(id.Emails, func(x string) bool { return strings.EqualFold(x, email) }) {
			return n
		}
	}
	return ""
}

// humanKeyPrefix starts a person's identity key (human:<email>).
const humanKeyPrefix = "human:"

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
