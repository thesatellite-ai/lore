package wire

// whoami_standalone.go — the `lore whoami` host of cmd/lore (ops/whoami.Host,
// P3-CMDLORE-4): the v0.1.17 lines and JSON of saas/cmd/cli/whoami_cmd.go,
// built from the process's OrgResolution (adapters/orgcfg.Situation) and the
// identity chain the core writes with (resolveIdentity), so whoami shows
// exactly the org and person a write would use. The old binary keeps its
// own host (Options.Whoami); StandaloneOptionsOrg installs this one.
//
//	Whoami ─▶ Situation (F10) ─▶ refusal? error (exit kept) ─▶ identity ─▶
//	          redactor (no TTY or CI: store urls and folders hidden, §6.5)
//	          ─▶ lines + data

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
)

// whoami output words (v0.1.17's).
const (
	whoamiLabelWidth = 10
	redactedText     = "<redacted>"
	envHumanOn       = "1"
)

// ciMarkers are the CI variables that turn redaction on (v0.1.17
// stores_redact.go ciMarkers, Q-stores-3).
// ds:def id=team-ci-markers-ec4tqnjs owner=@khanakia stability=stable desc="CI variables that turn on redacted output"
var ciMarkers = []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD", "BITBUCKET_BUILD_NUMBER"}

// standaloneWhoami implements opswhoami.Host for cmd/lore.
type standaloneWhoami struct {
	opts Options
	res  *OrgResolution
	// app hands out the App New bound (nil = no session lookup): the
	// session hook's model lives on its machine-store session rows.
	app boundAppSource
}

var _ opswhoami.Host = (*standaloneWhoami)(nil)

// Whoami implements opswhoami.Host. whoami is the diagnostic for a
// resolution that fails, so an org refusal (E_ORG_AMBIGUOUS,
// E_ORG_DISAGREES, …) or an identity error still prints every line it can
// (org refused, identity, machine, agent, project, remotes, links,
// personal) and the --json data with "error", then fails with that error
// through Out.Fail (v0.1.17 printed, then exited 17; P3-ORG-G-3). Only a
// situation that cannot be gathered at all (an unreadable config) is a
// plain error.
func (w *standaloneWhoami) Whoami(ctx context.Context, in opswhoami.ShowIn) (opswhoami.Out, error) {
	s, err := w.res.Situation(ctx)
	if err != nil {
		return opswhoami.Out{}, err
	}
	var id identity.Resolved
	var idErr, fail error
	if s.ResolveErr != nil {
		fail = orgcfg.ResolveError(s.ResolveErr)
	} else if id, idErr = resolveIdentity(ctx, w.opts); idErr != nil {
		fail = idErr
	}
	if fail == nil && len(id.Aliases) == 0 {
		// an email change made on another machine: the stores name the
		// actor this key moved to (SOURCE_OF_TRUTH), and whoami's act_ id
		// is the one this machine writes as
		id = w.movedIdentity(ctx, id)
	}
	red := whoamiRedactor{}
	det, derr := DetectAgent()
	var agent *agentdetect.Result
	if derr == nil {
		agent = &det
		w.hookModel(ctx, agent)
	}
	m, personal := whoamiMachine(w.opts.Home), whoamiPersonal(ctx, w.opts.Home, optsProjectDir(w.opts))
	d := whoamiData(s, id, red, agent, m, personal)
	if fail != nil {
		d.Error = red.str(errs.EnvelopeOf(fail).Error.Message) // the code is in the exit status and the CLI's error line
	}
	lines := whoamiLines(s, id, idErr, in.Explain, red, agent, m, personal)
	if fail == nil {
		if l := w.identityContact(ctx); l != "" {
			d.IdentityContact = red.str(strings.TrimPrefix(l, identityContactPrefix))
			lines = append(lines, red.str(l))
		}
	}
	var serr error
	d.Setup, lines, serr = whoamiSetup(ctx, w.opts, in.Explain, lines) // ONBOARDING 3.3
	if fail == nil && serr != nil {
		fail = serr
		d.Error = red.str(errs.EnvelopeOf(serr).Error.Message)
	}
	return opswhoami.Out{Data: d, Lines: lines, Fail: fail}, nil
}

// identityContact is the TEAM_PROFILES 6.3 line "identity contact:
// mira.patel@gmail.com is an identity contact of Mira Patel (act_…)", ""
// when the key is no other actor's identity contact. Best effort: whoami
// is the diagnostic, so a store that cannot be opened leaves the line out
// rather than failing the command.
func (w *standaloneWhoami) identityContact(ctx context.Context) string {
	if w.app == nil {
		return ""
	}
	app := w.app.boundApp()
	if app == nil {
		return ""
	}
	l, err := app.IdentityContactLine(ctx)
	if err != nil {
		return ""
	}
	return l
}

// movedIdentity is id after an email change made on another machine, as
// open settled it (identity_match.go): a person gets the key aliases the
// actor was found under; a detected coding agent the identity derived from
// its owner's kept id (movedAgentOwner), so whoami's act_ id is the one
// this machine writes as. id itself when nothing moved or the stores
// cannot be opened (whoami is the diagnostic: best effort).
func (w *standaloneWhoami) movedIdentity(ctx context.Context, id identity.Resolved) identity.Resolved {
	if w.app == nil {
		return id
	}
	app := w.app.boundApp()
	if app == nil {
		return id
	}
	st, err := app.state(ctx)
	if err != nil {
		return id
	}
	if id.Step == identity.StepDetected && st.who.Step == identity.StepDetected && st.who.Owner != nil && len(st.who.Owner.Aliases) > 0 {
		return st.who
	}
	if id.Kind == identity.KindHuman {
		id.Aliases = slices.Clone(st.who.Aliases)
	}
	return id
}

// hookModel fills a model the environment did not expose from the one
// `lore agent start` (the session hook) recorded on this session's row,
// with source session_hook (OV11, AGENT_IDENTITY 6.3; v0.1.17 whoami).
func (w *standaloneWhoami) hookModel(ctx context.Context, a *agentdetect.Result) {
	if a.Model != "" || a.Tool == "" || a.SessionID == "" || w.app == nil {
		return
	}
	app := w.app.boundApp()
	if app == nil {
		return
	}
	if m := app.HookReportedModel(ctx, a.Tool, a.SessionID); m != "" {
		a.Model = m
		a.Sources = withHookModel(a.Sources)
	}
}

// SetMachineLabel implements opswhoami.Host: `whoami --machine-label`
// runs machine.label through the bound App's registry, exactly as
// `lore machine label <label>` does (same target store, refusals and
// audit), and hands back its result (P3-CMDLORE-4; v0.1.17
// setMachineLabel).
func (w *standaloneWhoami) SetMachineLabel(ctx context.Context, label string) (opswhoami.MachineLabel, []string, error) {
	var app *App
	if w.app != nil {
		app = w.app.boundApp()
	}
	if app == nil {
		return opswhoami.MachineLabel{}, nil, errs.New(errs.Internal, "whoami: no App to set the machine label through")
	}
	e, ok := app.Registry().Lookup(opsinfra.OpMachineLabel)
	if !ok {
		return opswhoami.MachineLabel{}, nil, errs.New(errs.Internal, opsinfra.OpMachineLabel+" is not registered")
	}
	in, err := json.Marshal(opsinfra.MachineLabelIn{Label: label})
	if err != nil {
		return opswhoami.MachineLabel{}, nil, errs.Wrap(errs.Internal, "whoami: encode the machine label", err)
	}
	res, err := app.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{Interactive: isTerminal(os.Stdin)})
	if err != nil {
		return opswhoami.MachineLabel{}, nil, err
	}
	raw, err := res.JSON()
	if err != nil {
		return opswhoami.MachineLabel{}, nil, errs.Wrap(errs.Internal, "whoami: encode the machine.label result", err)
	}
	var out opsinfra.MachineLabelOut
	if err := json.Unmarshal(raw, &out); err != nil {
		return opswhoami.MachineLabel{}, nil, errs.Wrap(errs.Internal, "whoami: decode the machine.label result", err)
	}
	return opswhoami.MachineLabel{ID: out.ID, Label: out.Label, Store: out.Store, Changed: out.Changed},
		strings.Split(strings.TrimRight(out.Text(), "\n"), "\n"), nil
}

// whoamiMachine is this machine's id ("" when none can be had).
func whoamiMachine(home string) string {
	m, _, err := machine.Load(home, os.Getenv)
	if err != nil || m.IsZero() {
		return ""
	}
	return m.String()
}

// quietOutput: no TTY on stdout, or a CI marker set (§6.5).
func quietOutput() bool {
	for _, k := range ciMarkers {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return !isTerminal(os.Stdout)
}

// whoamiRedactor masks only credentials (repourl.MaskText): store urls,
// org folders and link folders are printed as they are, on a terminal, in
// a pipe and in CI alike (a store's plain address is no secret).
type whoamiRedactor struct{}

func (whoamiRedactor) str(v string) string { return repourl.MaskText(v) }

func (whoamiRedactor) folder(f string) string { return f }

// whoamiPersonal is the personal store `me` as one place (G-DECIDE-7) as
// the setup in effect for dir sees it (ONBOARDING 3.4, the same answer
// `--to me` and `lore store list` use): the setup's own url, nothing for
// personal_store: none, else personal.store's url, else the v0.1.17 local
// folder (~/.lore/personal, `store: local` or present on disk), else ""
// (not set). Reading only personal.store said "not set" on every machine
// whose setup holds its own store (journey solo/day1). Read only: whoami
// never makes the local store's marker.
func whoamiPersonal(ctx context.Context, home, dir string) string {
	mc, err := readMachineConfig(home)
	if err != nil {
		return ""
	}
	if _, isNone, _ := setupPersonal(ctx, home, dir); isNone {
		return ""
	}
	if u := effectivePersonalURL(ctx, home, dir, mc); u != "" {
		return u
	}
	return mc.localPersonalV017(home)
}

// personalNotSet is the personal row when no personal store is set.
const personalNotSet = "not set"

// whoamiActorID is the act_ id of an actor key (Resolved.ActorKey: the
// first alias after an email change; "" when none).
func whoamiActorID(key string) string {
	if key == "" {
		return ""
	}
	id, err := identity.ActorID(key)
	if err != nil {
		return ""
	}
	return id.String()
}

// whoamiWarnings are the resolution's, the identity's and the remotes'.
func whoamiWarnings(s *orgcfg.Situation, id identity.Resolved) []string {
	w := append([]string{}, s.Result.Warnings...)
	w = append(w, id.Notes...)
	w = append(w, s.RemoteNotes...)
	if s.CommittedErr != nil {
		w = append(w, fmt.Sprintf(".lore/org.yaml ignored: %v", s.CommittedErr))
	}
	return w
}

// whoamiData is the --json data (v0.1.17 whoamiJSON keys).
func whoamiData(s *orgcfg.Situation, id identity.Resolved, red whoamiRedactor, agent *agentdetect.Result, m, personal string) opswhoami.Data {
	r := s.Result
	d := opswhoami.Data{Org: r.OrgName(), MatchedBy: red.str(r.MatchedBy), Rule: int(r.Step), RepoRole: string(r.Role),
		ProjectID: s.ProjectID, Remotes: nonNilStrs(s.Remotes), Personal: personal,
		LinkStatus: r.LinkStatus.String(), Warnings: []string{}, Stores: []opswhoami.Store{}, Trace: []opswhoami.Rule{}, Machine: m,
		Identity: opswhoami.Identity{StableKey: id.StableKey, Name: id.DisplayName, Kind: id.Kind, ActorID: whoamiActorID(id.ActorKey()), Source: id.Source(), Session: os.Getenv(identity.EnvSession)}}
	for _, warn := range whoamiWarnings(s, id) {
		d.Warnings = append(d.Warnings, red.str(warn))
	}
	if r.Org != nil {
		for _, st := range r.Org.Stores {
			u, f := repourl.Mask(st.URL), red.folder(st.OrgFolder)
			d.Stores = append(d.Stores, opswhoami.Store{URL: u, Branch: st.Branch, OrgFolder: f})
		}
	}
	for _, t := range r.Trace {
		d.Trace = append(d.Trace, opswhoami.Rule{Step: int(t.Step), Outcome: string(t.Outcome), Reason: red.str(t.Reason)})
	}
	if agent != nil {
		a := opswhoami.Agent{Tool: agent.Tool, ToolSession: redactSession(agent.SessionID), Version: agent.Version, Model: agent.Model,
			Signal: agent.Signal, Outer: agent.Outer, Flags: agent.Flags, Sources: agent.Sources, DeclaredHuman: os.Getenv(identity.EnvHuman) == envHumanOn}
		if agent.CI != nil {
			a.CI = agent.CI.ActorKey()
		}
		d.Agent = a
	}
	return d
}

// redactSession hides a tool session id without a terminal or in CI.
func redactSession(id string) string {
	if id == "" || !quietOutput() {
		return id
	}
	return redactedText
}

func nonNilStrs(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// orgUseHint tells a user in a repo no rule matched how to pick an org
// (staging entries left out: chosen only on purpose).
func orgUseHint(cfg *orgconf.Config) string {
	if cfg == nil {
		return ""
	}
	var names []string
	for i := range cfg.Orgs {
		if !orgresolve.IsStaging(&cfg.Orgs[i], nil) {
			names = append(names, cfg.Orgs[i].Name)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "lore org use " + names[0]
	default:
		return "lore org use <entry> (" + strings.Join(names, ", ") + ")"
	}
}

// whoamiLines are the human lines (v0.1.17 printWhoami without colour).
// idErr is the identity error ("missing" line); a refused org
// (s.ResolveErr) prints "refused" and no identity or repo role.
func whoamiLines(s *orgcfg.Situation, id identity.Resolved, idErr error, explain bool, red whoamiRedactor, agent *agentdetect.Result, m, personal string) []string {
	var out []string
	row := func(label, value string) { out = append(out, fmt.Sprintf("%-*s %s", whoamiLabelWidth, label, value)) }
	r := s.Result
	switch {
	case s.ResolveErr != nil:
		row("org", "refused  ("+red.str(s.ResolveErr.Error())+")")
	case r.Org == nil:
		v := "none  (matched by: " + red.str(r.MatchedBy) + ")"
		if h := orgUseHint(s.Config); h != "" {
			v += "   hint: " + h
		}
		row("org", v)
	default:
		row("org", r.Org.Name+"  (matched by: "+red.str(r.MatchedBy)+")")
	}
	switch {
	case s.ResolveErr != nil:
		row("identity", "(not resolved: the org is refused above)")
	case idErr != nil:
		row("identity", "missing  "+red.str(idErr.Error()))
	default:
		v := fmt.Sprintf("%s <%s>   %s", id.DisplayName, id.StableKey, whoamiActorID(id.ActorKey()))
		if id.Handle != "" {
			v += "  @" + id.Handle
		}
		if ses := os.Getenv(identity.EnvSession); ses != "" {
			v += "  session " + ses
		}
		row("identity", v+"  (from: "+id.Source()+")")
	}
	if s.ResolveErr == nil {
		switch r.Role {
		case orgresolve.RoleClient:
			row("repo", "client repo  (guards on)")
		case orgresolve.RoleOwn:
			row("repo", "own repo  (no client guards)")
		default:
			row("repo", "no org  (fallback identity)")
		}
	}
	if m == "" {
		row("machine", "(no OS machine id readable here)")
	} else {
		// PROV4 / AGENT_IDENTITY 6.8: the short form (mch_ + 8 hex, like a
		// git short hash), as the design's whoami sample; --json, stored
		// data and `lore machine show` keep the full id.
		short := m
		if id, err := ids.ParseMachine(m); err == nil {
			short = id.Short()
		}
		row("machine", short)
	}
	out = append(out, agentLines(agent)...)
	project := s.ProjectID
	if project == "" {
		project = "(no .lore/data/_meta.json here)"
	}
	row("project", project)
	if len(s.Remotes) > 0 {
		row("remotes", strings.Join(s.Remotes, ", "))
	} else {
		row("remotes", "(none)")
	}
	if r.Org != nil {
		if len(r.Org.Stores) == 0 {
			row("stores", "(none listed for "+r.Org.Name+")")
		}
		for i, st := range r.Org.Stores {
			label := ""
			if i == 0 {
				label = "stores"
			}
			sv := red.str(st.URL) + " on branch " + st.EffectiveBranch()
			if st.OrgFolder != "" {
				sv += "  (org folder " + red.folder(st.OrgFolder) + ")"
			}
			row(label, sv)
		}
		if r.Org.OrgFolderStore() == nil {
			row("global", "(no store sets org_folder: no roster or company rules)")
		}
		for _, l := range r.Links {
			lv := red.str(l.StoreURL) + " " + red.folder(l.Folder) + "/"
			if l.Default {
				lv += " (default link)"
			}
			row("internal", lv)
		}
	}
	row("links", r.LinkStatus.String()+"  "+red.str(r.LinkDetail))
	if personal == "" {
		row("personal", personalNotSet)
	} else {
		row("personal", attach.ReservedMe+" -> "+personal)
	}
	if !s.HasConfig {
		row("config", s.ConfigPath+" does not exist (no orgs configured)")
	}
	for _, warn := range whoamiWarnings(s, id) {
		out = append(out, "warning: "+red.str(warn))
	}
	if explain {
		out = append(out, "", "org resolution (first rule that answers wins):")
		for _, t := range r.Trace {
			mark := "·"
			switch t.Outcome {
			case orgresolve.OutcomeHit:
				mark = "✓"
			case orgresolve.OutcomeMiss:
				mark = "✗"
			case orgresolve.OutcomeNotReached:
			}
			out = append(out, fmt.Sprintf("  %s %d. %-16s %s", mark, int(t.Step), t.Step.String(), red.str(t.Reason)))
		}
	}
	return out
}

// agentLines are the tool, session, version and model lines (R11).
func agentLines(a *agentdetect.Result) []string {
	if a == nil {
		return nil
	}
	row := func(label, value string) string { return fmt.Sprintf("%-*s %s", whoamiLabelWidth, label, value) }
	if a.Tool == "" {
		v := "none  (no coding agent detected)"
		if a.CI != nil {
			v = a.CI.ActorKey() + "  (CI actor)"
		}
		return []string{row("agent", v)}
	}
	v := a.Tool + "  (from: " + a.Signal + ")"
	if os.Getenv(identity.EnvHuman) == envHumanOn {
		v += "  LORE_HUMAN=1: writes stay yours"
	}
	out := []string{row("agent", v)}
	if a.SessionID != "" {
		out = append(out, row("session", redactSession(a.SessionID)))
	}
	if a.Version != "" {
		out = append(out, row("tool version", a.Version+"  (from: "+a.Sources["tool_version"]+")"))
	}
	model := "(not exposed by the tool)"
	switch {
	case a.Model != "" && a.Sources[sourceFieldModel] == SessionSourceHook:
		model = a.Model + "  (from: session hook)"
	case a.Model != "":
		model = a.Model + "  (from config)"
	}
	return append(out, row("model", model))
}
