package leakcheck

import (
	"regexp"
	"strings"
)

// profileTables are the team profile tables (TEAM_PROFILES_DESIGN 2.2-2.6):
// contacts, contact types, roles, company periods and project memberships.
// Every one is NeverClient in the schema (TS-3), so a row file of any of
// them under a client repo's .lore/data/ is a leak whatever it holds
// (TP-7: a client repo never holds a contact, period or membership row).
//
// The names repeat schema.ProfileTables() on purpose: this package is the
// commit check and stays free of the table registry (it imports only
// canonjson), the same way logic/formatgate repeats table names. A schema
// test pins this list equal to the registry's NeverClient tables, so a new
// NeverClient table cannot be forgotten here.
// invites (ONBOARDING_DESIGN 4.5, Q10) is org roster only as well.
var profileTables = []string{"actor_contacts", "contact_types", "roles", "actor_periods", "project_members", "invites"}

// ProfileTables returns the table names the commit check blocks in a
// client repo (a fresh slice; the schema pin test reads it).
func ProfileTables() []string { return append([]string(nil), profileTables...) }

// anyRowPathRe recognises any .lore/data/<table>/<name>.json, whatever the
// file name: a profile row file blocks even when its name is not an id of
// the usual shape (a hand-copied or renamed file still carries the row).
var anyRowPathRe = regexp.MustCompile(`(?:^|/)\.lore/data/([a-z_]+)/([^/]+)\.json$`)

// profileDetail is the reason of a profile row finding (TEAM_PROFILES 7).
const profileDetail = "team profile and invite rows live only in the org store; remove the file"

// profileRowFile reports the table and row id (the file name without
// .json) when path is a row file of a profile table.
func profileRowFile(path string) (table, id string, ok bool) {
	m := anyRowPathRe.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	for _, t := range profileTables {
		if m[1] == t {
			return m[1], m[2], true
		}
	}
	return "", "", false
}

// profileHit is the blocking finding of a profile row file in a client
// repo (TP-7, TEAM_PROFILES 7 layer 4): Rule actor, never only a warning.
func profileHit(it Item, table, id string) Finding {
	return Finding{Rule: RuleActor, Where: it.Where, RowID: id, Path: it.Path,
		Detail: table + " row " + id + " in client-repo file " + actorFileName(it) + ": " + profileDetail}
}

// profileCIHit is CheckCI's variant: it names the table, never the id (a CI
// finding never names an internal id, see CIFinding).
func profileCIHit(table string) CIFinding {
	return CIFinding{Where: table + " row file", Detail: "a client repo holds no " + strings.ReplaceAll(table, "_", " ") + " rows: " + profileDetail}
}
