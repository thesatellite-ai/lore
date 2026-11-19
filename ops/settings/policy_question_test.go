package settings

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/store"
)

// TestPolicyQuestionParts (devco D-603): the confirm question of policy
// set names where the row goes, who it reaches and the exact command that
// answers it with --yes ("policy set?" said none of them).
func TestPolicyQuestionParts(t *testing.T) {
	if got := policyCommand("set", "context.budget.internal", "22000", ""); got != "lore policy set --key context.budget.internal --value 22000 --yes" {
		t.Errorf("policyCommand = %q", got)
	}
	if got := policyCommand("set", "secrets.allow", "a, b", "acme/web"); got != "lore policy set --key secrets.allow --value 'a, b' --to acme/web --yes" {
		t.Errorf("policyCommand with a list = %q", got)
	}
	if got := policyCommand("unset", "trash.retention_days", "", "acme"); got != "lore policy unset --key trash.retention_days --to acme --yes" {
		t.Errorf("policyCommand unset = %q", got)
	}
	var o ops
	if got := o.policyPlace(store.SourceRepo); got != "this repo's .lore/ (committed with the repo)" {
		t.Errorf("policyPlace(repo) = %q", got)
	}
	for lv, want := range map[policy.Level]string{
		policy.LevelRepo:      "everyone working in this repo",
		policy.LevelPersonal:  "only you",
		policy.LevelFolder:    "every repo linked to acme/web",
		policy.LevelOrg:       "every folder and repo of acme (a folder may only tighten it)",
		policy.LevelStoreRoot: "every folder and repo of store acme/web",
	} {
		if got := policyReach(lv, "acme/web"); got != want {
			t.Errorf("policyReach(%s) = %q, want %q", lv, got, want)
		}
	}
}
