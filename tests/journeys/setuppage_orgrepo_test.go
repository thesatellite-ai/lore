//go:build journeys && !windows

package journeys

// setuppage_orgrepo_test.go — devco N-1: a lead starts the team from the
// setup page opened in a lore project of the org's own repo. The page is
// one long-lived lore process that had read the project before the team
// step wrote the org entry, so the team step's roster write refused "no
// store named <org>/org is attached" (and every retry on the same page
// did), while the same answers through `lore setup apply` worked. Driven
// here through `lore api`, the same long-lived server (it follows config
// the way the page does): the team step saves, a second save is unchanged,
// and setup list's folder line names the org store instead of "no
// attached store".

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestJourneyTeamStepOnAPageInAnOrgRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	dir := tt.repo[tlLee.Email].Dir

	w.Step("day1/team-from-a-server-in-the-org-repo", func() {
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		api := jAgentsStartAPI(t, tt.repo[tlLee.Email], nil, "")
		apply := func(step, answers string) (int, string) {
			t.Helper()
			body := `{"setup":"` + tlLeadSetup + `","step":"` + step + `","answers":` + answers + `}`
			req, err := http.NewRequest(http.MethodPost, api.base+"/v1/setup/apply", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+api.token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			raw, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(raw)
		}
		team := `{"org":"` + tlOrg + `","store_url":"` + tt.store.SSHURL() + `","own":["github.com/acme-co/*"]}`
		for _, s := range [][2]string{
			{"kind", `{"kind":"admin","name":"` + tlLeadSetup + `"}`},
			{"identity", `{"name":"` + tlLee.Name + `","email":"` + tlLee.Email + `"}`},
			{"github", `{}`},
			{"personal_store", `{"personal_store":"local"}`},
			{"team", team},
			{"team", team}, // saved again on the same page: unchanged, not refused
		} {
			if code, out := apply(s[0], s[1]); code/100 != 2 {
				t.Fatalf("setup apply %s on the long-lived server in the org repo → %d: %s", s[0], code, out)
			}
		}
		r := lee.Lore(dir, "setup", "list").OK(t)
		r.Says(t, tlOrg+"/org")
		if strings.Contains(r.Stdout, "no attached store") {
			t.Fatalf("setup list in the org repo after the team step says no attached store:\n%s", r)
		}
	})
}
