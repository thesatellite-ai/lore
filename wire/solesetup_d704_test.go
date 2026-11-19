package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// d704Base is a machine with two org setups and no solo setup; the cases
// add the default and the kinds.
const d704Base = `orgs:
  - name: acme
    identity: {key: "human:mira@acme.example"}
  - name: beta
    identity: {key: "human:mira@beta.example"}
setups:
`

// TestRosterRoutingTwoOrgSetupsD704: a roster op from a folder no org
// applies to (the home folder) on a machine with two org setups runs on
// the default setup when that is an org setup, else a roster change runs
// on the only setup where the person is an admin; each says which setup
// it used. Two admin setups and no default route nowhere (the refusal
// names --setup <each>), and a roster read is never routed by admin kind
// (devco:D-704: member add from home was refused with two org setups).
func TestRosterRoutingTwoOrgSetupsD704(t *testing.T) {
	t.Parallel()
	setup := func(name, kind, org string, def bool) string {
		s := "  - name: " + name + "\n    kind: " + kind + "\n    org: " + org + "\n    folders: [~/code/" + org + "/**]\n    personal_store: none\n"
		if def {
			s += "    default: true\n"
		}
		return s
	}
	add := `{"email":"pat@acme.example","name":"Pat"}`
	for _, c := range []struct {
		name, setups, opName, input string
		want, why                   string // want "" = not routed
	}{
		{"default-org-setup", setup("acme-admin", "admin", "acme", true) + setup("beta-member", "member", "beta", false), profilestore.OpStaffAdd, add, "acme-admin", "the default setup"},
		{"default-member-setup-read", setup("acme-admin", "admin", "acme", false) + setup("beta-member", "member", "beta", true), profilestore.OpStaffList, `{}`, "beta-member", "the default setup"},
		{"only-admin-setup", setup("acme-admin", "admin", "acme", false) + setup("beta-member", "member", "beta", false), profilestore.OpStaffAdd, add, "acme-admin", "the only org setup where you are an admin"},
		{"only-admin-setup-read", setup("acme-admin", "admin", "acme", false) + setup("beta-member", "member", "beta", false), profilestore.OpStaffList, `{}`, "", ""},
		{"two-admin-setups", setup("acme-admin", "admin", "acme", false) + setup("beta-admin", "admin", "beta", false), profilestore.OpStaffAdd, add, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var said []string
			a := openWith(t, t.TempDir(), Options{Home: writeSetupsConfig(t, d704Base+c.setups), Notice: func(m string) { said = append(said, m) }})
			e, ok := a.Registry().Lookup(c.opName)
			if !ok {
				t.Fatalf("no op %s", c.opName)
			}
			ctx, err := a.routeSoleSetup(background(), e, op.JSONDecoder{Body: []byte(c.input)})
			if err != nil {
				t.Fatalf("routeSoleSetup: %v", err)
			}
			if got := op.SetupOf(ctx); got != c.want {
				t.Fatalf("routed to %q, want %q (notices %q)", got, c.want, said)
			}
			if c.want == "" {
				if len(said) != 0 {
					t.Errorf("not routed, yet a notice names a setup: %q", said)
				}
				return
			}
			if len(said) != 1 || !strings.Contains(said[0], "setup "+c.want+" ("+c.why+")") {
				t.Errorf("notices %q, want one naming setup %s (%s)", said, c.want, c.why)
			}
		})
	}
	// a named --setup is never overridden
	a := openWith(t, t.TempDir(), Options{Home: writeSetupsConfig(t, d704Base+setup("acme-admin", "admin", "acme", true)+setup("beta-admin", "admin", "beta", false))})
	e, _ := a.Registry().Lookup(profilestore.OpStaffAdd)
	ctx, err := a.routeSoleSetup(op.WithSetup(background(), "beta-admin"), e, op.JSONDecoder{Body: []byte(add)})
	if err != nil || op.SetupOf(ctx) != "beta-admin" {
		t.Errorf("--setup beta-admin: routed to %q (%v), want it kept", op.SetupOf(ctx), err)
	}
}
