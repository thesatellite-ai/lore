package projresolve

import (
	"testing"
)

// TestResolve_ExplicitDBOutsideProject_HonoursEnvChain pins BUG-S1: with
// --db or LORE_DB outside a project folder, the project id and repo mount
// still follow flag > env (LORE_PROJECT_ID, LORE_REPO). Before the fix the
// env values were dropped and a database holding several projects stopped
// with E_AMBIGUOUS_PROJECT although LORE_PROJECT_ID named one.
func TestResolve_ExplicitDBOutsideProject_HonoursEnvChain(t *testing.T) {
	const (
		dbFlag   = "/flag/shared.db"
		dbEnv    = "/env/shared.db"
		prjFlag  = "prj_flag"
		prjEnv   = "prj_env"
		repoFlag = "api"
		repoEnv  = "web"
	)
	cases := []struct {
		name                 string
		flagDB, envDB        string
		flagPrj, envPrj      string
		flagRepo, envRepo    string
		wantDB, wantDBSource string
		wantPrj, wantPrjSrc  string
		wantRepo             string
	}{
		{name: "env db + env project", envDB: dbEnv, envPrj: prjEnv,
			wantDB: dbEnv, wantDBSource: "env", wantPrj: prjEnv, wantPrjSrc: "env"},
		{name: "flag db + env project", flagDB: dbFlag, envPrj: prjEnv,
			wantDB: dbFlag, wantDBSource: "flag", wantPrj: prjEnv, wantPrjSrc: "env"},
		{name: "flag project beats env project", envDB: dbEnv, flagPrj: prjFlag, envPrj: prjEnv,
			wantDB: dbEnv, wantDBSource: "env", wantPrj: prjFlag, wantPrjSrc: "flag"},
		{name: "flag db beats env db", flagDB: dbFlag, envDB: dbEnv,
			wantDB: dbFlag, wantDBSource: "flag", wantPrjSrc: "local"},
		{name: "no project anywhere is local (caller picks the single one)", envDB: dbEnv,
			wantDB: dbEnv, wantDBSource: "env", wantPrjSrc: "local"},
		{name: "env repo is read", envDB: dbEnv, envRepo: repoEnv,
			wantDB: dbEnv, wantDBSource: "env", wantPrjSrc: "local", wantRepo: repoEnv},
		{name: "flag repo beats env repo", envDB: dbEnv, flagRepo: repoFlag, envRepo: repoEnv,
			wantDB: dbEnv, wantDBSource: "env", wantPrjSrc: "local", wantRepo: repoFlag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir()) // not a project folder
			t.Setenv(EnvDB, tc.envDB)
			t.Setenv(EnvProjectID, tc.envPrj)
			t.Setenv(EnvRepo, tc.envRepo)
			c, err := Resolve(Inputs{FlagDB: tc.flagDB, FlagProject: tc.flagPrj, FlagRepo: tc.flagRepo})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if c.DBPath != tc.wantDB || c.SourceDB != tc.wantDBSource {
				t.Errorf("db = %q (%s), want %q (%s)", c.DBPath, c.SourceDB, tc.wantDB, tc.wantDBSource)
			}
			if c.ProjectID != tc.wantPrj || c.SourceProject != tc.wantPrjSrc {
				t.Errorf("project = %q (%s), want %q (%s)", c.ProjectID, c.SourceProject, tc.wantPrj, tc.wantPrjSrc)
			}
			if c.RepoMount != tc.wantRepo {
				t.Errorf("repo = %q, want %q", c.RepoMount, tc.wantRepo)
			}
		})
	}
}
