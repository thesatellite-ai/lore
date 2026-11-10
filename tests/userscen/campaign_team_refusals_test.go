//go:build userscen

package userscen

// campaign_team_refusals_test.go — the help and bad-input matrices of the
// team area: every command's --help shows a worked example naming the
// command itself; every mistyped call answers its E_* code, its exit, the
// next step, the same error object under --json, and pushes nothing.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignTeam_HelpHasExamples: `lore <cmd> --help` exits 0, shows its
// usage line and an Examples block with at least one line running that
// same command (a person copies it), for all 49 team commands.
func TestCampaignTeam_HelpHasExamples(t *testing.T) {
	w := newWorld(t)
	p := w.Human("pat", "pat@example.com")
	dir := orgOutside(t, p)
	if len(teamCommands) != 49 {
		t.Fatalf("the team area has 49 commands, the list %d", len(teamCommands))
	}
	for _, cmd := range teamCommands {
		t.Run(strings.ReplaceAll(cmd, " ", "_"), func(t *testing.T) {
			r := p.Lore(dir, append(strings.Fields(cmd), "--help")...).OK(t)
			r.Says(t, "Usage:\n  lore "+cmd)
			ex := cliBlock(r.Stdout, "Examples:")
			// A refused or retired verb shows what to run instead.
			want := "lore " + cmd
			if teamRefusedVerbs[cmd] {
				want = "lore "
			}
			found := false
			for _, l := range ex {
				if strings.HasPrefix(strings.TrimSpace(l), want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("lore %s --help has no example running it (Examples: %q)\n%s", cmd, ex, r.Stdout)
			}
		})
	}
}

// teamRefusedVerbs always refuse; their example is the replacement.
var teamRefusedVerbs = map[string]bool{"actor delete": true, "project shared-init": true, "project shared-list": true}

// teamBad is one mistyped call: its exit, E_ code and what the message and
// hint must name.
type teamBad struct {
	name string
	args []string
	exit int
	code string
	say  []string
}

// TestCampaignTeam_BadInputMatrix: every team command given a wrong value,
// an unknown row or a missing argument, in text and --json; the store is
// not pushed and the clone stays clean.
func TestCampaignTeam_BadInputMatrix(t *testing.T) {
	o := newOrgScen(t)
	o.app.MustLore("actor", "contact", "add", "@mira", "github", "mira-gh")
	cases := []teamBad{
		// contacts
		{"contact add unknown type", []string{"actor", "contact", "add", "@mira", "nosuchtype", "x"}, orgExitBadInput, "E_CONTACT_TYPE", []string{"nosuchtype is not in the list", "lore contact-type list"}},
		// No terminal in this harness, so the org folder is "<redacted>" in refusals too (§6.5).
		{"contact add unknown person", []string{"actor", "contact", "add", "@nobody", "github", "x"}, th.ExitNotFound, "E_NOT_FOUND", []string{"no actor @nobody on the roster in <redacted>", "lore actor register"}},
		{"contact add bad email", []string{"actor", "contact", "add", "@mira", "email", "not-an-email"}, orgExitBadInput, "E_CONTACT_TYPE", []string{"exactly one @", "lore contact-type list"}},
		{"contact add missing value", []string{"actor", "contact", "add", "@mira", "github"}, orgExitBadInput, "E_INVALID_INPUT", []string{"value"}},
		{"contact edit wrong-kind id", []string{"actor", "contact", "edit", teamUnknownWsp, "--label", "x"}, orgExitBadInput, "E_INVALID_ID", []string{"not a contact id (acn_…)", "lore actor contact list"}},
		{"contact edit unknown id", []string{"actor", "contact", "edit", teamUnknownAcn, "--label", "x"}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"contact remove wrong-kind id", []string{"actor", "contact", "remove", teamUnknownWsp}, orgExitBadInput, "E_INVALID_ID", []string{"lore actor contact list"}},
		{"contact list unknown person", []string{"actor", "contact", "list", "@nobody"}, th.ExitNotFound, "E_NOT_FOUND", []string{"@nobody"}},
		// periods and status
		{"period add bad type", []string{"actor", "period", "add", "@mira", "--type", "wizard", "--started-on", "2020-01-01"}, orgExitBadInput, "E_INVALID_INPUT", []string{"employee, contractor, intern, client_contact"}},
		{"period add bad date", []string{"actor", "period", "add", "@mira", "--type", "employee", "--started-on", "notadate"}, orgExitBadInput, "E_INVALID_INPUT", []string{"YYYY-MM-DD"}},
		{"period add ends before start", []string{"actor", "period", "add", "@mira", "--type", "employee", "--started-on", "2020-01-01", "--ended-on", "2019-01-01"}, th.ExitRefused, "E_PERIOD_OVERLAP", []string{"ends before it starts", "end it on or after its start (2020-01-01)"}},
		{"period list unknown person", []string{"actor", "period", "list", "@nobody"}, th.ExitNotFound, "E_NOT_FOUND", []string{"@nobody"}},
		{"period edit unknown", []string{"actor", "period", "edit", "apd_0190f0f0f0f07000800000000000dead", "--ended-on", "2026-01-01"}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"status unknown value", []string{"actor", "status", "@mira", "sleeping"}, orgExitBadInput, "E_INVALID_INPUT", []string{"active, on_leave, left, archived"}},
		{"status until before from", []string{"actor", "status", "@mira", "on_leave", "--from", "2026-12-01", "--until", "2026-11-01"}, orgExitBadInput, "E_INVALID_INPUT", []string{"before --from"}},
		{"offboard unknown", []string{"actor", "offboard", "@nobody", "--yes"}, th.ExitNotFound, "E_NOT_FOUND", []string{"@nobody"}},
		{"offboard unconfirmed", []string{"actor", "offboard", "@mira"}, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", []string{"rerun with --yes"}},
		{"rejoin an active person", []string{"actor", "rejoin", "@mira", "--type", "employee", "--yes"}, th.ExitRefused, "E_STATUS_CHANGE", nil},
		{"rejoin without type", []string{"actor", "rejoin", "@mira", "--yes"}, orgExitBadInput, "E_INVALID_INPUT", []string{"--type"}},
		{"deactivate unknown", []string{"actor", "deactivate", "@nobody"}, orgExitBadInput, "E_INVALID_INPUT", []string{"no one is @nobody on the roster in <redacted>", "lore actor register"}},
		{"delete is refused", []string{"actor", "delete", "@mira"}, th.ExitRefused, "E_REFUSED", []string{"lore actor deactivate"}},
		{"edit bad timezone", []string{"actor", "edit", "@mira", "--timezone", "Not/AZone"}, orgExitBadInput, "E_INVALID_INPUT", nil},
		// agents
		{"agent label with a space", []string{"agent", "start", "--label", "bad label"}, orgExitBadInput, "E_INVALID_INPUT", []string{"lower-case letters"}},
		// contact types and roles
		{"contact-type bad name", []string{"contact-type", "add", "BadName!", "--kind", "handle"}, orgExitBadInput, "E_INVALID_INPUT", []string{"snake_case", "work_slack"}},
		{"contact-type bad kind", []string{"contact-type", "add", "matrix", "--kind", "weird"}, orgExitBadInput, "E_INVALID_INPUT", []string{"email, handle, phone, url, text"}},
		{"contact-type taken", []string{"contact-type", "add", "github", "--kind", "handle"}, th.ExitRefused, "E_NAME_TAKEN", []string{"lore contact-type edit github"}},
		{"contact-type edit unknown", []string{"contact-type", "edit", "nosuch", "--label", "x"}, orgExitBadInput, "E_CONTACT_TYPE", []string{"lore contact-type list"}},
		{"role taken", []string{"role", "add", "dev"}, th.ExitRefused, "E_NAME_TAKEN", []string{"lore role edit dev"}},
		{"role bad name", []string{"role", "add", "Bad Role"}, orgExitBadInput, "E_INVALID_INPUT", []string{"snake_case", "lore role add tech_lead"}},
		{"role edit unknown", []string{"role", "edit", "nosuch", "--label", "x"}, orgExitBadInput, "E_ROLE_UNKNOWN", []string{"lore role list"}},
		// projects and members
		// An unknown prj_ id answers as archive/unarchive/delete do (the
		// row lookup's not-found, whose hint names the newer-main case).
		{"project show unknown", []string{"project", "show", teamUnknownPrj}, th.ExitNotFound, "E_NOT_FOUND", []string{"lore sync peek origin/main projects"}},
		{"project archive unknown", []string{"project", "archive", teamUnknownPrj}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"project unarchive unknown", []string{"project", "unarchive", teamUnknownPrj}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"project delete unknown", []string{"project", "delete", teamUnknownPrj, "--yes"}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"project archive an unknown name", []string{"project", "archive", "no-such-project"}, th.ExitNotFound, "E_NOT_FOUND", []string{"lore project list"}},
		{"project archive a malformed id", []string{"project", "archive", "prj_zz"}, orgExitBadInput, "E_INVALID_ID", []string{"prj_"}},
		{"member add one argument", []string{"project", "member", "add", "@mira", "--role", "dev"}, orgExitBadInput, "E_INVALID_INPUT", []string{"actor"}},
		{"member add unknown person", []string{"project", "member", "add", "app", "@nobody", "--role", "dev"}, th.ExitNotFound, "E_NOT_FOUND", []string{"@nobody"}},
		{"member add unknown role", []string{"project", "member", "add", "app", "@mira", "--role", "nosuchrole"}, orgExitBadInput, "E_ROLE_UNKNOWN", []string{"lore role list"}},
		{"member add unknown project", []string{"project", "member", "add", "nosuchproj", "@mira", "--role", "dev"}, th.ExitNotFound, "E_NOT_FOUND", []string{"lore project list"}},
		{"member edit not a member", []string{"project", "member", "edit", "app", "@mira", "--role", "lead"}, th.ExitNotFound, "E_NOT_FOUND", []string{"lore project member list app --history"}},
		{"member remove not a member", []string{"project", "member", "remove", "app", "@mira"}, th.ExitNotFound, "E_NOT_FOUND", []string{"lore project member list app --history"}},
		{"member list unknown project", []string{"project", "member", "list", "nosuchproj"}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"shared-init retired", []string{"project", "shared-init"}, th.ExitRefused, "E_UNSUPPORTED", []string{"lore store init"}},
		{"shared-list retired", []string{"project", "shared-list"}, th.ExitRefused, "E_UNSUPPORTED", []string{"lore store"}},
		// orgs
		{"org rename unknown", []string{"org", "rename", "nosuch", "x"}, th.ExitNotFound, "E_NOT_FOUND", []string{"agency"}},
		{"org use unknown", []string{"org", "use", "nosuch"}, orgExitBadInput, "E_INVALID_INPUT", []string{"agency"}},
		{"org set unconfirmed", []string{"org", "set", orgName}, orgExitBadInput, "E_INVALID_INPUT", []string{"--commit", "lore org use agency"}},
		{"org set no org", []string{"org", "set"}, orgExitBadInput, "E_INVALID_INPUT", []string{"org"}},
		{"org link no project", []string{"org", "link", orgName}, orgExitBadInput, "E_INVALID_INPUT", []string{"--project"}},
		{"org create taken", []string{"org", "create", orgName, "--store", "https://" + orgSlugNotes, "--yes"}, orgExitBadInput, "E_INVALID_INPUT", []string{"already exists", "lore org link"}},
		{"org import unreachable", []string{"org", "import", "https://github.com/agency/nothere", "--yes"}, th.ExitOffline, "E_OFFLINE", []string{"retry"}},
		// scopes and workspaces
		{"scope move unknown", []string{"scope", "move", "nosuch", "other"}, th.ExitNotFound, "E_NOT_FOUND", []string{"lore scope list"}},
		{"scope move into itself", []string{"scope", "move", "a", "a"}, orgExitBadInput, "E_INVALID_INPUT", nil},
		{"workspace show unknown", []string{"workspace", "show", teamUnknownWsp}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"workspace edit unknown", []string{"workspace", "edit", teamUnknownWsp, "--name", "x"}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"workspace delete unknown", []string{"workspace", "delete", teamUnknownWsp, "--yes"}, th.ExitNotFound, "E_NOT_FOUND", nil},
		{"workspace search empty", []string{"workspace", "search", ""}, orgExitBadInput, "E_INVALID_INPUT", []string{"query"}},
		{"workspace add empty body", []string{"workspace", "add", "--title", "x"}, orgExitBadInput, "E_EMPTY_BODY", []string{"--body"}},
	}
	before := o.notes.Rev("refs/heads/main")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// "agency org/'s roster" read as a typo: the roster is named "the roster in <where>"
			o.app.Lore(c.args...).Fails(t, c.exit, append([]string{"[" + c.code + "]"}, c.say...)...).Never(t, "/'s roster")
			j := teamJSONErr(t, o.app.Lore(append(c.args, "--json")...))
			if j.Error.Code != c.code || j.Error.Exit != c.exit || j.Error.Message == "" {
				t.Fatalf("--json error object %+v, want code %s exit %d", *j.Error, c.code, c.exit)
			}
		})
	}
	if after := o.notes.Rev("refs/heads/main"); after != before {
		t.Fatalf("refused team commands push nothing: %s -> %s", before, after)
	}
	if st := o.app.Status(); st != "" {
		t.Fatalf("refused team commands leave the clone clean: %q", st)
	}
}
