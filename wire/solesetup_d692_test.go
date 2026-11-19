package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// soleSetupBase is a machine with one solo setup and the org setups the
// test appends.
const soleSetupBase = `orgs:
  - name: acme
    identity: {key: "human:mira@acme.example"}
  - name: beta
    identity: {key: "human:mira@beta.example"}
setups:
  - name: personal
    kind: solo
    default: true
    folders: [~/www/mira/**]
    personal_store: none
  - name: acme-admin
    kind: admin
    org: acme
    folders: [~/code/acme/**]
    personal_store: none
`

// TestSoleOrgSetupRoutesD692: from a folder no org applies to, a command
// with no --setup runs on the only org setup of this machine and says so;
// with two org setups nothing is guessed (devco:D-692, D-683).
func TestSoleOrgSetupRoutesD692(t *testing.T) {
	t.Parallel()
	var said []string
	home := writeSetupsConfig(t, soleSetupBase)
	a := openWith(t, t.TempDir(), Options{Home: home, Notice: func(m string) { said = append(said, m) }})
	if got := a.soleOrgSetup(); got != "acme-admin" {
		t.Fatalf("soleOrgSetup = %q, want acme-admin (the solo setup has no org)", got)
	}
	ctx, ok := a.withSoleOrgSetup(background())
	if !ok || op.SetupOf(ctx) != "acme-admin" || len(said) != 1 || !strings.Contains(said[0], "setup acme-admin (the only org setup on this machine)") {
		t.Fatalf("withSoleOrgSetup: ok=%v setup=%q notices=%q, want acme-admin named", ok, op.SetupOf(ctx), said)
	}
	if _, ok = a.withSoleOrgSetup(op.WithSetup(background(), "personal")); ok {
		t.Error("a named --setup was overridden")
	}

	two := writeSetupsConfig(t, soleSetupBase+"  - name: beta-member\n    kind: member\n    org: beta\n    folders: [~/code/beta/**]\n    personal_store: none\n")
	b := openWith(t, t.TempDir(), Options{Home: two})
	if got := b.soleOrgSetup(); got != "" {
		t.Fatalf("soleOrgSetup with two org setups = %q, want none", got)
	}
}

// TestTaskNextOutsideNamesStoreAndSetupD683: plain task next where no code
// repo, store clone or single org setup decides the queue names the two
// ways that work from here (--store <org>, --setup <setup>) with this
// machine's orgs and setups, never "--store does not apply" (devco:D-683:
// the member was told --store does not apply while `task next --store
// <org>` ranked the store's tasks).
func TestTaskNextOutsideNamesStoreAndSetupD683(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, soleSetupBase+"  - name: beta-member\n    kind: member\n    org: beta\n    folders: [~/code/beta/**]\n    personal_store: none\n")
	a := openWith(t, t.TempDir(), Options{Home: home})
	_, err := run(t, a, opscoord.OpTaskNext, `{}`, false)
	var le *errs.Error
	if !errors.As(err, &le) || le.Code != errs.Unsupported {
		t.Fatalf("task next outside: want E_UNSUPPORTED, got %v", err)
	}
	for _, want := range []string{"lore task next --store <org>` (orgs: acme, beta)", "lore task next --setup <setup>` (setups: acme-admin, beta-member)", "cd into the code repo"} {
		if !strings.Contains(le.Hint, want) {
			t.Errorf("hint %q lacks %q", le.Hint, want)
		}
	}
	if strings.Contains(le.Hint, "does not apply") {
		t.Errorf("hint says --store does not apply although --store works here: %q", le.Hint)
	}
}
