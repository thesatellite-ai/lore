package engine

// agentnowrite.go — agents.no_write, the place-level twin of
// agents.person_only (owner org-setup item 6, devco D-869). person_only
// names commands, so a folder only people may change (acme/legal) stayed
// open to every agent through any command person_only did not list. This
// key names the place: an agent caller (a bot too: plugin, cron, system,
// hook; identity.NonHuman, H-259) is refused every write into a place
// whose OWN policies list it (or "*").
//
//	Run (agent caller, write op) ──▶ agentNoWrite(target)   refused before any row is written
//	writeChanges / writePurge / move_many sinks
//	     ──▶ guardWrites ──▶ agentNoWrite(st)               every write path, whatever command
//
// Only the target place's own policies rows count, not the union of every
// attached store (storeSettings.Policy): a folder that keeps agents out
// must not keep them out of the repo next to it that only reads it.

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// AgentsAll is the agents.no_write item that names every agent and bot.
const AgentsAll = "*"

// PlaceLabeler is the optional upgrade of a WriteGuard that names an
// attached store as the person knows it ("acme/legal" for a linked folder
// attached as "acme"), so a refusal names the real place and the --to that
// changes its policy.
type PlaceLabeler interface {
	PlaceLabel(src store.Source) string
}

// placeLabel is src as a refusal names it.
func (e *Engine) placeLabel(src store.Source) string {
	if l, ok := e.guard.(PlaceLabeler); ok {
		return l.PlaceLabel(src)
	}
	return string(src)
}

// AgentSource resolves the agent a write runs for when the running
// operation's caller does not say (wire: the identity chain with agent
// detection, "" for a person). Why: some CLI paths (a folder with no lore
// project, a setup-scoped open) invoke the operation without Run's caller,
// or with a caller whose agent was never filled in, so a check that read
// only op.Caller.Agent let a detected agent through (devco D-869).
type AgentSource func(ctx context.Context) string

// SetAgentSource binds the fallback agent resolver (nil: none), like
// SetWriteGuard after New.
func (e *Engine) SetAgentSource(f AgentSource) { e.agentOf = f }

// writerAgent is the agent the write in ctx runs for: the caller's when the
// surface decided it (REST's X-Lore-Agent, AgentFixed) or named one, else
// the AgentSource's answer.
func (e *Engine) writerAgent(ctx context.Context) string {
	if c, ok := CallerOf(ctx); ok && (c.AgentFixed || c.Agent != "") {
		return c.Agent
	}
	if e.agentOf != nil {
		return e.agentOf(ctx)
	}
	return ""
}

// agentNoWriteChanges applies agents.no_write to the changes of one write
// into st: lore's own upkeep (WithSupportingRefresh) never counts as the
// agent's write.
func (e *Engine) agentNoWriteChanges(ctx context.Context, st Attached, changes []store.Change) error {
	if !slices.ContainsFunc(changes, func(c store.Change) bool { return !c.Upkeep }) {
		return nil
	}
	return e.agentNoWrite(ctx, st)
}

// agentNoWrite refuses the write in ctx into st when it runs for an agent
// that st's own policies list (case-insensitive) or "*" in
// agents.no_write. The policies are read first, so a write into a place
// without the key resolves no agent. The refusal names the key, the policy
// row, the place and the exact command a person runs to change the list;
// it is needs_person (the agent asks its human).
func (e *Engine) agentNoWrite(ctx context.Context, st Attached) error {
	rows, err := e.List(ctx, st.Name, schema.TablePolicies)
	if err != nil {
		return errs.Prefix("read the policies of "+e.placeLabel(st.Name), err)
	}
	var entries []policy.Entry
	for _, r := range rows {
		en, perr := policy.EntryFromRow(levelOf(st), r)
		if perr != nil {
			return perr
		}
		if en.Key == policy.KeyAgentsNoWrite {
			entries = append(entries, en)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	agent := e.writerAgent(ctx)
	if agent == "" {
		return nil
	}
	set, err := policy.Resolve(entries)
	if err != nil {
		return err
	}
	listed := set.Strings(policy.KeyAgentsNoWrite)
	names := agentNames(agent)
	hit := ""
	for _, it := range listed {
		if it == AgentsAll || slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(it, n) }) {
			hit = it
			break
		}
	}
	if hit == "" {
		return nil
	}
	place := e.placeLabel(st.Name)
	where := ""
	if eff, ok := set.Get(policy.KeyAgentsNoWrite); ok && !eff.Source.IsZero() {
		where = " (policy " + eff.Source.String() + ")"
	}
	who := "every agent and bot"
	if hit != AgentsAll {
		who = strconv.Quote(hit)
	}
	key := string(policy.KeyAgentsNoWrite)
	e2 := errs.New(errs.PolicyDenied, key+" of "+place+" lists "+who+where+": no agent or bot may write there, and this looks like "+strconv.Quote(agent)+"; nothing was written").
		WithHint("a person runs it in their own terminal; a person (in an org's company folder, an admin) changes the list with `lore policy unset --key " + key + " --to " + place + " --yes` or `lore policy set --key " + key + " --value <agents> --to " + place + " --yes`")
	e2.NeedsPerson = true
	return e2
}

// agent stable keys (adapters/identity): "agent:auto/<tool>/<owner hex>"
// for a detected agent, "<kind>:<label>" for LORE_ACTOR=agent:<label> and
// for a bot (plugin:<label>, cron:<label>, system:<label>, H-259).
const (
	agentKeySep     = ":"
	agentKindAgent  = "agent"
	agentAutoPrefix = "auto/"
)

// agentNames are the names an agents.no_write item may use for the agent
// or bot whose stable key is key: the key itself, its label after the
// kind ("helper" of plugin:helper), and for a detected agent the tool
// ("codex" of agent:auto/codex/1a2b3c), so a folder names the tool once
// for every person's agent of that tool. Why every kind: a bot's label
// matched nothing while only "agent:" was cut (H-259).
func agentNames(key string) []string {
	names := []string{key}
	kind, rest, ok := strings.Cut(key, agentKeySep)
	if !ok || kind == "" || rest == "" {
		return names
	}
	names = append(names, rest)
	if auto, isAuto := strings.CutPrefix(rest, agentAutoPrefix); isAuto && kind == agentKindAgent {
		tool, _, _ := strings.Cut(auto, "/")
		if tool != "" {
			names = append(names, tool)
		}
	}
	return names
}
