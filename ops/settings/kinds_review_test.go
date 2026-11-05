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
// the plain list) until a person marks it --reviewed; an agent's
// --reviewed is refused E_POLICY_DENIED (needs a person) and changes
// nothing; a person's own kind is never unreviewed.
func TestKindsUnreviewed(t *testing.T) {
	h := opstest.New(t)
	runAs(t, h, "codex", settings.OpKindAdd, `{"kind":"vpn"}`)
	runAs(t, h, "", settings.OpKindAdd, `{"kind":"host"}`)
	unreviewed := func() string { return h.JSON(h.Must(settings.OpKinds, `{"unreviewed":true}`)) }
	if got := unreviewed(); !strings.Contains(got, `"vpn"`) || strings.Contains(got, `"host"`) {
		t.Fatalf("--unreviewed: %s", got)
	}
	if plain := opstest.Text(h.Must(settings.OpKinds, `{}`)); !strings.Contains(plain, "vpn (unreviewed)") || strings.Contains(plain, "host (unreviewed)") {
		t.Fatalf("plain list: %s", plain)
	}
	err := runAsErr(t, h, "codex", settings.OpKindEdit, `{"id":"vpn","reviewed":true}`)
	if !errs.Has(err, errs.PolicyDenied) || !errs.EnvelopeOf(err).Error.NeedsPerson {
		t.Fatalf("agent --reviewed: %v", err)
	}
	if got := unreviewed(); !strings.Contains(got, `"vpn"`) {
		t.Fatalf("a refused review changed the kind: %s", got)
	}
	runAs(t, h, "", settings.OpKindEdit, `{"id":"vpn","reviewed":true}`)
	if got := unreviewed(); strings.Contains(got, `"vpn"`) {
		t.Fatalf("after a person's review: %s", got)
	}
}
