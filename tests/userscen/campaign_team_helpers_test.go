//go:build userscen

package userscen

// campaign_team_helpers_test.go — helpers of the team campaign
// (TestCampaignTeam_*): the 49 commands of the team area (people, orgs,
// projects, roles, contact types, scopes, workspaces) driven the way a
// person runs them. The world is the org fixture of org_fixture_test.go
// (newOrgScen: mira's agency on a lore store, teammates joining with
// `lore org import`), reused rather than rebuilt. Every helper name starts
// with team so it never collides with another area's helpers.

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teamCommands is every command of the team area (inventory.md, agent
// scen-team), as the words after `lore`.
var teamCommands = []string{
	"actor contact add", "actor contact edit", "actor contact list", "actor contact remove",
	"actor deactivate", "actor delete", "actor edit", "actor list", "actor offboard",
	"actor period add", "actor period edit", "actor period list",
	"actor register", "actor rejoin", "actor show", "actor status",
	"agent start",
	"contact-type add", "contact-type edit", "contact-type list",
	"org create", "org import", "org link", "org rename", "org set", "org use",
	"project archive", "project delete", "project list",
	"project member add", "project member edit", "project member list", "project member remove",
	"project shared-init", "project shared-list", "project show", "project unarchive",
	"role add", "role edit", "role list",
	"scope list", "scope move", "scope show",
	"workspace add", "workspace delete", "workspace edit", "workspace list", "workspace search", "workspace show",
}

// teamUnknownWsp, teamUnknownPrj are well-formed ids no row has.
const (
	teamUnknownWsp = "wsp_0190f0f0f0f07000800000000000dead"
	teamUnknownPrj = "prj_0190f0f0f0f07000800000000000dead"
	teamUnknownAcn = "acn_0190f0f0f0f07000800000000000dead"
)

// teamLong is a 10k-character value (odd-values matrix).
var teamLong = strings.Repeat("ab", 5000)

// teamErr is the `--json` error object (spec 9.4) every refusal prints.
type teamErr struct {
	Error *struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Hint        string `json:"hint"`
		Exit        int    `json:"exit"`
		Class       string `json:"class"`
		Retryable   bool   `json:"retryable"`
		NeedsPerson bool   `json:"needs_person"`
	} `json:"error"`
}

// teamJSONErr decodes the error object of a refused `--json` run (on
// stdout or stderr); it fails the test when there is none.
func teamJSONErr(t *testing.T, r th.Result) teamErr {
	t.Helper()
	for _, s := range []string{r.Stdout, r.Stderr} {
		var e teamErr
		if json.Unmarshal([]byte(strings.TrimSpace(s)), &e) == nil && e.Error != nil {
			return e
		}
	}
	t.Fatalf("no --json error object: %s", r)
	return teamErr{}
}

// teamContact is one row of `lore actor contact list --json`.
type teamContact struct {
	ID        string `json:"id"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	ValueNorm string `json:"value_norm"`
	Label     string `json:"label"`
	Primary   bool   `json:"primary"`
	Identity  bool   `json:"identity"`
}

// teamContacts lists one person's contacts (or everyone's with no ref).
func teamContacts(t *testing.T, c *th.Clone, ref ...string) []teamContact {
	t.Helper()
	args := append([]string{"actor", "contact", "list"}, ref...)
	v := th.DecodeJSON[struct {
		Contacts []teamContact `json:"contacts"`
	}](t, c.MustLore(append(args, "--json")...))
	return v.Contacts
}

// teamContactByValue finds the contact whose value is v.
func teamContactByValue(cs []teamContact, v string) (teamContact, bool) {
	for _, c := range cs {
		if c.Value == v {
			return c, true
		}
	}
	return teamContact{}, false
}

// teamWorkspace is one workspace of `lore workspace list|show --json`.
type teamWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Body string `json:"body"`
}

// teamWorkspaces is `lore workspace list --json`.
func teamWorkspaces(t *testing.T, c *th.Clone) []teamWorkspace {
	t.Helper()
	v := th.DecodeJSON[struct {
		Count int             `json:"count"`
		Data  []teamWorkspace `json:"data"`
	}](t, c.MustLore("workspace", "list", "--json"))
	if v.Count != len(v.Data) {
		t.Fatalf("workspace list count %d of %d rows", v.Count, len(v.Data))
	}
	return v.Data
}

// teamProject is one row of `lore project list --json`.
type teamProject struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ArchivedAt string `json:"archived_at"`
}

// teamProjects is `lore project list --json` (plus extra flags).
func teamProjects(t *testing.T, c *th.Clone, extra ...string) []teamProject {
	t.Helper()
	v := th.DecodeJSON[struct {
		SchemaVersion int           `json:"schema_version"`
		Kind          string        `json:"kind"`
		Count         int           `json:"count"`
		Data          []teamProject `json:"data"`
	}](t, c.MustLore(append([]string{"project", "list", "--json"}, extra...)...))
	if v.SchemaVersion != 1 || v.Kind != "project.list" || v.Count != len(v.Data) {
		t.Fatalf("project list --json envelope: %d %q %d of %d", v.SchemaVersion, v.Kind, v.Count, len(v.Data))
	}
	return v.Data
}

// teamProjectNamed is the id of the project called name in c.
func teamProjectNamed(t *testing.T, c *th.Clone, name string) string {
	t.Helper()
	for _, p := range teamProjects(t, c) {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatalf("no project %q in %s", name, c.Dir)
	return ""
}

// teamRoles is the role names of `lore role list --json`.
func teamRoles(t *testing.T, c *th.Clone) map[string]bool {
	t.Helper()
	v := th.DecodeJSON[struct {
		Roles []struct {
			Name    string `json:"name"`
			Label   string `json:"label"`
			Retired bool   `json:"retired"`
			Builtin bool   `json:"builtin"`
		} `json:"roles"`
	}](t, c.MustLore("role", "list", "--json"))
	out := map[string]bool{}
	for _, r := range v.Roles {
		out[r.Name] = !r.Retired
	}
	return out
}
