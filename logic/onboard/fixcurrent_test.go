package onboard

import (
	"slices"
	"testing"
)

// TestFixCurrentEveryTemplate: every catalog fix, filled the two ways a
// saved row can hold it (FillFix with valid values, TextFix with none), is
// current; so a row saved by this binary is never replaced on open.
func TestFixCurrentEveryTemplate(t *testing.T) {
	for k, tmpl := range fixes {
		vars := map[string]string{}
		for _, p := range Placeholders(tmpl) {
			vars[p] = sampleValue[placeholderKinds[p]]
			if v, ok := samplePlaceholder[p]; ok {
				vars[p] = v
			}
		}
		stepID := k.step
		if stepID == "" {
			stepID, _ = StepOfCheck(k.check, CheckKinds(k.check)[0])
		}
		step, _ := StepByID(stepID)
		filled, err := FillFix(k.check, tmpl, vars, step)
		if err != nil {
			t.Fatalf("%+v does not fill: %v", k, err) // TestEveryFixPlaceholderFillable says why
		}
		if !FixCurrent(k.check, filled) {
			t.Errorf("%s/%s/%s filled %+v: not current", k.check, k.step, k.variant, filled)
		}
		if text := TextFix(tmpl, nil); !FixCurrent(k.check, text) {
			t.Errorf("%s/%s/%s text-only %+v: not current", k.check, k.step, k.variant, text)
		}
	}
}

// TestFixCurrentRefusesWhatTheCatalogNoLongerGives: the dimensions a saved
// fix can drift in (wording, command, runnable flag, a check without a
// template) each make it not current; an empty fix (a passing row) always is.
func TestFixCurrentRefusesWhatTheCatalogNoLongerGives(t *testing.T) {
	keyPair := FixFor(CheckGitHubKeyMatches, "", FixKeyPair)
	vars := map[string]string{VarSetup: "personal", VarFixAlias: "github-personal", VarFixKey: "~/.ssh/id_personal.pub", VarUser: "octo-personal"}
	step, _ := StepByID(StepGitHub)
	good, err := FillFix(CheckGitHubKeyMatches, keyPair, vars, step)
	if err != nil {
		t.Fatal(err)
	}
	reworded := good
	reworded.Text = "Old wording: switch to github-personal"
	otherCmd := good
	otherCmd.Command = append(slices.Clone(good.Command[:len(good.Command)-2]), "--route", "https")
	textOnlyButRunnable := TextFix(FixFor(CheckGitHubSSHUser, "", FixDefault), vars)
	textOnlyButRunnable.Runnable = true
	shorter := good
	shorter.Command = good.Command[:3]
	for _, c := range []struct {
		name  string
		check CheckID
		fix   Fix
		want  bool
	}{
		{"filled today", CheckGitHubKeyMatches, good, true},
		{"empty fix (passing row)", CheckGitHubKeyMatches, Fix{Command: []string{}}, true},
		{"empty fix, unknown check", CheckID("gone.check"), Fix{}, true},
		{"reworded text", CheckGitHubKeyMatches, reworded, false},
		{"other command", CheckGitHubKeyMatches, otherCmd, false},
		{"command cut short", CheckGitHubKeyMatches, shorter, false},
		{"runnable where the template is not", CheckGitHubSSHUser, textOnlyButRunnable, false},
		{"text of another check", CheckStoreURL, good, false},
		{"check without a template", CheckID("gone.check"), good, false},
	} {
		if got := FixCurrent(c.check, c.fix); got != c.want {
			t.Errorf("%s: FixCurrent = %v, want %v (%+v)", c.name, got, c.want, c.fix)
		}
	}
}

// TestCurrentFixesKeepsTheRow: only the fix of a stale row changes; nil
// stays nil (a row saved with no checks marshals as before).
func TestCurrentFixesKeepsTheRow(t *testing.T) {
	if CurrentFixes(nil) != nil {
		t.Fatal("CurrentFixes(nil) != nil")
	}
	in := []CheckResult{
		{Check: CheckStoreURL, Status: StatusFail, Level: LevelError, Detail: "d", Fix: Fix{Text: "an old fix", Command: []string{"lore", "old"}, Runnable: true}},
		{Check: CheckStoreURL, Status: StatusOK, Level: LevelError},
	}
	out := CurrentFixes(in)
	if out[0].Fix.Text != staleFixText || out[0].Fix.Runnable || out[0].Fix.Command == nil || len(out[0].Fix.Command) != 0 {
		t.Errorf("stale row fix = %+v, want StaleFix", out[0].Fix)
	}
	if out[0].Status != StatusFail || out[0].Detail != "d" || out[1].Status != StatusOK {
		t.Errorf("row changed beyond its fix: %+v", out)
	}
	if in[0].Fix.Text != "an old fix" {
		t.Error("CurrentFixes changed its input")
	}
}
