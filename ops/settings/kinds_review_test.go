package settings_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// runAsErr is runAs that returns the error.
func runAsErr(t *testing.T, h *opstest.H, agent, name, input string) error {
	t.Helper()
	e, ok := h.App.Registry().Lookup(name)
	if !ok {
		t.Fatalf("no op %s", name)
	}
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Actor: actor, Agent: agent, AgentFixed: true, Yes: true})
	return err
}

// TestKindsUnreviewed (later 38, CLI 3.2, MQ7): a kind an agent added is
// listed by `resource kinds --unreviewed` (and marked "(unreviewed)" in
// the plain list) until it is marked --reviewed; a person's own kind is
// never unreviewed. Owner rule 2026-11-11: a coding agent reviews for its
// person by default; a team that lists resource.kind.edit.reviewed in
// agents.person_only refuses the agent's --reviewed (E_POLICY_DENIED,
// needs a person) and nothing changes, while the person's still passes.
func TestKindsUnreviewed(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	runAs(t, h, "codex", settings.OpKindAdd, `{"kind":"vpn"}`)
	runAs(t, h, "codex", settings.OpKindAdd, `{"kind":"dns"}`)
	runAs(t, h, "", settings.OpKindAdd, `{"kind":"host"}`)
	unreviewed := func() string { return h.JSON(h.Must(settings.OpKinds, `{"unreviewed":true}`)) }
	if got := unreviewed(); !strings.Contains(got, `"vpn"`) || !strings.Contains(got, `"dns"`) || strings.Contains(got, `"host"`) {
		t.Fatalf("--unreviewed: %s", got)
	}
	if plain := opstest.Text(h.Must(settings.OpKinds, `{}`)); !strings.Contains(plain, "vpn (unreviewed)") || strings.Contains(plain, "host (unreviewed)") {
		t.Fatalf("plain list: %s", plain)
	}
	// No policy: the agent reviews for its person.
	runAs(t, h, "codex", settings.OpKindEdit, `{"id":"vpn","reviewed":true}`)
	if got := unreviewed(); strings.Contains(got, `"vpn"`) {
		t.Fatalf("after the agent's review with no policy set: %s", got)
	}
	// The team keeps reviews to people.
	runAs(t, h, "", settings.OpPolicySet, `{"key":"agents.person_only","value":"resource.kind.edit.reviewed"}`)
	err := runAsErr(t, h, "codex", settings.OpKindEdit, `{"id":"dns","reviewed":true}`)
	if !errs.Has(err, errs.PolicyDenied) || !errs.EnvelopeOf(err).Error.NeedsPerson {
		t.Fatalf("agent --reviewed with the policy set: %v", err)
	}
	if got := unreviewed(); !strings.Contains(got, `"dns"`) {
		t.Fatalf("a refused review changed the kind: %s", got)
	}
	runAs(t, h, "", settings.OpKindEdit, `{"id":"dns","reviewed":true}`)
	if got := unreviewed(); strings.Contains(got, `"dns"`) {
		t.Fatalf("after a person's review: %s", got)
	}
}
