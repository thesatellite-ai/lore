package gitcommit

import "strings"

// Agent signatures (devco:D-983). Every commit lore makes on any ref is
// authored and committed as the person: their git identity, else their
// lore identity. Older versions signed some commits as the detected agent,
// "<person> via <tool>" <agent.auto/<tool>/<hex>@lore.invalid>, and that
// author reached public remotes. IsAgentSignature is the one test every
// signer and every commit sink applies, so no path can bring it back.
const (
	// AgentEmailDomain is the non-routable domain older lore versions made
	// commit e-mails in for keys that hold no address (agent:…, a machine
	// key). No commit lore makes carries it any more.
	AgentEmailDomain = "lore.invalid"
	// agentNameMark is the " via <tool>" of an agent's display name.
	agentNameMark = " via "
)

// IsAgentSignature reports whether a commit author or committer is an
// agent's rather than a person's: an e-mail at AgentEmailDomain or a name
// of the "<person> via <tool>" form. Such a signature is never used to sign
// a commit; a signer that would produce one falls back to the next
// identity, and lore doctor counts the old commits that carry one.
func IsAgentSignature(name, email string) bool {
	e := strings.ToLower(strings.TrimSpace(email))
	return strings.HasSuffix(e, "@"+AgentEmailDomain) || strings.Contains(name, agentNameMark)
}
