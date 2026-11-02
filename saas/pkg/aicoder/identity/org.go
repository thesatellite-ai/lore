package identity

// org.go — identity when an org entry resolves (LORE_TEAM_SPEC F18, C17,
// TEAM-029).
//
// The 8-step chain in resolve.go is the FALLBACK, used only when no org
// applies. When an org resolves, its entry's identity is required and the
// fallback chain (identity file, git email, OS user, machine id, salts) is
// skipped: in a northwind repo you are human:alice@northwind.example on
// every machine, never whatever `git config user.email` happens to say.
//
// LORE_ACTOR (and --actor) is accepted only for agent actors — one actor per
// agent session — and, once the org roster exists, only for agents on it. A
// human key in LORE_ACTOR is ignored with a warning, so an agent that
// inherited a person's LORE_ACTOR cannot approve or write as that person.

import (
	"errors"
	"fmt"
	"strings"

	"saas/pkg/aicoder/lorecfg"
)

// Environment variables read here. Named once so docs and tests agree.
// ds:def id=team-env-actor-53pv3fuc owner=@khanakia stability=stable desc="LORE_ACTOR and LORE_SESSION"
const (
	// EnvActor names the acting agent for this session (agent:<label>).
	EnvActor = "LORE_ACTOR"
	// EnvSession is the agent session id set by `lore agent start`.
	EnvSession = "LORE_SESSION"
)

// Actor kinds as produced by kindFromKey (dbent/schema/actor.go).
const (
	KindHuman = "human"
	KindAgent = "agent"
)

// StepOrgConfig: the identity came from the resolved org entry in
// ~/.lore/config.yaml. Appended after the fallback chain's steps so their
// numbers do not change.
const StepOrgConfig Step = StepEphemeralSalt + 1

// stepOrgConfigName is StepOrgConfig's display name.
const stepOrgConfigName = "org entry (config.yaml)"

// Errors for the org path; the CLI maps them to codes and hints.
var (
	// ErrOrgIdentityMissing: the resolved entry has no identity.key.
	ErrOrgIdentityMissing = errors.New("identity: the resolved org entry has no identity")
	// ErrAgentNotOnRoster: LORE_ACTOR names an agent the roster does not list.
	ErrAgentNotOnRoster = errors.New("identity: agent is not on the org roster")
	// ErrAgentRequired: an agent-only command ran without an agent actor.
	ErrAgentRequired = errors.New("identity: this command needs an agent actor")
)

// Getenv reads an environment variable; os.Getenv in production, a map in
// tests (the Env seam, §9.1).
type Getenv func(key string) string

// Roster answers "is this agent registered in the org?" (F18). The roster
// lives in the store's org folder and arrives with stores; until then
// NoRoster reports that it did not check.
type Roster interface {
	// AgentRegistered returns (registered, checked). checked=false means the
	// roster could not be consulted; the agent is then accepted and the
	// caller is told so in Resolved.Notes.
	AgentRegistered(stableKey string) (registered, checked bool)
}

// NoRoster is the Roster before stores exist: it never checks.
type NoRoster struct{}

// AgentRegistered implements Roster.
func (NoRoster) AgentRegistered(string) (bool, bool) { return false, false }

// OrgInputs is everything ResolveForOrg reads.
type OrgInputs struct {
	// Org is the resolved entry (required).
	Org *lorecfg.Org
	// FlagActor is --actor, when a command offers it.
	FlagActor string
	// Getenv reads LORE_ACTOR and LORE_SESSION.
	Getenv Getenv
	// Roster checks agents; nil means NoRoster.
	Roster Roster
}

// ResolveForOrg returns the identity inside a resolved org. Notes carries
// one-line warnings worth printing (an ignored human LORE_ACTOR, an agent
// accepted without a roster check).
func ResolveForOrg(in OrgInputs) (Resolved, error) {
	if in.Org == nil {
		return Resolved{}, errors.New("identity: ResolveForOrg needs an org")
	}
	getenv := in.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	roster := in.Roster
	if roster == nil {
		roster = NoRoster{}
	}
	var notes []string

	actor, step, src := strings.TrimSpace(in.FlagActor), StepFlag, "--actor"
	if actor == "" {
		actor, step, src = strings.TrimSpace(getenv(EnvActor)), StepEnv, EnvActor
	}
	if actor != "" {
		if kindFromKey(actor) == KindAgent && strings.HasPrefix(actor, KindAgent+":") && len(actor) > len(KindAgent)+1 {
			registered, checked := roster.AgentRegistered(actor)
			if checked && !registered {
				return Resolved{}, fmt.Errorf("%w: %s=%s is not on %s's roster (register it with `lore agent start --label …`)",
					ErrAgentNotOnRoster, src, actor, in.Org.Name)
			}
			if !checked {
				notes = append(notes, fmt.Sprintf("%s=%s accepted without a roster check (the %s roster is not read yet)", src, actor, in.Org.Name))
			}
			return Resolved{
				StableKey:   actor,
				DisplayName: extractDisplayName(actor),
				Kind:        KindAgent,
				Step:        step,
				Org:         in.Org.Name,
				Session:     strings.TrimSpace(getenv(EnvSession)),
				Notes:       notes,
			}, nil
		}
		notes = append(notes, fmt.Sprintf("%s=%s ignored: in %s only agent:<label> actors are accepted; you act as the org identity", src, actor, in.Org.Name))
	}

	key := strings.TrimSpace(in.Org.Identity.Key)
	if key == "" {
		return Resolved{}, fmt.Errorf("%w: add identity.key under orgs[%s] in ~/.lore/config.yaml, or run `lore identity set --org %s --key human:<your email in %s>`",
			ErrOrgIdentityMissing, in.Org.Name, in.Org.Name, in.Org.Name)
	}
	if err := lorecfg.ValidateHumanKey(key); err != nil {
		return Resolved{}, fmt.Errorf("identity: orgs[%s].identity.key %q: %w", in.Org.Name, key, err)
	}
	display := strings.TrimSpace(in.Org.Identity.Name)
	if display == "" {
		display = extractDisplayName(key)
	}
	return Resolved{
		StableKey:   key,
		DisplayName: display,
		Kind:        KindHuman,
		Step:        StepOrgConfig,
		Org:         in.Org.Name,
		Handle:      in.Org.Identity.Handle,
		Notes:       notes,
	}, nil
}

// RequireAgent refuses r unless it is an agent actor (agent commands such as
// claims need one actor per session, F18).
func RequireAgent(r Resolved) error {
	if r.Kind == KindAgent {
		return nil
	}
	return fmt.Errorf("%w: set %s=agent:<label> for this session (you are %s)", ErrAgentRequired, EnvActor, r.StableKey)
}
