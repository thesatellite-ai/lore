package identity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"saas/pkg/aicoder/lorecfg"
)

// F10 rule 6 / Q7: the no-org fallback never borrows an org identity key.
// Dimensions: orgs configured (none / some) × top-level identity (absent /
// set) × where the work key shows up (git email, identity.yaml, LORE_ACTOR).
func TestResolveNoOrgMatrix(t *testing.T) {
	const (
		workKey     = "human:alice@northwind.example"
		personalKey = "human:alice@personal.example"
		user        = "alice"
	)
	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	osKey := "human:" + user + "@" + host

	orgs := []lorecfg.Org{
		{Name: "northwind", Identity: lorecfg.OrgIdentity{Key: workKey}},
		{Name: "betalabs", Identity: lorecfg.OrgIdentity{Key: "human:alice@betalabs.example"}},
	}
	withTop := func(key string) *lorecfg.Config {
		return &lorecfg.Config{Orgs: orgs, Identity: lorecfg.OrgIdentity{Key: key, Name: "Alice Doe"}}
	}

	cases := []struct {
		name     string
		cfg      *lorecfg.Config
		gitEmail string // "" = unset
		fileKey  string // ~/.lore/identity.yaml stable_key, "" = no file
		envActor string
		wantKey  string
		wantStep Step
		wantHint bool
		wantIn   []string // substrings the single note must contain
	}{
		{name: "nil config: legacy chain unchanged", cfg: nil, gitEmail: "alice@northwind.example", wantKey: workKey, wantStep: StepGitConfig},
		{name: "no orgs: legacy chain unchanged", cfg: &lorecfg.Config{}, gitEmail: "alice@northwind.example", wantKey: workKey, wantStep: StepGitConfig},
		{name: "orgs + top-level identity: top wins over git email", cfg: withTop(personalKey), gitEmail: "alice@northwind.example", wantKey: personalKey, wantStep: StepTopConfig},
		{name: "orgs + top-level: an agent LORE_ACTOR still wins", cfg: withTop(personalKey), envActor: "agent:fe-2", wantKey: "agent:fe-2", wantStep: StepEnv},
		{name: "orgs + top-level: LORE_ACTOR = org key is skipped", cfg: withTop(personalKey), envActor: workKey, wantKey: personalKey, wantStep: StepTopConfig},
		{name: "orgs, no top: git email = org key is skipped → OS user", cfg: &lorecfg.Config{Orgs: orgs}, gitEmail: "alice@northwind.example", wantKey: osKey, wantStep: StepOSUser, wantHint: true, wantIn: []string{"your git email is an org identity (northwind) and was skipped"}},
		{name: "orgs, no top: identity file and git email both org keys → OS user", cfg: &lorecfg.Config{Orgs: orgs}, fileKey: "human:alice@betalabs.example", gitEmail: "alice@northwind.example", wantKey: osKey, wantStep: StepOSUser, wantHint: true, wantIn: []string{"your ~/.lore/identity.yaml and git email are org identities (betalabs, northwind) and were skipped"}},
		{name: "orgs, no top: personal git email is used, hint still shown", cfg: &lorecfg.Config{Orgs: orgs}, gitEmail: "alice@personal.example", wantKey: personalKey, wantStep: StepGitConfig, wantHint: true, wantIn: []string{"no top-level identity"}},
		{name: "orgs, no top: LORE_ACTOR = org key skipped → git email", cfg: &lorecfg.Config{Orgs: orgs}, envActor: workKey, gitEmail: "alice@personal.example", wantKey: personalKey, wantStep: StepGitConfig, wantHint: true, wantIn: []string{"your LORE_ACTOR is an org identity (northwind) and was skipped"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USER", user)
			t.Setenv("USERNAME", "")
			t.Setenv(EnvActor, c.envActor)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			gitcfg := filepath.Join(home, ".gitconfig")
			t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
			if c.gitEmail != "" {
				if out, err := exec.Command("git", "config", "--file", gitcfg, "user.email", c.gitEmail).CombinedOutput(); err != nil {
					t.Fatalf("git config: %v %s", err, out)
				}
			}
			if c.fileKey != "" {
				if err := os.MkdirAll(filepath.Join(home, ".lore"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".lore", "identity.yaml"), []byte("stable_key: "+c.fileKey+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(home) // no repo-local git config can leak in

			r := ResolveNoOrg(NoOrgInputs{Config: c.cfg})
			if r.StableKey != c.wantKey || r.Step != c.wantStep {
				t.Fatalf("got %s via %s, want %s via %s (notes %q)", r.StableKey, r.Step, c.wantKey, c.wantStep, r.Notes)
			}
			if c.cfg != nil {
				for _, k := range c.cfg.OrgKeys() {
					if r.StableKey == k {
						t.Fatalf("no-org identity borrowed org key %s", k)
					}
				}
			}
			// Exactly one note on the no-top-level path, none otherwise; it
			// never repeats an org identity key (E08), and names the orgs
			// and sources it skipped.
			wantNotes := 0
			if c.wantHint {
				wantNotes = 1
			}
			if len(r.Notes) != wantNotes {
				t.Fatalf("notes = %q, want %d line(s)", r.Notes, wantNotes)
			}
			notes := strings.Join(r.Notes, "\n")
			if c.wantHint && !strings.Contains(notes, topIdentityFix) {
				t.Errorf("note %q lacks the fix %q", notes, topIdentityFix)
			}
			for _, o := range orgs {
				if strings.Contains(notes, o.Identity.Key) {
					t.Errorf("note %q repeats org key %s", notes, o.Identity.Key)
				}
			}
			for _, want := range c.wantIn {
				if !strings.Contains(notes, want) {
					t.Errorf("note %q lacks %q", notes, want)
				}
			}
		})
	}
}
