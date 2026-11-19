package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
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
		// D-531: an ssh alias host the ssh config did not resolve is one
		// person's spelling: never offered for the shared own: list
		{"unresolved alias only", []string{"work-gh/agency/tools"}, nil, ""},
		{"alias and a real host", []string{"work-gh/agency/tools", "gitlab.example/agency/tools"}, nil, "--key own --add gitlab.example/agency/tools --yes"},
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
			if !strings.Contains(got, c.want) || !strings.Contains(got, "no own: pattern of agency matches") {
				t.Fatalf("UnlistedOwnStep = %q, want the cause and %q", got, c.want)
			}
		})
	}
	if UnlistedOwnStep(UnlistedClientRepo(nil, nil, []string{"github.com/agency/tools"})) != "" {
		t.Fatal("no org: no way-out step")
	}
}
