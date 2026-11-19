package wire

// migratedby.go — who `lore migrate run` records as having raised a
// project's format (T-1). It is the PERSON, never "<person> via <tool>
// (agent:auto/…)": a detected agent acts for its person; the agent goes
// into migrated_via only, the audit counterpart of the Lore-Agent commit
// trailer (gitcommit.AgentTrailer).
//
// devco:D-705: the person is recorded by display NAME only, never "Name
// <email>" and never an actor id. _meta.json is a committed file of the
// code repo, and in a client repo lore's own pre-commit leak check refuses
// a commit whose non-row files name the email of a person on an org
// store's roster (logic/leakcheck emailHits, F-4): an email there made
// `lore migrate run` produce the one commit lore itself refuses. An actor
// id is no way out: the same check refuses a roster actor id outside a row
// file unless that actor's row file is in the same commit (idHits,
// projectedActors), which a migration cannot promise. The name is what the
// commit's author line shows anyway; the rule is the same in every repo
// kind (one rule, no client/own branch). A _meta.json an earlier build
// wrote with "Name <email>" still reads and is shown as written.

import (
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
)

// migratedBy splits who into the person who ran the migration (their
// display name; "" when they have none, or it holds an address: no name is
// ever made up from an email) and the agent it ran through ("<tool>
// (<agent key>)", "" for the person, CI or an agent with no tool label).
func migratedBy(who identity.Resolved) (by, via string) {
	if name := strings.TrimSpace(who.Person().DisplayName); !strings.Contains(name, "@") {
		by = name
	}
	if key := strings.TrimSpace(who.Agent); key != "" {
		via = key
		if label := strings.TrimSpace(who.Label); label != "" {
			via = label + " (" + key + ")"
		}
	}
	return by, via
}
