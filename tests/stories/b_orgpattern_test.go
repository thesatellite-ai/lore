//go:build stories && !windows

package stories

// b_orgpattern_test.go — story B, step H-112 (devco:D-857): an admin who
// writes an org repo pattern without a host (client-*) is told the shape
// host/owner/prefix* and handed the same command with the pattern it most
// likely meant, which runs as printed.

import (
	"testing"
)

func TestStoryBOrgPatternNeedsAHost(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	const store = "acme_team"

	s.Step("D-857/org-create-without-a-host-names-the-shape", func() {
		b.person(t, bCTO)
		b.lore(bCTO, "store", "init", "--name", store, "--url", b.store.SSHURL(), "--yes").OK(t)
		r := b.lore(bCTO, "org", "create", "--org", bOrg, "--store", store, "--own", "acme",
			"--key", "human:"+bCTO.Email, "--name", bCTO.Name, "--handle", "cora", "--yes").
			Refused(t, `pattern "acme" has no host`, "host/owner/prefix*", "--own 'github.com/acme/*'").Never(t, "needs a host")
		s.NamedFlags(t, r)
		b.lore(bCTO, "org", "create", "--org", bOrg, "--store", store, "--own", "github.com/acme/*",
			"--key", "human:"+bCTO.Email, "--name", bCTO.Name, "--handle", "cora", "--yes").OK(t)
	})

	s.Step("D-857/org-config-set-hint-runs-as-printed", func() {
		r := b.lore(bCTO, "org", "config", "set", "--org", bOrg, "--key", "repos", "--add", "client-*", "--add", "github.com/acme/*", "--yes").
			Refused(t, `pattern "client-*" has no host`, "like github.com/acme/client-*",
				"lore org config set --org "+bOrg+" --key repos --add 'github.com/acme/client-*' --add 'github.com/acme/*' --yes")
		s.NamedFlags(t, r)
		b.lore(bCTO, "org", "config", "set", "--org", bOrg, "--key", "repos", "--add", "github.com/acme/client-*", "--add", "github.com/acme/*", "--yes").OK(t).
			Says(t, "  + github.com/acme/client-*")
	})
}
