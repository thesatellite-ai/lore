package wire

// personcredit.go — the admin's own org-creation steps are the person's
// (devco D-424; agents act for a person, C35/U-1). A person who runs
// `lore setup apply` or `lore org create` through a coding agent used to
// get "<person> via <tool>" (agent:auto/<tool>/…) registered on the new
// org's roster, its first rows stamped with it and Lore-Agent trailers on
// the store's commits, before any agent label was chosen. These steps now
// resolve the person (Options.PersonOnly, as `lore api` does) and say so:
//
//	setup apply identity / personal_store / join / team, org create
//	   ─▶ detection finds a tool ─▶ one line: what is credited to whom
//	   ─▶ the App reopens person-only: rows, actor copies and commits
//	      name the person; no agent actor
//	   ─▶ the detected tool stays on ctx (creditedAgent) for this
//	      machine's agents.may_manage_people check (agentMayCreateTeam)
//
// Every other command run by the agent still writes as the agent.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// creditedAgentKey is the context key of the agent a person-credited step
// ran through.
type creditedAgentKey struct{}

// creditedAgent is the agent a person-credited step ran through ("" for
// none): a policy about agents still applies to it.
func creditedAgent(ctx context.Context) string {
	s, ok := ctx.Value(creditedAgentKey{}).(string)
	if !ok {
		return ""
	}
	return s
}

// creditPerson makes the rest of a's writes the person's when agent
// detection finds a coding tool: a reopens resolving the person only, and
// say gets one line naming the tool and person (the person's name from the
// step, else the identity's). It returns ctx carrying the detected agent's
// key for agent policies. LORE_ACTOR=agent:<label> (an admin's wrapper)
// is credited the same way (devco D-440). Nothing changes when the person
// runs lore themself, --actor named the actor, or a is person-only
// already.
func (a *App) creditPerson(ctx context.Context, verb, person string, say func(string)) context.Context {
	if a.personOnly() {
		return ctx
	}
	who, err := resolveIdentity(ctx, a.opts)
	if err != nil || who.Agent == "" {
		return ctx
	}
	if a.opts.FlagActor != "" {
		// devco D-440: --actor names the actor on purpose; it is never
		// swapped for a person (an ownerless one is refused up front,
		// refuseOwnerlessAgent)
		return ctx
	}
	tool := who.Label
	if tool == "" {
		tool = who.Agent
	}
	// devco D-440: LORE_ACTOR=agent:<label> is credited like a detected
	// tool; the person-only reopen resolves the person behind it
	// (personEnv), whose name the identity gives once reopened
	a.reopenAsPerson(false)
	if strings.TrimSpace(person) == "" {
		if p, perr := resolveIdentity(ctx, a.opts); perr == nil {
			person = p.Person().DisplayName
		}
	}
	if say != nil {
		say(verb + ": run through " + tool + "; this step's rows and store commits are credited to " + person +
			" (no agent actor). An agent of yours writes as itself after `lore agent start --label <label>`")
	}
	return context.WithValue(ctx, creditedAgentKey{}, who.Agent)
}

// personOnly reports whether a resolves the person only.
func (a *App) personOnly() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.opts.PersonOnly
}

// detectedTool is the coding tool agent detection finds for opts'
// identity ("" when none does, or LORE_ACTOR decided).
func detectedTool(ctx context.Context, opts Options) string {
	who, err := resolveIdentity(ctx, opts)
	if err != nil || who.Agent == "" {
		return ""
	}
	if who.Label != "" {
		return who.Label
	}
	return who.Agent
}

// refuseOwnerlessAgent refuses, before anything is written, the team step
// of a new org (setup apply team) that would run as an agent with no person
// behind it: one named with --actor agent:<label>. devco D-440: the roster
// refused such an agent only after the step had created and committed the
// org's store clone, leaving a half-made org. A person, a detected agent
// (it has an owner) and an agent credited to its person (creditPerson)
// pass.
func (a *App) refuseOwnerlessAgent(ctx context.Context, step string) error {
	if creditedAgent(ctx) != "" {
		return nil
	}
	who, err := resolveIdentity(ctx, a.opts)
	if err != nil || who.Kind != schema.ActorKindAgent || who.Owner != nil {
		return nil // an identity that does not resolve is refused by the step itself
	}
	return errs.New(errs.InvalidInput, step+": the agent "+who.StableKey+" has no person behind it, so it cannot set up an org's roster; nothing was written").
		WithHint("run this step as yourself, without --actor (a LORE_ACTOR=agent:<label> step is credited to you); once the org exists, `lore agent start --label <label>` registers your agent under you")
}
