package identity

import (
	"context"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Agent identity facts (AGENT_IDENTITY_DESIGN 6.1, 6.5).
const (
	// agentAutoPrefix starts a detected agent's stable key (OQ9).
	agentAutoPrefix = KindAgent + kindSep + "auto/"
	// ownerShortHex is how many hex characters of the owner's act_ id the
	// key carries (OQ9).
	ownerShortHex = 6
	// ownerWideHex is the width used instead when another owner already
	// holds the 6-hex key (6.1 collision rule: "the second one uses 10").
	ownerWideHex = 10
	// actPrefixLen skips "act_".
	actPrefixLen = 4
	// FlagHumanOverAgent is set when LORE_HUMAN=1 met tool markers (10.6).
	FlagHumanOverAgent = "human_over_agent_signals"
)

// Resolve runs the person chain, then agent detection (package flow).
// LORE_ACTOR always wins; detection never changes who the person is, it
// only says "this person's tool did it" (section 3).
func Resolve(ctx context.Context, in Inputs) (Resolved, error) {
	r, err := resolveChain(ctx, in)
	if err != nil {
		return r, err
	}
	if r.Kind == KindAgent {
		r.Agent = r.StableKey
	}
	if in.Detect == nil || DetectionOff() {
		return r, nil // DetectionOff: loretest-only LORE_NO_DETECT (cl-gate)
	}
	det, derr := in.Detect()
	if derr != nil {
		r.Notes = append(r.Notes, "agent detection skipped: "+derr.Error())
		return r, nil
	}
	r.Detection = &det
	if det.Note != "" {
		r.Notes = append(r.Notes, det.Note) // 10.4 stale marker: one line for the person
	}
	if r.Step == StepFlag || r.Step == StepEnv {
		return r, nil // step 1: an explicit actor wins; detection is recorded only
	}
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	switch h := strings.TrimSpace(getenv(EnvHuman)); {
	case h == "1":
		r.DeclaredHuman = true
		if det.IsAgent() {
			det.Flags = append(det.Flags, FlagHumanOverAgent)
			r.Detection = &det
		}
		return r, nil
	case h != "":
		r.Notes = append(r.Notes, EnvHuman+" ignored: only the value 1 counts")
	}
	if det.IsAgent() {
		return agentOf(r, det, ownerShortHex)
	}
	if det.CI != nil {
		return ciOf(det), nil
	}
	return r, nil
}

// agentOf is the detected agent's identity, owned by person (6.1): key
// agent:auto/<tool>/<first 6 hex of the owner's act_ id>, display
// "<owner> via <tool>", label the tool slug. The owner's handle is only
// the display, because handles change and stable_key cannot (OQ9).
func agentOf(person Resolved, det agentdetect.Result, width int) (Resolved, error) {
	ownerID, err := person.ActorID() // an email change keeps the owner's id, so its agents keep theirs
	if err != nil {
		return person, err
	}
	key := agentAutoKey(ownerID, det.Tool, width)
	owner := person
	out := Resolved{StableKey: key, DisplayName: person.DisplayName + " via " + det.Tool, Kind: KindAgent, Step: StepDetected,
		Org: person.Org, Notes: person.Notes, Agent: key, Label: det.Tool, Owner: &owner, Detection: &det}
	return out, nil
}

// agentAutoKey is the stable key of owner's detected agent in tool, with
// width hex characters of the owner's act_ id (6.1, OQ9).
func agentAutoKey(owner ids.ID[ids.Actor], tool string, width int) string {
	return agentAutoPrefix + tool + "/" + owner.String()[actPrefixLen:actPrefixLen+width]
}

// DetectedAgentIDs are the actor ids owner's detected agent in tool may
// write as: the 6-hex key and the 10-hex key of the collision rule (6.1).
// Doctor uses them to name an agent whose only saves are machine-local
// rows, which carry no actors row in any store (P3-LOCAL-1).
func DetectedAgentIDs(owner ids.ID[ids.Actor], tool string) ([]ids.ID[ids.Actor], error) {
	out := make([]ids.ID[ids.Actor], 0, 2)
	for _, w := range []int{ownerShortHex, ownerWideHex} {
		id, err := ActorID(agentAutoKey(owner, tool, w))
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// ReownAgent is detected agent r derived again from owner: the same person
// found under an earlier key (an email change made on another machine,
// wire movedKeyActor), so the agent's key carries the owner's kept act_
// id and the agent keeps its id too. ok is false when r is not a detected
// agent (an explicit LORE_ACTOR key is never rewritten). The width is the
// 6-hex one; the caller settles the 6.1 collision rule again.
func ReownAgent(r Resolved, owner Resolved) (Resolved, bool, error) {
	if r.Step != StepDetected || r.Owner == nil || r.Detection == nil {
		return r, false, nil
	}
	w, err := agentOf(owner, *r.Detection, ownerShortHex)
	if err != nil {
		return r, false, err
	}
	return w, true, nil
}

// ciOf is the CI actor (6.5, OQ13): kind system (a new ci kind would be a
// breaking synced-enum change), key ci:<provider>/<repo>.
func ciOf(det agentdetect.Result) Resolved {
	c := det.CI
	display := strings.TrimSpace("ci " + c.Provider + " " + c.Repo)
	return Resolved{StableKey: c.ActorKey(), DisplayName: display, Kind: KindSystem, Step: StepCI, Label: c.Provider, Detection: &det}
}

// WidenAgentKey is r with the 10-hex owner short id, for a detected agent
// whose 6-hex key is already held by another owner's agent (6.1, OQ9: the
// second one created uses 10). ok is false when r is not a detected agent
// (an explicit LORE_ACTOR key is never rewritten). The caller decides when
// the key is taken (it needs the stores); this only computes the key.
func WidenAgentKey(r Resolved) (Resolved, bool, error) {
	if r.Step != StepDetected || r.Owner == nil || r.Detection == nil {
		return r, false, nil
	}
	w, err := agentOf(*r.Owner, *r.Detection, ownerWideHex)
	if err != nil {
		return r, false, err
	}
	return w, true, nil
}
