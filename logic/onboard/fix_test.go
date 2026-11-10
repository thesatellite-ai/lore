package onboard

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// sampleValue is a valid value of each kind, used to prove every
// placeholder of every fix can be filled.
var sampleValue = map[valueKind]string{
	vkText:        "committed .lore/org.yaml",
	vkName:        "mira-agency",
	vkSetup:       "acme-hub",
	vkEmail:       "mira@agency.example",
	vkPath:        "~/www/projects/agency/**",
	vkURL:         "git@github.com:agency/lore-team.git",
	vkID:          "inv_01jfa0c3",
	vkVersion:     "0.1.18",
	vkFingerprint: "SHA256:9fQx2Kd2",
	vkProfiles:    "claude_code,codex",
}

// samplePlaceholder overrides sampleValue where a step question's rules
// are stricter than the value kind (setup name, a team name the org and
// its store both take, .pub key).
var samplePlaceholder = map[string]string{
	VarName:   "personal",
	VarOrg:    "acmehub",
	VarSSHKey: "~/.ssh/id_agency.pub",
}

// TestEveryFixPlaceholderFillable: every placeholder of every fix row is a
// known placeholder, display-only values never reach an argv, and the fix
// fills cleanly from valid values in its step.
func TestEveryFixPlaceholderFillable(t *testing.T) {
	for k, f := range fixes {
		vars := map[string]string{}
		for _, p := range Placeholders(f) {
			vk, ok := placeholderKinds[p]
			if !ok {
				t.Errorf("fix %+v: placeholder {%s} is not in placeholderKinds", k, p)
				continue
			}
			vars[p] = sampleValue[vk]
			if v, ok := samplePlaceholder[p]; ok {
				vars[p] = v
			}
		}
		for _, a := range f.Command {
			for _, m := range placeholderRe.FindAllStringSubmatch(a, -1) {
				if placeholderKinds[m[1]] == vkText {
					t.Errorf("fix %+v: display-only {%s} inside the command", k, m[1])
				}
			}
		}
		stepID := k.step
		if stepID == "" {
			stepID, _ = StepOfCheck(k.check, CheckKinds(k.check)[0])
		}
		step, _ := StepByID(stepID)
		got, err := FillFix(k.check, f, vars, step)
		if err != nil {
			t.Errorf("fix %+v does not fill from valid values: %v", k, err)
			continue
		}
		if strings.Contains(got.Text, "{") && placeholderRe.MatchString(got.Text) {
			t.Errorf("fix %+v left a placeholder: %q", k, got.Text)
		}
		if got.Command == nil {
			t.Errorf("fix %+v: Command nil", k)
		}
	}
}

func TestFillFixFills(t *testing.T) {
	step, _ := StepByID(StepPersonalStore)
	got, err := FillFix(CheckStoreRead, FixFor(CheckStoreRead, StepPersonalStore, FixDefault),
		map[string]string{VarOwner: "mira", VarRepo: "lore-me"}, step)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gh", "repo", "create", "mira/lore-me", "--private"}; !slices.Equal(got.Command, want) {
		t.Errorf("argv = %q, want %q", got.Command, want)
	}
	if !got.Runnable || !got.Outward || !strings.Contains(got.Text, "gh repo create mira/lore-me --private") {
		t.Errorf("filled = %+v", got)
	}
	// A fix with no command fills to [] (JSON prints []).
	got, err = FillFix(CheckStoreURL, FixFor(CheckStoreURL, "", FixDefault), nil, step)
	if err != nil || got.Command == nil || len(got.Command) != 0 {
		t.Errorf("no-command fix = %+v, %v", got, err)
	}
	owner, repo, ok := OwnerRepo("git@github.com:agency/lore-team.git")
	if !ok || owner != "agency" || repo != "lore-team" {
		t.Errorf("OwnerRepo = %s %s %v", owner, repo, ok)
	}
}

// TestFillFixRefusesInjection is the guard of ONB 5.3: a value that could
// change what a fix runs, or put a token on screen, refuses the whole fix.
func TestFillFixRefusesInjection(t *testing.T) {
	ps, _ := StepByID(StepPersonalStore)
	tools, _ := StepByID(StepTools)
	join, _ := StepByID(StepJoin)
	kind, _ := StepByID(StepKind)
	this, _ := StepByID(StepThisFolder)
	team, _ := StepByID(StepTeam)
	cases := []struct {
		name  string
		check CheckID
		fix   Fix
		vars  map[string]string
		step  Step
		field string
	}{
		{"shell in owner", CheckStoreRead, FixFor(CheckStoreRead, StepPersonalStore, FixDefault),
			map[string]string{VarOwner: "mira; rm -rf ~", VarRepo: "lore-me"}, ps, VarOwner},
		{"option in repo", CheckStoreRead, FixFor(CheckStoreRead, StepPersonalStore, FixDefault),
			map[string]string{VarOwner: "mira", VarRepo: "--upload-pack=touch /tmp/x"}, ps, VarRepo},
		{"option in tool", CheckToolsSkillInstalled, FixFor(CheckToolsSkillInstalled, StepTools, FixDefault),
			map[string]string{VarTool: "--upload-pack=x"}, tools, VarTool},
		{"newline in tool", CheckToolsSkillInstalled, FixFor(CheckToolsSkillInstalled, StepTools, FixDefault),
			map[string]string{VarTool: "codex\nrm -rf ~"}, tools, VarTool},
		{"token url in text", CheckStoreRead, FixFor(CheckStoreRead, StepJoin, FixDefault),
			map[string]string{VarStore: userinfoURL("https", "mira", "ghp_x1y2", "github.com/agency/lore-team.git"), VarBy: "tomas@agency.example", VarGitHubUser: "mira"}, join, VarStore},
		{"https userinfo token", CheckStoreRead, FixFor(CheckStoreRead, StepJoin, FixDefault),
			map[string]string{VarStore: "https://ghp_" + "x1y2" + "@github.com/agency/lore-team.git", VarBy: "tomas@agency.example", VarGitHubUser: "mira"}, join, VarStore},
		{"shell in email argv", CheckIdentityGitEmail, FixFor(CheckIdentityGitEmail, StepIdentity, FixDefault),
			map[string]string{VarEmail: "mira@agency.example; rm -rf ~", VarGitEmail: "mira@agency.example"}, Step{}, VarEmail},
		{"shell in repo path", CheckIdentityGitEmail, FixFor(CheckIdentityGitEmail, StepThisFolder, FixRepo),
			map[string]string{VarRepoRoot: "/w/acme; rm -rf /", VarEmail: "mira@agency.example", VarGitEmail: "x@agency.example"}, this, VarRepoRoot},
		{"option as repo path", CheckIdentityGitEmail, FixFor(CheckIdentityGitEmail, StepThisFolder, FixRepo),
			map[string]string{VarRepoRoot: "--exec=sh", VarEmail: "mira@agency.example", VarGitEmail: "x@agency.example"}, this, VarRepoRoot},
		{"bad invite id", CheckInvitePending, FixFor(CheckInvitePending, StepTeam, FixDefault),
			map[string]string{VarID: "inv_1 --all", VarInviteEmail: "tomas@agency.example"}, team, VarID},
		{"missing value", CheckToolsSkillInstalled, FixFor(CheckToolsSkillInstalled, StepTools, FixDefault),
			map[string]string{}, tools, VarTool},
		// Display-only {name} still gets the kind step's setup_name rule.
		{"question rule", CheckSetupNameFree, FixFor(CheckSetupNameFree, StepKind, FixDefault),
			map[string]string{VarName: "Acme Hub"}, kind, VarName},
		{"unknown placeholder", CheckStoreURL, Fix{Text: "x {nope}", Command: []string{}}, map[string]string{"nope": "a"}, ps, "nope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FillFix(c.check, c.fix, c.vars, c.step)
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.FixRefused {
				t.Fatalf("FillFix = %+v, %v; want E_FIX_REFUSED", got, err)
			}
			if want := "fix for " + string(c.check) + " refused: " + c.field + " "; !strings.HasPrefix(e.Message, want) {
				t.Errorf("message %q, want prefix %q", e.Message, want)
			}
		})
	}
}

func TestAnswersVars(t *testing.T) {
	got := AnswersVars(Answers{
		"email":   {Text: "mira@agency.example"},
		"tools":   {List: []string{"claude_code", "codex"}, IsList: true},
		"default": {Bool: true, IsBool: true},
	})
	if got["email"] != "mira@agency.example" || got["tools"] != "claude_code, codex" || got["default"] != "true" {
		t.Errorf("AnswersVars = %v", got)
	}
}

// userinfoURL builds a url carrying user:secret userinfo at run time, so no
// source line holds a credential-shaped literal (AGENTS.md: fixtures that
// look like keys are built at run time).
func userinfoURL(scheme, user, secret, rest string) string {
	return scheme + "://" + user + ":" + secret + "@" + rest
}

// TestInitTargetsValue: the repo.init fix takes the setup tools as one
// --target word of profile names; anything else is refused before it can
// reach an argv (a flag, a shell word, a path, an empty list).
func TestInitTargetsValue(t *testing.T) {
	f := FixFor(CheckRepoInit, StepThisFolder, FixInitTargets)
	step, _ := StepByID(StepThisFolder)
	got, err := FillFix(CheckRepoInit, f, map[string]string{VarTargets: "claude_code,codex"}, step)
	if err != nil || strings.Join(got.Command, " ") != "lore init --target claude_code,codex" {
		t.Fatalf("fill = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "-x", "claude_code;rm", "a,,b", "Claude", "../x", "a b", "claude_code,"} {
		if _, err := FillFix(CheckRepoInit, f, map[string]string{VarTargets: bad}, step); err == nil {
			t.Errorf("targets %q filled; want refused", bad)
		}
	}
}

// TestRecordedKeyFixNamesTheRunnableFix (realmac m1): github.ssh_user's
// recorded-key fix is text only and sends the person to
// github.key_matches' Run it. That Run it either drops the recorded key or
// records another alias and its .pub (wire keyMatches), so the text must
// name the check and promise neither outcome: the walk found it saying
// "drops the recorded key" beside a Run it that recorded another alias.
func TestRecordedKeyFixNamesTheRunnableFix(t *testing.T) {
	f := FixFor(CheckGitHubSSHUser, "", FixRecordedKey)
	if f.Runnable || len(f.Command) != 0 {
		t.Errorf("recorded-key fix is runnable or has a command: %+v (one runnable remedy per cause: key_matches')", f)
	}
	if !strings.Contains(f.Text, string(CheckGitHubKeyMatches)) {
		t.Errorf("recorded-key fix %q does not name %s", f.Text, CheckGitHubKeyMatches)
	}
	for _, outcome := range []string{"drops", "--no-ssh-key", "{alias}'s own key", "{fix_alias}"} {
		if strings.Contains(f.Text, outcome) {
			t.Errorf("recorded-key fix %q promises one outcome (%q) of a fix that has two", f.Text, outcome)
		}
	}
	for _, v := range []FixVariant{FixKeyPair, FixKeyAliasOnly} {
		if km := FixFor(CheckGitHubKeyMatches, "", v); !km.Runnable {
			t.Errorf("github.key_matches %s is not runnable; the recorded-key fix points at it", v)
		}
	}
}

// TestWithKeyKeepsTheOldKey: a new key on the same identity appends the old
// one to Aliases once, oldest first; the same key changes nothing.
func TestWithKeyKeepsTheOldKey(t *testing.T) {
	t.Parallel()
	id := SetupIdentity{Key: "human:a@x", Name: "A"}
	same := id.WithKey("human:a@x")
	if len(same.Aliases) != 0 || same.Key != "human:a@x" {
		t.Errorf("same key: %+v", same)
	}
	two := id.WithKey("human:b@x").WithKey("human:c@x").WithKey("human:a@x")
	if two.Key != "human:a@x" || !slices.Equal(two.Aliases, []string{"human:a@x", "human:b@x", "human:c@x"}) {
		t.Errorf("a→b→c→a: %+v, want key a and aliases [a b c] (nothing removed, the first key stays first)", two)
	}
	if len(id.Aliases) != 0 {
		t.Errorf("WithKey changed its receiver: %+v", id)
	}
}
