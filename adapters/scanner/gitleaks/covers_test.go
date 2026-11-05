package gitleaks

import "testing"

// TestCoversIDsExist (SECRETS 3.3, `lore secrets rules --replaced`): every
// key of Covers is a lore pack rule and every gitleaks id it names is a
// rule of the loaded gitleaks default config, so a gitleaks upgrade that
// renames or drops one fails here instead of printing a dead id.
func TestCoversIDsExist(t *testing.T) {
	t.Parallel()
	cfg, err := gitleaksDefaults()
	if err != nil {
		t.Fatal(err)
	}
	pack := map[string]bool{}
	for _, r := range Pack() {
		pack[r.ID] = true
	}
	covers := Covers()
	if len(covers) == 0 {
		t.Fatal("Covers is empty")
	}
	for loreID, ids := range covers {
		if !pack[loreID] {
			t.Errorf("Covers key %s is not a lore pack rule", loreID)
		}
		if len(ids) == 0 {
			t.Errorf("Covers[%s] is empty: a lore-only rule has no entry", loreID)
		}
		for _, id := range ids {
			if _, ok := cfg.Rules[id]; !ok {
				t.Errorf("Covers[%s] names %s, not a rule of the gitleaks default config", loreID, id)
			}
		}
	}
}
