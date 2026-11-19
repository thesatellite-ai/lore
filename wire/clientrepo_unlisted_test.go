package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// TestUnlistedClientRepoOnlyByDefault pins devco D-525: a repo is "client
// only by the fail-closed default" exactly when no repos: pattern and no
// client link of the org claims it; only then does UnlistedOwnStep name
// the own: pattern that declares it the org's own repo. A repo the org
// claims as a client's keeps the plain client lines (D-512 unchanged).
func TestUnlistedClientRepoOnlyByDefault(t *testing.T) {
	t.Parallel()
	org := &orgconf.Org{Name: "agency", Repos: []string{"github.com/client/*"}}
	cases := []struct {
		name    string
		remotes []string
		links   []orgresolve.Link
		want    string // "" = no way-out step
	}{
		{"own repo never listed", []string{"github.com/agency/tools"}, nil, "lore org config set --org agency --key own --add github.com/agency/tools --yes"},
		{"repos: pattern claims it", []string{"github.com/client/website"}, nil, ""},
		{"client link claims it", []string{"github.com/agency/tools"}, []orgresolve.Link{{Entry: "agency", Folder: "shop"}}, ""},
		{"no hosted remote", nil, nil, ""},
		// D-531: a forge host is named before a host with no dot
		{"alias and a real host", []string{"work-gh/agency/tools", "gitlab.example/agency/tools"}, nil, "--key own --add gitlab.example/agency/tools --yes"},
		// devco D-706: a server reached by its ssh name (scp-style
		// buildbox:/srv/git/agency/tools normalises to this) is named too,
		// with the caveat that the spelling is this machine's
		{"server by its ssh name", []string{"buildbox/srv/git/agency/tools"}, nil, "--key own --add buildbox/srv/git/agency/tools --yes (buildbox is this machine's name for the server"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := UnlistedOwnStep(UnlistedClientRepo(org, c.links, c.remotes))
			if c.want == "" {
				if got != "" {
					t.Fatalf("UnlistedOwnStep = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, c.want) || !strings.Contains(got, "no own: pattern of agency matches this repo's remote") {
				t.Fatalf("UnlistedOwnStep = %q, want the cause and %q", got, c.want)
			}
		})
	}
	if UnlistedOwnStep(UnlistedClientRepo(nil, nil, []string{"github.com/agency/tools"})) != "" {
		t.Fatal("no org: no way-out step")
	}
}

// TestUnlistedServerRemoteMatchable pins devco D-706 end to end in the
// pure parts: the remote spellings of a repo on a server reached by its
// ssh name all normalise to host/path, the command UnlistedOwnStep prints
// adds exactly that as an own: pattern, and with it the repo is own (no
// longer client by the fail-closed default). A folder pattern in either
// spelling (host/path/* or host:/path/*) matches it too.
func TestUnlistedServerRemoteMatchable(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"buildbox:/srv/git/agency/tools.git", "dev@buildbox:/srv/git/agency/tools", "ssh://buildbox/srv/git/agency/tools.git"} {
		norm, err := repourl.Normalize(raw)
		if err != nil || norm != "buildbox/srv/git/agency/tools" {
			t.Fatalf("Normalize(%q) = %q, %v", raw, norm, err)
		}
		org := &orgconf.Org{Name: "agency", Own: []string{"github.com/agency/*"}}
		c := UnlistedClientRepo(org, nil, []string{norm})
		if c.Remote != norm {
			t.Fatalf("%s: UnlistedClientRepo = %+v, want the remote named", raw, c)
		}
		org.Own = append(org.Own, c.Remote) // what the printed command adds
		if got := orgresolve.ClassifyRemote(org, []string{norm}).Role; got != orgresolve.RoleOwn {
			t.Fatalf("%s: with the printed pattern the role is %q, want own", raw, got)
		}
		for _, p := range []string{"buildbox/srv/git/agency/*", "buildbox:/srv/git/agency/*"} {
			cp, err := repourl.Compile(p)
			if err != nil || !cp.Match(norm) {
				t.Fatalf("pattern %q does not match %q (%v)", p, norm, err)
			}
		}
	}
}
