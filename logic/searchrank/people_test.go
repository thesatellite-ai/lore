package searchrank

import "testing"

// TestAsksAboutPeople (devco:D-1034): whole words of a question about
// people, in any case; a word that only starts like one is not.
func TestAsksAboutPeople(t *testing.T) {
	for q, want := range map[string]bool{
		"Who owns the billing page?":                     true,
		"which TEAM runs the nightly export":             true,
		"billing page (owner: finance)":                  true,
		"how are invoice totals rounded before tax?":     false,
		"how does the export handle a failed upload?":    false,
		"what is the teamwork policy for owned-by rules": true,
		"does the ownerless queue drain at night":        false,
		"": false,
	} {
		if got := AsksAboutPeople(q); got != want {
			t.Errorf("AsksAboutPeople(%q) = %v, want %v", q, got, want)
		}
	}
}
