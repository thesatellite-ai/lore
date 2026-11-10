package wire_test

// setupkey_regress_test.go — the GitHub step and setup.edit accept a
// private key path with no .pub beside it.

import (
	"strings"
	"testing"
)

// Finding 4cf415be / owner test 2026-11-07: the owner's github.com key has
// no .pub, the page offered (and the user picked) ~/.ssh/id_ed25519, and
// lore refused the right answer with "must be a public key path ending in
// .pub; lore never records a private key path".
func TestRegress_PrivateKeyWithoutPubAccepted(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only", NoPub: true})
	a := rwAllAccounts[0]
	w.WriteConfig(rwPersonalConfig(a.Login, a.Email))
	key := "~/.ssh/" + a.Key

	out := w.Check("personal", "github", rwAnswers{"github_account": str(a.Login), "host_alias": str("github.com"), "ssh_key": str(key)})
	for _, fe := range out.FieldErrors {
		if fe.Question == "ssh_key" {
			t.Fatalf("github step refused the private key path %s: %+v", key, fe)
		}
	}

	var edited rwNone
	if err := rwRun(w, "setup.edit", map[string]string{"name": "personal", "ssh_key": key}, &edited); err != nil {
		t.Fatalf("setup edit --ssh-key %s refused: %v", key, err)
	}
	if cfg := w.Config(); !strings.Contains(cfg, "ssh_key: "+key) {
		t.Fatalf("config.yaml does not hold the key path:\n%s", cfg)
	}
	w.Reopen()
	rwMust(w, "setup.show", map[string]string{"name": "personal"}, &edited)
}
