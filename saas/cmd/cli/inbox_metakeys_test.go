package main

import (
	"testing"

	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaReviewText pins the review-queue line (METADATA 3.14 sample):
// count, distinct authors in first-seen order, names once each, the hint;
// the singular; and a key row without a writer (no "added by").
func TestMetaReviewText(t *testing.T) {
	who := map[string]string{"act_a": "tomas via codex", "act_b": "mira via claude_code"}
	author := func(id string) string { return who[id] }
	cases := []struct {
		name string
		keys []opssettings.UnreviewedKey
		want string
	}{
		{"design sample", []opssettings.UnreviewedKey{{Name: "customer_tier", CreatedBy: "act_a"}, {Name: "renewal_risk", CreatedBy: "act_a"}, {Name: "root_cause", CreatedBy: "act_a"}},
			"3 keys await review (added by tomas via codex): customer_tier, renewal_risk, root_cause — lore meta keys --unreviewed"},
		{"two authors, a name in two stores", []opssettings.UnreviewedKey{{Name: "a", CreatedBy: "act_b", Store: "repo"}, {Name: "a", CreatedBy: "act_a", Store: "kb"}, {Name: "b", CreatedBy: "act_a"}},
			"2 keys await review (added by mira via claude_code, tomas via codex): a, b — lore meta keys --unreviewed"},
		{"one key", []opssettings.UnreviewedKey{{Name: "solo", CreatedBy: "act_a"}},
			"1 key awaits review (added by tomas via codex): solo — lore meta keys --unreviewed"},
		{"no writer", []opssettings.UnreviewedKey{{Name: "old"}},
			"1 key awaits review: old — lore meta keys --unreviewed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := metaReviewText(c.keys, author); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}
