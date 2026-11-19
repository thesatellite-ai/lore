package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestAliasHostResolverAsksSetupOnEveryMissD684: an alias the ssh config
// does not name but a setup's github.host_alias does resolves to github.com
// on every question of the process, not only the first (devco D-684: the
// ssh miss was cached and the setup never asked again, so a linked client
// repo's alias remote matched no link in `lore sync`).
func TestAliasHostResolverAsksSetupOnEveryMissD684(t *testing.T) {
	sshAsked := 0
	ssh := func(host string) (orgconf.SSHAlias, bool) {
		sshAsked++
		if host == "work-gh" {
			return orgconf.SSHAlias{HostName: "github.com"}, true
		}
		return orgconf.SSHAlias{}, false
	}
	setup := func(host string) (string, bool) {
		if host == "acme-gh" {
			return "github.com", true
		}
		return "", false
	}
	resolve := aliasHostResolver(ssh, setup)
	for i := 0; i < 3; i++ {
		if hn, ok := resolve("acme-gh"); !ok || hn != "github.com" {
			t.Fatalf("question %d: host_alias acme-gh resolved to %q, %v; want github.com", i+1, hn, ok)
		}
		if hn, ok := resolve("work-gh"); !ok || hn != "github.com" {
			t.Fatalf("question %d: ssh alias work-gh resolved to %q, %v; want github.com", i+1, hn, ok)
		}
		if hn, ok := resolve("other"); ok {
			t.Fatalf("question %d: unnamed host resolved to %q", i+1, hn)
		}
	}
	if sshAsked != 3 {
		t.Errorf("the ssh config was read %d times for 3 hosts; its answers are cached per host", sshAsked)
	}
}
