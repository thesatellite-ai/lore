package wire_test

// setupresave_regress_test.go — saving a step of a finished setup again
// without changing anything is a no-op on config.yaml (round-3 finding:
// the identity step dropped identity.emails when it repeated the key's
// address, so every unchanged save rewrote the entry and the setup's kept
// checks no longer matched it).

import "testing"

func TestUnchangedResaveWritesNothing(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
	a := w.Accounts[0]
	// As a hand-written or older entry has it: emails repeats the key's
	// address. (The top-level identity is there already: the first default
	// solo setup's save fills it once when empty, 4.2.)
	w.WriteConfig("identity:\n  key: \"human:" + a.Email + "\"\n  name: Pat Example\n" + rwPersonalConfig(a.Login, a.Email))
	before := w.Config()
	if err := w.Apply("personal", "identity", rwAnswers{"name": str("Pat Example"), "email": str(a.Email)}); err != nil {
		t.Fatalf("identity re-save: %v", err)
	}
	if got := w.Config(); got != before {
		t.Fatalf("an unchanged identity save rewrote config.yaml:\nbefore:\n%s\nafter:\n%s", before, got)
	}

	// The GitHub step fills the route and key fingerprint it probed once;
	// a second unchanged save keeps them as they are.
	gh := rwAnswers{"github_account": str(a.Login)}
	if err := w.Apply("personal", "github", gh); err != nil {
		t.Fatalf("github save: %v", err)
	}
	once := w.Config()
	if err := w.Apply("personal", "github", gh); err != nil {
		t.Fatalf("github re-save: %v", err)
	}
	if got := w.Config(); got != once {
		t.Fatalf("a second unchanged GitHub save rewrote config.yaml:\nfirst:\n%s\nsecond:\n%s", once, got)
	}
}
