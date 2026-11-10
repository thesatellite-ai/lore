package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/schema"
)

// TestAgentKeepsIDAfterOwnerEmailChange: the person changed email on
// another machine (the roster row keeps the first email's id, stable_key
// is the new email, key_aliases the old one); this machine only knows the
// new email. A coding agent detected here is derived from the owner's kept
// id, so it writes as the same agent actor as before the change.
func TestAgentKeepsIDAfterOwnerEmailChange(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	a := orgPeriodsApp(t)
	ctx := background()
	const oldKey, newKey = "human:alice.old@example.com", testEmail // the org identity is the new email
	kept := dupCreate(t, a, rosterStore, oldKey, map[string]string{"kind": schema.ActorKindHuman, "display_name": "Alice",
		"stable_key": newKey, schema.FieldActorKeyAliases: oldKey})

	// The new-email-only machine, inside a coding tool's session.
	for _, n := range agentdetect.AllowedNames(nil) {
		t.Setenv(n, "")
	}
	t.Setenv("LORE_HUMAN", "")
	t.Setenv("CODEX_THREAD_ID", "t-1")
	home := srcHome(t, "identity:\n  key: "+newKey+"\n  name: Alice\n")
	b, err := New(Options{RepoDir: a.opts.RepoDir, Home: home, OrgStores: a.opts.OrgStores, Org: a.opts.Org, RepoIsClient: a.opts.RepoIsClient})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	st, err := b.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.who.Kind != identity.KindAgent || st.who.Owner == nil {
		t.Fatalf("no coding agent detected: %+v", st.who)
	}
	owner, err := st.who.Owner.ActorID()
	if err != nil || owner.Any() != kept {
		t.Fatalf("the agent's owner is %s (%v), want the kept actor %s", owner, err, kept)
	}
	if !strings.HasSuffix(st.who.StableKey, "/"+kept.String()[len("act_"):len("act_")+6]) {
		t.Fatalf("agent key %q is not derived from the kept owner id %s", st.who.StableKey, kept)
	}
	if want, _ := identity.ActorID(st.who.StableKey); st.actor != want {
		t.Fatalf("writes are stamped %s, want the agent's own id %s", st.actor, want)
	}
}
