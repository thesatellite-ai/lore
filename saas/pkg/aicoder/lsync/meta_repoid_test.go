package lsync

import "testing"

// TestResolveRepoIdentityDocs pins Q5 on the repo's own _meta.json merge:
// a fork merged into its upstream keeps ours, other keys are untouched.
func TestResolveRepoIdentityDocs(t *testing.T) {
	up := "rep_01a1a8059a3d72b5b329a56217d97cb7"
	fork := "rep_01a1a8059af47955bcb30258ec041f70"
	base := map[string]any{"project_id": "prj_x", "repo_id": up, "urls": []any{"github.com/agency/x"}}
	ours := map[string]any{"project_id": "prj_x", "repo_id": up, "urls": []any{"github.com/agency/x"}, "extra": "o"}
	theirs := map[string]any{"project_id": "prj_x", "repo_id": fork, "forked_from": up, "urls": []any{"github.com/tomas/x"}}
	ResolveRepoIdentity(base, ours, theirs)
	for name, d := range map[string]map[string]any{"base": base, "ours": ours, "theirs": theirs} {
		if d["repo_id"] != up || d["forked_from"] != nil {
			t.Fatalf("%s: identity must be ours: %v", name, d)
		}
	}
	if ours["extra"] != "o" || theirs["project_id"] != "prj_x" {
		t.Fatalf("other keys must stay: %v %v", ours, theirs)
	}
}
