package leakcheck

import (
	"strings"
	"testing"
)

const profileRowID = "acn_01d9a000000000000000000000c00001"

// TP-7 (TEAM_PROFILES 7 layer 4): a row file of every team profile table
// under a client repo's .lore/data/ blocks in Check and CheckCI, whatever
// it holds and whatever its file name; outside a client repo Check leaves
// it alone (the org store is where these rows live).
func TestProfileRowFilesBlockInClientRepo(t *testing.T) {
	bodies := map[string]string{
		"actor_contacts":  `{"_table":"actor_contacts","actor_id":"` + actID + `","type":"email","value":"mira.patel@gmail.com","identity":true}`,
		"contact_types":   `{"_table":"contact_types","name":"bitbucket","kind":"handle"}`,
		"roles":           `{"_table":"roles","name":"ops"}`,
		"actor_periods":   `{"_table":"actor_periods","actor_id":"` + actID + `","type":"employee","started_on":"2026-01-12"}`,
		"project_members": `{"_table":"project_members","actor_id":"` + actID + `","project_id":"` + repoPID + `","roles":["dev"]}`,
		"invites":         `{"_table":"invites","email":"tomas@agency.example","period_type":"employee","status":"pending","expires_on":"2026-11-20"}`,
	}
	if len(bodies) != len(ProfileTables()) {
		t.Fatalf("matrix covers %d tables, the list has %d", len(bodies), len(ProfileTables()))
	}
	names := []string{profileRowID, "copied-by-hand"}
	prefixes := []string{".lore/data/", "web/.lore/data/"}
	for _, table := range ProfileTables() {
		for _, name := range names {
			for _, pre := range prefixes {
				path := pre + table + "/" + name + ".json"
				t.Run(path, func(t *testing.T) {
					it := Item{Where: path, Path: path, Content: []byte(bodies[table])}
					fs := Blocking(Check([]Item{it}, index(), Options{ClientRepo: true, RepoProjectID: repoPID}))
					var hit *Finding
					for i := range fs {
						if strings.Contains(fs[i].Detail, profileDetail) {
							hit = &fs[i]
						}
					}
					if hit == nil {
						t.Fatalf("profile row file must block: %+v", fs)
					}
					want := table + " row " + name + " in client-repo file " + path
					if hit.Rule != RuleActor || hit.Warn || !strings.HasPrefix(hit.Detail, want) {
						t.Fatalf("finding = %+v, want rule %s blocking, detail starting %q", hit, RuleActor, want)
					}
					if fs := Check([]Item{it}, nil, Options{}); len(fs) != 0 {
						t.Fatalf("outside a client repo the row is not a leak: %+v", fs)
					}
					ci := CheckCI(map[string][]byte{path: []byte(bodies[table])}, repoPID)
					if len(ci) != 1 || ci[0].Where != table+" row file" {
						t.Fatalf("CheckCI = %+v, want one %s finding", ci, table)
					}
					if strings.Contains(ci[0].Detail+ci[0].Where, name) || strings.Contains(ci[0].Detail, actID) {
						t.Fatalf("CI finding names the row: %+v", ci[0])
					}
				})
			}
		}
	}
}

// Look-alike paths are not profile rows: another table, a nested file, a
// non-JSON file, a table name that only starts like one.
func TestProfileRowFileMatcher(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".lore/data/actor_contacts/" + profileRowID + ".json", true},
		{"a/b/.lore/data/roles/x.json", true},
		{".lore/data/actors/" + actID + ".json", false},
		{".lore/data/actor_contacts_old/x.json", false},
		{".lore/data/actor_contacts/x.json.bak", false},
		{".lore/data/actor_contacts/sub/x.json", false},
		{"docs/actor_contacts/x.json", false},
		{"lore/data/roles/x.json", false},
	}
	for _, tc := range cases {
		if _, _, got := profileRowFile(tc.path); got != tc.want {
			t.Errorf("profileRowFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TEAM_PROFILES 7 layer 4: every new actor field (2.1) in a client actor
// file blocks through the existing allow-list, with no code change; each
// one is pinned here so a later allow-list edit cannot let one through.
func TestProfileActorFieldsBlockInClientActorFile(t *testing.T) {
	fields := map[string]string{
		"photo_url":   `"https://example.com/m.png"`,
		"timezone":    `"Europe/Istanbul"`,
		"location":    `"Ankara, TR"`,
		"title":       `"Backend engineer"`,
		"bio":         `"likes Go"`,
		"birthday":    `"0000-04-17"`,
		"status":      `"on_leave"`,
		"leave_from":  `"2026-06-01"`,
		"leave_until": `"2026-06-30"`,
		"status_note": `"parental leave"`,
	}
	for f, v := range fields {
		t.Run(f, func(t *testing.T) {
			body := `{"_table":"actors","id":"` + actID + `","display_name":"Mira","kind":"human","` + f + `":` + v + `}`
			path := ".lore/data/actors/" + actID + ".json"
			fs := Blocking(Check([]Item{{Where: path, Path: path, Content: []byte(body)}}, index(), Options{ClientRepo: true}))
			if len(fs) != 1 || fs[0].Rule != RuleActor || !strings.Contains(fs[0].Detail, f) {
				t.Fatalf("actor field %s must block naming it: %+v", f, fs)
			}
			ci := CheckCI(map[string][]byte{path: []byte(body)}, repoPID)
			if len(ci) != 1 || !strings.Contains(ci[0].Detail, f) {
				t.Fatalf("CheckCI must block %s: %+v", f, ci)
			}
		})
	}
}
