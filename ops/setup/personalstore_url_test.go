package setup

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestPersonalStoreURLOnlyJudgedWhenUsed (B1-27): the page fills store_url
// for every personal_store choice (inherit shows personal.store, which may
// be the v0.1.17 value "local"); only create and existing use it, so only
// they may be refused for it.
func TestPersonalStoreURLOnlyJudgedWhenUsed(t *testing.T) {
	t.Parallel()
	bad := []onboard.FieldError{{Question: onboard.QIDStoreURL, Message: "must be git@host:owner/repo"}, {Question: onboard.QIDPush, Message: "x"}}
	for choice, want := range map[string]int{
		onboard.PersonalStoreInherit: 1, onboard.PersonalStoreLocal: 1, onboard.PersonalStoreNone: 1,
		onboard.PersonalStoreCreate: 2, onboard.PersonalStoreExisting: 2,
	} {
		ans := onboard.Answers{onboard.QIDPersonalStore: {Text: choice}, onboard.QIDStoreURL: {Text: "local"}}
		if got := withoutUnusedStoreURL(bad, ans); len(got) != want {
			t.Errorf("%s: %d errors left, want %d (%+v)", choice, len(got), want, got)
		}
	}
	if len(bad) != 2 {
		t.Fatal("the input slice was changed")
	}
}
