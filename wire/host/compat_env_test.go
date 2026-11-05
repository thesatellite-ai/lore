package host

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestApplyEnvDefaults (B11 port of saas/pkg/aicoder/projresolve
// TestResolve_FlagOverridesEverything / TestResolve_EnvOverridesToml, the
// part that stays in 0.1.18): LORE_PROJECT_ID / LORE_REPO fill --project /
// --repo when the caller did not give them; a given flag always wins; an
// empty variable fills nothing; a command without the flag is untouched.
// The lore.toml/lore.yaml pointer step and LORE_DB are gone (--db refused).
func TestApplyEnvDefaults(t *testing.T) {
	cases := []struct {
		name                  string
		envProject, envRepo   string
		args                  []string
		wantProject, wantRepo string
	}{
		{"env fills both", "prj_env", "/r/env", nil, "prj_env", "/r/env"},
		{"flag beats env", "prj_env", "/r/env", []string{"--" + FlagProject, "prj_flag"}, "prj_flag", "/r/env"},
		{"both flags beat env", "prj_env", "/r/env", []string{"--" + FlagProject, "prj_flag", "--" + FlagRepo, "/r/flag"}, "prj_flag", "/r/flag"},
		{"empty env fills nothing", "", "", nil, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(EnvProjectID, c.envProject)
			t.Setenv(EnvRepo, c.envRepo)
			cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
			cmd.Flags().String(FlagProject, "", "")
			cmd.Flags().String(FlagRepo, "", "")
			if err := cmd.Flags().Parse(c.args); err != nil {
				t.Fatal(err)
			}
			applyEnvDefaults(cmd)
			p, _ := cmd.Flags().GetString(FlagProject) // declared above as a string flag
			r, _ := cmd.Flags().GetString(FlagRepo)    // declared above as a string flag
			if p != c.wantProject || r != c.wantRepo {
				t.Fatalf("project %q repo %q, want %q %q", p, r, c.wantProject, c.wantRepo)
			}
		})
	}
	t.Setenv(EnvProjectID, "prj_env")
	bare := &cobra.Command{Use: "y"}
	applyEnvDefaults(bare) // no scope flags: nothing to fill, no panic
	if bare.Flags().Lookup(FlagProject) != nil {
		t.Fatal("a command without --project gained one")
	}
}
