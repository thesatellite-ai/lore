package identity

import (
	"context"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// Agent identity facts (AGENT_IDENTITY_DESIGN 6.1, 6.5).
const (
	// agentAutoPrefix starts a detected agent's stable key (OQ9).
	agentAutoPrefix = KindAgent + kindSep + "auto/"
	// ownerShortHex is how many hex characters of the owner's act_ id the
	// key carries (OQ9).
	ownerShortHex = 6
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
	if in.Detect == nil {
		return r, nil
	}
	det, derr := in.Detect()
	if derr != nil {
		r.Notes = append(r.Notes, "agent detection skipped: "+derr.Error())
		return r, nil
	}
	r.Detection = &det
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
		return agentOf(r, det)
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
func agentOf(person Resolved, det agentdetect.Result) (Resolved, error) {
	ownerID, err := ActorID(person.StableKey)
	if err != nil {
		return person, err
	}
	short := ownerID.String()[actPrefixLen : actPrefixLen+ownerShortHex]
	key := agentAutoPrefix + det.Tool + "/" + short
	owner := person
	out := Resolved{StableKey: key, DisplayName: person.DisplayName + " via " + det.Tool, Kind: KindAgent, Step: StepDetected,
		Org: person.Org, Notes: person.Notes, Agent: key, Label: det.Tool, Owner: &owner, Detection: &det}
	return out, nil
}

// ciOf is the CI actor (6.5, OQ13): kind system (a new ci kind would be a
// breaking synced-enum change), key ci:<provider>/<repo>.
func ciOf(det agentdetect.Result) Resolved {
	c := det.CI
	display := strings.TrimSpace("ci " + c.Provider + " " + c.Repo)
	return Resolved{StableKey: c.ActorKey(), DisplayName: display, Kind: KindSystem, Step: StepCI, Label: c.Provider, Detection: &det}
}
