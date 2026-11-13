package wire

// migratedby.go — who `lore migrate run` records as having raised a
// project's format (T-1). It is the PERSON, "Name <email>", the same
// identity a commit lore makes carries (owner bug 12: storeSigner.fallback),
// never "<person> via <tool> (agent:auto/…)": a detected agent acts for its
// person. The agent goes into migrated_via only, the audit counterpart of
// the Lore-Agent commit trailer (gitcommit.AgentTrailer).

import (
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
)

// migratedBy splits who into the person who ran the migration ("Name
// <email>", or the name alone when the person's key holds no address) and
// the agent it ran through ("<tool> (<agent key>)", "" for the person, CI
// or an agent with no tool label).
func migratedBy(who identity.Resolved) (by, via string) {
	name, email := who.Person().CommitSignature()
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	switch {
	case email == "" || strings.HasSuffix(strings.ToLower(email), "@"+identity.CommitEmailDomain):
		by = name // no address of a person: never a made-up lore.invalid one
	case name == "":
		by = "<" + email + ">"
	default:
		by = name + " <" + email + ">"
	}
	if key := strings.TrimSpace(who.Agent); key != "" {
		via = key
		if label := strings.TrimSpace(who.Label); label != "" {
			via = label + " (" + key + ")"
		}
	}
	return by, via
}
