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
	"fmt"
	"os"
	"sort"
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

// Whoami implements opswhoami.Host. A resolution or identity error is the
// operation's error (P3-ORG-G-3).
func (w *standaloneWhoami) Whoami(ctx context.Context, in opswhoami.ShowIn) (opswhoami.Out, error) {
	s, err := w.res.Situation(ctx)
	if err != nil {
		return opswhoami.Out{}, err
	}
	if s.ResolveErr != nil {
		return opswhoami.Out{}, orgcfg.ResolveError(s.ResolveErr)
	}
	id, err := resolveIdentity(ctx, w.opts)
	if err != nil {
		return opswhoami.Out{}, err
	}
	red := newWhoamiRedactor(s, in.Share)
	det, derr := DetectAgent()
	var agent *agentdetect.Result
	if derr == nil {
		agent = &det
		w.hookModel(ctx, agent)
	}
	m, personal := whoamiMachine(w.opts.Home), whoamiPersonal(w.opts.Home)
	return opswhoami.Out{Data: whoamiData(s, id, red, agent, m, personal), Lines: whoamiLines(s, id, in.Explain, red, agent, m, personal)}, nil
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

// SetMachineLabel implements opswhoami.Host: not wired in cmd/lore yet
// (it runs machine.label through the registry, which this host does not
// hold).
func (w *standaloneWhoami) SetMachineLabel(context.Context, string) (opswhoami.MachineLabel, []string, error) {
	return opswhoami.MachineLabel{}, nil, errs.New(errs.Unsupported, "lore whoami --machine-label is not wired in this binary yet").
		WithHint("run `lore machine label <label>`")
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

// whoamiRedactor masks every configured store url (each form) and each
// link's "store folder/" when on; the repo's own remotes and patterns stay.
type whoamiRedactor struct {
	on      bool
	secrets []string
}

// newWhoamiRedactor decides redaction: quietOutput unless --share on a
// terminal. Stores of every entry are secrets (fail closed).
func newWhoamiRedactor(s *orgcfg.Situation, share bool) whoamiRedactor {
	on := quietOutput()
	if share && isTerminal(os.Stdout) {
		on = false
	}
	r := whoamiRedactor{on: on}
	if !on {
		return r
	}
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			r.secrets = append(r.secrets, v)
		}
	}
	addStore := func(raw string) {
		add(raw)
		add(strings.TrimSuffix(raw, ".git"))
		if n, err := repourl.Normalize(raw); err == nil {
			add(n)
			if host, rest, ok := strings.Cut(n, "/"); ok {
				add(host + ":" + rest) // scp-like form, as git prints it
			}
		}
	}
	if s.Config != nil {
		for i := range s.Config.Orgs {
			for _, st := range s.Config.Orgs[i].Stores {
				addStore(st.URL)
			}
		}
	}
	for _, l := range s.Result.Links {
		add(l.StoreURL + " " + l.Folder + "/")
		addStore(l.StoreURL)
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

func (r whoamiRedactor) str(v string) string {
	if !r.on {
		return v
	}
	for _, sec := range r.secrets {
		v = strings.ReplaceAll(v, sec, redactedText)
	}
	return v
}

func (r whoamiRedactor) folder(f string) string {
	if !r.on || f == "" {
		return f
	}
	return redactedText
}

// whoamiPersonal is the personal store `me` as one place (G-DECIDE-7):
// personal.store's url, else the v0.1.17 local folder (~/.lore/personal,
// `store: local` or present on disk), else "" (not set). Read only: whoami
// never makes the local store's marker.
func whoamiPersonal(home string) string {
	mc, err := readMachineConfig(home)
	if err != nil {
		return ""
	}
	if u := mc.personalURL(); u != "" {
		return u
	}
	return mc.localPersonalV017(home)
}

// personalNotSet is the personal row when no personal store is set.
const personalNotSet = "not set"

// whoamiActorID is the act_ id of a stable key ("" when none).
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
		Identity: opswhoami.Identity{StableKey: id.StableKey, Name: id.DisplayName, Kind: id.Kind, ActorID: whoamiActorID(id.StableKey), Source: string(id.Step), Session: os.Getenv(identity.EnvSession)}}
	for _, warn := range whoamiWarnings(s, id) {
		d.Warnings = append(d.Warnings, red.str(warn))
	}
	if r.Org != nil {
		for _, st := range r.Org.Stores {
			u, f := st.URL, st.OrgFolder
			if red.on {
				u, f = redactedText, red.folder(f)
			}
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
func whoamiLines(s *orgcfg.Situation, id identity.Resolved, explain bool, red whoamiRedactor, agent *agentdetect.Result, m, personal string) []string {
	var out []string
	row := func(label, value string) { out = append(out, fmt.Sprintf("%-*s %s", whoamiLabelWidth, label, value)) }
	r := s.Result
	if r.Org == nil {
		v := "none  (matched by: " + red.str(r.MatchedBy) + ")"
		if h := orgUseHint(s.Config); h != "" {
			v += "   hint: " + h
		}
		row("org", v)
	} else {
		row("org", r.Org.Name+"  (matched by: "+red.str(r.MatchedBy)+")")
	}
	v := fmt.Sprintf("%s <%s>   %s", id.DisplayName, id.StableKey, whoamiActorID(id.StableKey))
	if id.Handle != "" {
		v += "  @" + id.Handle
	}
	if ses := os.Getenv(identity.EnvSession); ses != "" {
		v += "  session " + ses
	}
	row("identity", v+"  (from: "+string(id.Step)+")")
	switch r.Role {
	case orgresolve.RoleClient:
		row("repo", "client repo  (guards on)")
	case orgresolve.RoleOwn:
		row("repo", "own repo  (no client guards)")
	default:
		row("repo", "no org  (fallback identity)")
	}
	if m == "" {
		row("machine", "(no OS machine id readable here)")
	} else {
		row("machine", m)
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
