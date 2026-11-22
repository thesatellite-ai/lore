// Package agent defines `lore agent start` (layer L5, CLI_REFERENCE 2.11,
// AGENT_IDENTITY 6.1 and 6.4, LORE_TEAM_SPEC F18): register a labelled
// agent on the roster and print its environment, or, from a tool's
// SessionStart hook, record that tool session's details.
//
//	agent.start --label L [--owner @h]
//	    ─▶ label check (a coding agent may start one for its person, owner rule
//	       2026-11-11; an agent with no person behind it is refused by the roster)
//	    ─▶ Roster.ActorRegister(kind agent, L, @h) ─▶ a fresh session id
//	    ─▶ "✓ <id> agent L (owner …) registered in …; LORE_ACTOR=… LORE_SESSION=…"
//	       inside a coding tool (ToolHost): "  <tool> runs each command in a new shell, … start every
//	       lore command of this agent with LORE_ACTOR=agent:L LORE_SESSION=s-…"
//	       inside a tool that names its session (SessionBinder, devco:D-889): the
//	       tool session is bound instead: "  every new shell of this <tool> session
//	       now writes as agent:L …; `lore agent stop` ends it"
//	       "export LORE_ACTOR=agent:L LORE_SESSION=s-…"   (the line `eval "$(… | tail -1)"` runs)
//	agent.stop ─▶ SessionBinder.UnbindToolSession: the tool session is the person's again
//	agent.start --tool T [--session S] [--model M] [--hook-stdin]
//	    ─▶ Host.RecordHook ─▶ never fails, prints nothing on success,
//	       at most one stderr line (6.4: a broken hook never stops the tool)
//
// The roster write goes through ops/infra's Roster (the same path as
// `lore actor register --kind agent`); the session rows are machine-local
// in the binary's lore.db, so the hook mode is the binary's Host
// (PASS3 ONE-PATH, P3-RUNS-6).
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Operation names (`lore agent start`, REST /v1/agents/start).
const (
	Noun    = "agent"
	OpStart = "agent.start"
	OpStop  = "agent.stop"
)

// Environment variable names the printed line sets (identity.EnvActor,
// identity.EnvSession of the resolver).
const (
	EnvActor   = "LORE_ACTOR"
	EnvSession = "LORE_SESSION"
)

// An agent's stable key is agentKeyPrefix + label (F18); a session id is
// sessionPrefix + sessionIDBytes random bytes in hex (v0.1.17 coord_cmd.go).
const (
	agentKeyPrefix = "agent:"
	sessionPrefix  = "s-"
	sessionIDBytes = 4
	successMark    = "✓"
)

// labelPattern is what a label may hold: it becomes part of the stable key
// (agent:<label>), a session's display name and a path segment, so it is
// kept to a safe, case-folded alphabet (v0.1.17 agentLabelPattern).
var labelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// HookIn is the hook mode's input, as Host.RecordHook takes it.
type HookIn struct {
	Tool    string
	Session string
	Model   string
	// Stdin: read the tool's own hook JSON from the process's stdin with
	// the tool's built-in parser (6.4).
	Stdin bool
}

// Host is the binary's part of agent.start.
//
// Invariants: RecordHook never fails and never registers anyone; it fills
// only the empty fields of the detected agent's session row, each with
// source session_hook, and reports a problem as at most one stderr line.
type Host interface {
	// CallerLabel is how the caller is shown as the default owner
	// (@handle, else the display name): for a coding agent, the person it
	// acts for.
	CallerLabel(ctx context.Context) (string, error)
	// RecordHook is the --tool mode.
	RecordHook(ctx context.Context, in HookIn)
}

// ToolHost is the optional Host upgrade naming the coding tool this
// process runs under. Why (devco:D-850): a coding tool runs every command
// in a new shell, so the printed `export` never reaches the agent's next
// command and that command is credited to "<owner> via <tool>"; inside a
// tool agent.start says so and prints the prefix that works there.
type ToolHost interface {
	// CallerTool is the detected coding tool's slug ("" for none).
	CallerTool(ctx context.Context) string
}

// SessionBinder is the optional Host upgrade that binds the caller's
// coding-tool session to the agent agent.start registered. Why
// (devco:D-889, owner 2026-11-22): a tool runs each command in a new
// shell, so the printed export reaches none of them; a binding keyed on
// the tool's own session id makes every later command of that session
// write as the agent until agent.stop (or the tool's session-end hook).
type SessionBinder interface {
	// BindToolSession binds the detected tool session to actor and
	// session. Bound is false (nothing written) outside a tool, or when
	// the tool names no session id.
	BindToolSession(ctx context.Context, actor, session string) (ToolBinding, error)
	// UnbindToolSession removes the detected tool session's binding.
	// Bound is false when there was none.
	UnbindToolSession(ctx context.Context) (ToolBinding, error)
}

// ToolBinding is one coding-tool session's binding to an agent.
type ToolBinding struct {
	// Tool is the detected tool's slug ("" outside a tool).
	Tool string
	// Actor and Session are the bound agent's LORE_ACTOR and LORE_SESSION.
	Actor   string
	Session string
	// Bound: a binding was written (start) or removed (stop).
	Bound bool
}

// Env is what agent.start needs; *wire.App implements it. The --label
// mode also needs the opsinfra.Roster upgrade.
type Env interface {
	// AgentHost is the binary's Host (E_UNSUPPORTED when it has none).
	AgentHost(ctx context.Context) (Host, error)
}

// StartIn is the input of agent.start (v0.1.17 flags plus AID 6.4's).
type StartIn struct {
	Label     string `json:"label,omitempty" flag:"label" desc:"the agent's label (one actor per label), e.g. agent-frontend"`
	Owner     string `json:"owner,omitempty" flag:"owner" desc:"the owning person (@handle); default: you"`
	Tool      string `json:"tool,omitempty" flag:"tool" desc:"session hook: the tool's slug (claude_code, codex, …); records details only"`
	Session   string `json:"session,omitempty" flag:"session" desc:"session hook: the tool's own session id"`
	Model     string `json:"model,omitempty" flag:"model" desc:"session hook: the model the tool reports (never guessed)"`
	HookStdin bool   `json:"hook_stdin,omitempty" flag:"hook-stdin" desc:"session hook: read the tool's hook JSON from stdin"`
}

// StartOut is the output of agent.start.
type StartOut struct {
	// Hook is true for the --tool mode (nothing printed).
	Hook    bool   `json:"hook,omitempty"`
	ID      string `json:"id,omitempty"`
	Label   string `json:"label,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Where   string `json:"where,omitempty"`
	Actor   string `json:"lore_actor,omitempty"`
	Session string `json:"lore_session,omitempty"`
	// Tool is the coding tool the caller runs under ("" for none), for the
	// text only (the JSON output is unchanged).
	Tool string `json:"-"`
	// Bound: this tool session now writes as the agent in every new shell
	// (SessionBinder), for the text only.
	Bound bool `json:"-"`
}

// Text implements op.Texter: v0.1.17's two lines, the last one the export
// a shell evals; nothing for the hook mode.
func (o StartOut) Text() string {
	if o.Hook {
		return ""
	}
	where := "registered"
	if o.Where != "" {
		where = "registered in the roster of " + o.Where
	}
	inTool := ""
	switch {
	case o.Bound:
		// devco:D-889: the binding, not the export, carries the agent to
		// the tool's next commands; the export stays the last line
		inTool = fmt.Sprintf("  every new shell of this %s session now writes as %s (%s=%s) without a prefix; `lore agent stop` ends it\n",
			o.Tool, o.Actor, EnvSession, o.Session)
	case o.Tool != "":
		// devco:D-850: the export below stays the last line (eval scripts
		// take it with tail -1); inside a tool it reaches no later command
		inTool = fmt.Sprintf("  %s runs each command in a new shell, so the export below does not reach its next command: start every lore command of this agent with %s=%s %s=%s (without it the command is yours, via %s)\n",
			o.Tool, EnvActor, o.Actor, EnvSession, o.Session, o.Tool)
	}
	return fmt.Sprintf("%s %s agent %s (owner %s) %s; %s=%s %s=%s\n%sexport %s=%s %s=%s\n",
		successMark, o.ID, o.Label, o.Owner, where, EnvActor, o.Actor, EnvSession, o.Session,
		inTool, EnvActor, o.Actor, EnvSession, o.Session)
}

// Ops returns the agent operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpStart, Summary: "Register an agent actor for this session and print its environment; --tool: record a tool session's details (session hook)",
			Permission: op.PermWrite, Access: op.AccessRead,
			Errors: []errs.Code{errs.InvalidInput, errs.NotAStore, errs.Unsupported, errs.Internal},
			Examples: []op.Example{
				{Title: "start a labelled agent", CLI: `eval "$(lore agent start --label agent-frontend --owner @alice | tail -1)"`, Want: ""},
				{Title: "from the tool's SessionStart hook", CLI: "lore agent start --tool claude_code --hook-stdin", Want: ""},
			},
		}, func(ctx context.Context, in StartIn) (StartOut, error) {
			return Start(ctx, env, in)
		}),
		op.Define(op.Spec{
			Name: OpStop, Summary: "End the agent this coding-tool session was bound to by `lore agent start`: its next commands are yours again",
			Permission: op.PermWrite, Access: op.AccessRead,
			Errors:   []errs.Code{errs.Unsupported, errs.Internal},
			Examples: []op.Example{{Title: "inside the coding tool, when the agent's work is done", CLI: "lore agent stop", Want: ""}},
		}, func(ctx context.Context, in StopIn) (StopOut, error) {
			return Stop(ctx, env, in)
		}),
	}
}

// IsDirectOp reports whether name must run outside the repo engine, like
// the roster verbs (opsinfra.IsRosterOp): the roster may be an org store
// opened store-only outside any repo (FA-AGENT-9).
func IsDirectOp(name string) bool { return name == OpStart || name == OpStop }

// Start is agent.start over env.
func Start(ctx context.Context, env Env, in StartIn) (StartOut, error) {
	h, err := env.AgentHost(ctx)
	if err != nil {
		return StartOut{}, err
	}
	if in.Tool != "" { // v0.1.17: --tool selects the hook mode; --session/--model/--hook-stdin go with it
		if in.Label != "" || in.Owner != "" {
			return StartOut{}, errs.New(errs.InvalidInput, "--label/--owner and --tool are two different runs").
				WithHint("the session hook is `lore agent start --tool <slug> --hook-stdin`")
		}
		if c, ok := engine.CallerOf(ctx); ok && c.AgentFixed && in.HookStdin {
			// a `lore api` client: the server's stdin is not the tool's hook input
			return StartOut{}, errs.New(errs.InvalidInput, "hook_stdin needs the CLI").WithHint("send session and model instead")
		}
		h.RecordHook(ctx, HookIn{Tool: in.Tool, Session: in.Session, Model: in.Model, Stdin: in.HookStdin})
		return StartOut{Hook: true}, nil
	}
	if in.Label == "" {
		return StartOut{}, errs.New(errs.InvalidInput, "--label is required (or --tool for the session hook)")
	}
	if !labelPattern.MatchString(in.Label) {
		return StartOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("agent label %q: use lower-case letters, digits, '.', '_' or '-' (max 63, starting with a letter or digit)", in.Label))
	}
	// A coding agent may start an agent for its person (owner rule
	// 2026-11-11): the roster registers it as that person, and refuses
	// only an agent with no person behind it (wire rosterCaller).
	r, ok := upgrade.As[opsinfra.Roster](env)
	if !ok {
		return StartOut{}, errs.New(errs.Unsupported, "this binary has no roster")
	}
	owner := in.Owner
	if owner == "" {
		if owner, err = h.CallerLabel(ctx); err != nil {
			return StartOut{}, err
		}
	}
	reg, err := r.ActorRegister(ctx, opsinfra.ActorRegisterIn{Kind: opsinfra.RegisterKindAgent, Label: in.Label, Owner: in.Owner})
	if err != nil {
		return StartOut{}, err
	}
	session, err := newSessionID()
	if err != nil {
		return StartOut{}, errs.Wrap(errs.Internal, "make a session id", err)
	}
	out := StartOut{ID: reg.ID, Label: in.Label, Owner: owner, Where: reg.Where, Actor: agentKeyPrefix + in.Label, Session: session}
	if b, ok := h.(SessionBinder); ok {
		tb, err := b.BindToolSession(ctx, out.Actor, out.Session)
		if err != nil {
			return StartOut{}, err
		}
		out.Tool, out.Bound = tb.Tool, tb.Bound
	} else if th, ok := h.(ToolHost); ok {
		out.Tool = th.CallerTool(ctx)
	}
	return out, nil
}

// StopIn is the input of agent.stop: none, the tool session is detected.
type StopIn struct{}

// StopOut is the output of agent.stop.
type StopOut struct {
	// Stopped: this tool session was bound and no longer is.
	Stopped bool   `json:"stopped"`
	Tool    string `json:"tool,omitempty"`
	Actor   string `json:"lore_actor,omitempty"`
	Session string `json:"lore_session,omitempty"`
}

// Text implements op.Texter.
func (o StopOut) Text() string {
	switch {
	case o.Stopped:
		return fmt.Sprintf("%s this %s session no longer writes as %s (%s=%s); its next commands are yours, via %s\n",
			successMark, o.Tool, o.Actor, EnvSession, o.Session, o.Tool)
	case o.Tool != "":
		return "nothing to stop: this " + o.Tool + " session is not bound to an agent (`lore agent start --label <label>` binds it)\n"
	}
	return "nothing to stop: no coding-tool session found here (outside a tool, unset LORE_ACTOR and LORE_SESSION in your shell)\n"
}

// Stop is agent.stop over env: unbind the caller's tool session.
func Stop(ctx context.Context, env Env, _ StopIn) (StopOut, error) {
	h, err := env.AgentHost(ctx)
	if err != nil {
		return StopOut{}, err
	}
	b, ok := h.(SessionBinder)
	if !ok {
		return StopOut{}, errs.New(errs.Unsupported, "this binary cannot bind tool sessions")
	}
	tb, err := b.UnbindToolSession(ctx)
	if err != nil {
		return StopOut{}, err
	}
	return StopOut{Stopped: tb.Bound, Tool: tb.Tool, Actor: tb.Actor, Session: tb.Session}, nil
}

// newSessionID is a fresh LORE_SESSION value.
func newSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return sessionPrefix + hex.EncodeToString(b), nil
}
