package repourl

import "strings"

// providerSSHRoutes maps a git provider's own ssh-over-port-443 host to the
// provider's host: the HostName an ssh config gives an alias when port 22
// is blocked (`Host work-gh` / `HostName ssh.github.com` / `Port 443`).
// The repositories behind the route are the provider's, so a url through
// it names the same store as one on the provider host.
var providerSSHRoutes = map[string]string{
	"ssh.github.com":       "github.com",
	"altssh.gitlab.com":    "gitlab.com",
	"altssh.bitbucket.org": "bitbucket.org",
}

// ProviderSSHHost is host with a known provider's port-443 ssh route
// replaced by the provider's host (ssh.github.com → github.com), lower
// case; any other host is returned lower case as it is. Every place that
// resolves a host from the person's ssh config (store identity, the
// same-store test of a clone's origin, a code repo's remote, the canonical
// url of an alias spelling) maps the HostName through it, so an alias whose
// HostName is the route is the provider's host (devco D-629).
func ProviderSSHHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if p, ok := providerSSHRoutes[h]; ok {
		return p
	}
	return h
}
