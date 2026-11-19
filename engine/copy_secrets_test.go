package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestPlanSecretsBulkCopy (devco:D-717): a bulk copy (store adopt, the
// bulk move) runs the scan of a normal write over every row before any
// write: unanswered findings refuse the whole batch naming each row and
// the reruns (never the value), and each answer flag does for every row
// what it does for one write (a ref per finding sliced per row, allow
// rows for --not-a-secret, the redaction for --secret-unstored).
func TestPlanSecretsBulkCopy(t *testing.T) {
	t.Parallel()
	second := "AKIA" + strings.Repeat("Z", 16)
	newRow := func(f *fixture, body string) row.Row {
		t.Helper()
		id, err := f.eng.ids.NewID(ids.PrefixMemory)
		if err != nil {
			t.Fatal(err)
		}
		return row.Row{Table: schema.TableMemories, ID: id, Fields: map[string]value.Value{"body": txt(body)}}
	}
	const rerun = "lore store adopt --target acme/notes --yes"
	type tc struct {
		name   string
		mode   policy.OnDetect
		caller op.Caller
		code   errs.Code
	}
	for _, c := range []tc{
		{name: "ask without an answer", mode: policy.OnDetectAsk, code: errs.NeedsConfirm},
		{name: "refuse without an answer", mode: policy.OnDetectRefuse, code: errs.SecretDetected},
		{name: "three refs for two findings", mode: policy.OnDetectAsk, caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"env://A", "env://B", "env://C"}}}, code: errs.InvalidInput},
		{name: "unstored", mode: policy.OnDetectAsk, caller: op.Caller{Secret: op.SecretChoice{Unstored: true}}},
		{name: "one ref per finding", mode: policy.OnDetectRefuse, caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"env://ONE", "env://TWO"}}}},
		{name: "not a secret", mode: policy.OnDetectAsk, caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "made-up fixture"}}},
		{name: "redact policy", mode: policy.OnDetectRedact},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t, onDetect(t, c.mode))
			ctx := context.WithValue(context.Background(), callKey{}, call{op: "store.adopt", caller: c.caller})
			rows := []row.Row{newRow(f, "key "+fakeKey+" end"), newRow(f, "plain words"), newRow(f, "deploy "+second)}
			plan, err := f.eng.PlanSecrets(ctx, "store adopt", rerun, store.SourceRepo, rows)
			if c.code != "" {
				if !errs.Has(err, c.code) {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
				msg := err.Error()
				var e *errs.Error
				if errors.As(err, &e) {
					msg += e.Hint
				}
				if strings.Contains(msg, fakeKey) || strings.Contains(msg, second) {
					t.Fatalf("the refusal leaks a value: %s", msg)
				}
				if c.code != errs.InvalidInput {
					for _, want := range []string{rows[0].ID.String(), rows[2].ID.String(), "nothing written", rerun + " --secret-unstored", rerun + ` --not-a-secret --reason "<why>"`} {
						if !strings.Contains(msg, want) {
							t.Errorf("the refusal does not say %q:\n%s", want, msg)
						}
					}
					if strings.Contains(msg, rows[1].ID.String()) {
						t.Errorf("the refusal names the clean row:\n%s", msg)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Rows) != 2 || plan.Rows[0] != rows[0].ID || plan.Rows[1] != rows[2].ID {
				t.Fatalf("plan rows = %v, want the two rows with keys", plan.Rows)
			}
			var bodies []string
			var allows int
			for _, r := range rows {
				got, serr := f.eng.ScreenCopy(f.eng.ForRow(ctx, plan, r.ID), store.SourceRepo, r)
				if serr != nil {
					t.Fatalf("%s: %v", r.ID, serr)
				}
				bodies = append(bodies, got.Row.Fields["body"].String())
				allows += len(got.Allows)
				if c.caller.Secret.Unstored && r.ID != rows[1].ID && got.Redacted.ID != r.ID {
					t.Errorf("%s: no store-then-rotate redaction returned", r.ID)
				}
			}
			want := map[string][]string{
				"unstored":            {"key " + refs.Unstored + " end", "plain words", "deploy " + refs.Unstored},
				"one ref per finding": {"key ${secret:env://ONE} end", "plain words", "deploy ${secret:env://TWO}"},
				"not a secret":        {"key " + fakeKey + " end", "plain words", "deploy " + second},
				"redact policy":       {"key " + refs.Unstored + " end", "plain words", "deploy " + refs.Unstored},
			}[c.name]
			if strings.Join(bodies, "|") != strings.Join(want, "|") {
				t.Fatalf("bodies = %q, want %q", bodies, want)
			}
			if c.caller.Secret.NotASecret && allows < 2 {
				t.Fatalf("--not-a-secret recorded %d allow rows, want one per kept finding", allows)
			}
			if !c.caller.Secret.NotASecret && allows != 0 {
				t.Fatalf("unexpected allow rows: %d", allows)
			}
		})
	}
}
