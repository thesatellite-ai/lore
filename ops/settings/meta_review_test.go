package settings_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// runAs runs an operation as the harness actor with the given agent name
// ("" = a person), confirmed.
func runAs(t *testing.T, h *opstest.H, agent, name, input string) {
	t.Helper()
	e, ok := h.App.Registry().Lookup(name)
	if !ok {
		t.Fatalf("no op %s", name)
	}
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Actor: actor, Agent: agent, AgentFixed: true, Yes: true}); err != nil {
		t.Fatalf("%s %s: %v", name, input, err)
	}
}

// TestUnreviewedKeys: keys an agent added are listed (sorted, with their
// writer) until someone describes them, a person or an agent (P3-AGENT-3);
// a person's own key never is; the list matches `meta keys --unreviewed`
// (AGENT_IDENTITY 8.3, MQ7).
func TestUnreviewedKeys(t *testing.T) {
	h := opstest.New(t)
	runAs(t, h, "codex", settings.OpMetaAdd, `{"name":"renewal_risk","type":"text"}`)
	runAs(t, h, "codex", settings.OpMetaAdd, `{"name":"customer_tier","type":"text"}`)
	runAs(t, h, "", settings.OpMetaAdd, `{"name":"owner_team","type":"text"}`)

	got, err := settings.UnreviewedKeys(context.Background(), h.App)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "customer_tier" || got[1].Name != "renewal_risk" {
		t.Fatalf("unreviewed = %+v, want customer_tier, renewal_risk", got)
	}
	for _, k := range got {
		if k.CreatedBy != opstest.Actor || k.CreatedAt.IsZero() || k.Store == "" {
			t.Errorf("key %s: writer %q at %v in %q", k.Name, k.CreatedBy, k.CreatedAt, k.Store)
		}
	}
	list := h.JSON(h.Must(settings.OpMetaKeys, `{"unreviewed":true}`))
	if !strings.Contains(list, `"count":2`) {
		t.Errorf("meta keys --unreviewed = %s, want count 2 (same set as the inbox)", list)
	}

	// an agent's description reviews the key, a person's too
	runAs(t, h, "codex", settings.OpMetaDescribe, `{"id":"customer_tier","text":"plan level"}`)
	got, err = settings.UnreviewedKeys(context.Background(), h.App)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "renewal_risk" {
		t.Fatalf("after an agent's describe: %+v, want renewal_risk only", got)
	}
	runAs(t, h, "", settings.OpMetaDescribe, `{"id":"renewal_risk","text":"churn risk"}`)
	if got, err = settings.UnreviewedKeys(context.Background(), h.App); err != nil || len(got) != 0 {
		t.Fatalf("after a person's describe: %+v (%v), want none", got, err)
	}
}
