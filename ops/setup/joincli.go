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
		include[st.ID] = !st.Optional || len(st.Values) > 0 || (st.ID == onboard.StepGitHub && id.GitHub != "")
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
			if id.Name != "" {
				ans[onboard.QIDName] = onboard.Answer{Text: id.Name}
			}
		case onboard.StepGitHub:
			if id.GitHub != "" {
				ans[onboard.QIDGitHubAccount] = onboard.Answer{Text: id.GitHub}
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
	// Name and GitHub are the roster's name and GitHub login of Email
	// (devco D-487); "" keeps the plan's prefill (global git, gh).
	Name, GitHub string
	// NameFrom and GitHubFrom say where the prefilled values came from
	// (JoinOut.PrefillNote).
	NameFrom, GitHubFrom string
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

// rosterEmail is the one of this machine's git emails (global first, then
// each includeIf file's) the org's roster has, for a join by org name or
// store url; "" when git names one email or fewer, or none is on the
// roster, or the roster cannot be read (the line then carries git's global
// email as before). devco D-448: a person whose global git email is a
// personal one, with the work address in an includeIf file, got a join
// line with the personal address, which the roster does not know. Each
// try is the join step's own invite.found check, so only a machine with
// several git emails reads the store here.
func (e engine) rosterEmail(ctx context.Context, setup, answer string) string {
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return ""
	}
	p, err := pr.Prefill(ctx, onboard.PrefillGitEmails, onboard.SetupEntry{})
	if err != nil || len(p.Choices) < 2 {
		return ""
	}
	c, ok := e.cat.Check(onboard.CheckInviteFound, onboard.StepJoin, onboard.KindMember, nil)
	if !ok {
		return ""
	}
	entry := onboard.SetupEntry{Name: setup, Kind: onboard.KindMember}
	for _, ch := range p.Choices {
		got := pr.Probe(ctx, []ProbeRequest{{Check: c, Setup: entry,
			Answers: onboard.Answers{onboard.QIDInvite: {Text: answer}, onboard.QIDEmail: {Text: ch.Value}}}})
		if len(got) == 1 && got[0].Status == onboard.StatusOK {
			return ch.Value
		}
	}
	return ""
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

// JoinPerson is what the org's roster says about the person joining
// (devco D-487): the name and GitHub login the join lines prefill instead
// of global git's user.name.
type JoinPerson struct {
	Name   string `json:"name,omitempty"`
	GitHub string `json:"github,omitempty"`
}

// JoinPersonFinder is the optional Prober upgrade that reads the roster
// entry of email in the org a join answer names (an org on this machine,
// or a store url, read as the join step's own check reads it); ok false
// when the roster cannot be read or has no member with that address.
type JoinPersonFinder interface {
	JoinPerson(ctx context.Context, setup, answer, email string) (JoinPerson, bool)
}

// joinPerson fills id's Name and GitHub from the roster entry of its
// email in the org answer names, else from global git (name) and the
// github step's own prefill; NameFrom and GitHubFrom say where each came
// from (JoinOut.PrefillNote).
func (e engine) joinPerson(ctx context.Context, id joinIdentity, setup, answer, org string) joinIdentity {
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return id
	}
	where := cmp.Or(org, setup)
	if f, ok := pr.(JoinPersonFinder); ok && strings.Contains(id.Email, "@") && answer != "" {
		if p, found := f.JoinPerson(ctx, setup, answer, id.Email); found {
			// a name member add derived from the address (bob for
			// bob@acme.example, no --name given) is a placeholder: git's
			// user.name is a better guess then
			local, _, _ := strings.Cut(id.Email, "@")
			if p.Name != "" && !strings.EqualFold(p.Name, local) {
				id.Name, id.NameFrom = p.Name, "the roster of "+where+" ("+id.Email+")"
			}
			if p.GitHub != "" {
				id.GitHub, id.GitHubFrom = p.GitHub, "the roster of "+where+" ("+id.Email+")"
			}
		}
	}
	if id.NameFrom == "" {
		if p, perr := pr.Prefill(ctx, onboard.PrefillGitUserName, onboard.SetupEntry{}); perr == nil && strings.TrimSpace(p.Value.Text) != "" {
			id.NameFrom = "git config --global user.name (the roster of " + where + " gave none)"
		}
	}
	return id
}

// prefillNote says where the join lines' name and GitHub account came
// from; "" when neither is prefilled.
func prefillNote(id joinIdentity, commands []string) string {
	var parts []string
	if id.NameFrom != "" {
		parts = append(parts, "name from "+id.NameFrom)
	}
	hasGitHub := slices.ContainsFunc(commands, func(c string) bool { return strings.Contains(c, `"`+onboard.QIDGitHubAccount+`":`) })
	switch {
	case id.GitHubFrom != "":
		parts = append(parts, "GitHub account from "+id.GitHubFrom)
	case hasGitHub:
		parts = append(parts, "GitHub account from this machine's gh login or ssh keys (the roster has no GitHub login for you)")
	}
	if len(parts) == 0 {
		return ""
	}
	return "Prefilled: " + strings.Join(parts, "; ") + ". Change a value in its line if it is wrong."
}
