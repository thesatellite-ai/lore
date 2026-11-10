package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// secretEvents is the audit log's events of one action.
func secretEvents(f *fixture, action Action) []Event {
	var out []Event
	for _, ev := range f.audit.events() {
		if ev.Action == action {
			out = append(out, ev)
		}
	}
	return out
}

// noSecretIn fails when an event of a secret action carries the value or
// its masked preview (SECRETS 16: names, ids and choices only).
func noSecretIn(t *testing.T, evs []Event, secrets ...string) {
	t.Helper()
	for _, ev := range evs {
		dump := fmt.Sprintf("%+v", ev.Secret)
		for _, s := range secrets {
			if strings.Contains(dump, s) {
				t.Fatalf("%s event carries the value: %s", ev.Action, dump)
			}
			if p := scan.Mask(s); strings.Contains(dump, p) {
				t.Fatalf("%s event carries the preview: %s", ev.Action, dump)
			}
		}
	}
}

// TestSecretDetectedAuditPerChoice (SECRETS 16, FA-AGENT-7): every
// finding of a write records one secret.detected event naming the field,
// the rule, the on_detect mode and the choice that settled it, for every
// way a finding is settled: refused, ask without an answer (cancelled), a
// terminal "no" to --not-a-secret (cancelled), --not-a-secret --reason
// --yes (not_a_secret), redact mode and --secret-unstored (unstored),
// --secret-ref (redacted). No event carries the value or its preview.
func TestSecretDetectedAuditPerChoice(t *testing.T) {
	t.Parallel()
	second := "AKIA" + strings.Repeat("Z", 16)
	one := "key " + fakeKey + " end"
	two := "a " + fakeKey + " b " + second + " c"
	cases := []struct {
		name     string
		mode     policy.OnDetect
		body     string
		caller   op.Caller
		answer   bool
		mode2    string // the mode= word
		choice   string
		findings int
	}{
		{name: "refuse", mode: policy.OnDetectRefuse, body: one, caller: op.Caller{Yes: true}, mode2: "refuse", choice: choiceRefused, findings: 1},
		{name: "refuse two", mode: policy.OnDetectRefuse, body: two, mode2: "refuse", choice: choiceRefused, findings: 2},
		{name: "ask no answer", mode: policy.OnDetectAsk, body: one, caller: op.Caller{Yes: true}, mode2: "ask", choice: choiceCancelled, findings: 1},
		{name: "ask terminal no", mode: policy.OnDetectAsk, body: one, answer: false, mode2: "ask", choice: choiceCancelled, findings: 1,
			caller: op.Caller{Interactive: true, Secret: op.SecretChoice{NotASecret: true, Reason: "doc sample"}}},
		{name: "ask terminal yes", mode: policy.OnDetectAsk, body: one, answer: true, mode2: "ask", choice: choiceNotASecret, findings: 1,
			caller: op.Caller{Interactive: true, Secret: op.SecretChoice{NotASecret: true, Reason: "doc sample"}}},
		{name: "not a secret --yes", mode: policy.OnDetectAsk, body: two, mode2: "ask", choice: choiceNotASecret, findings: 2,
			caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "doc sample"}}},
		{name: "redact mode", mode: policy.OnDetectRedact, body: one, mode2: "redact", choice: choiceUnstored, findings: 1},
		{name: "--secret-unstored", mode: policy.OnDetectAsk, body: one, mode2: "ask", choice: choiceUnstored, findings: 1,
			caller: op.Caller{Secret: op.SecretChoice{Unstored: true}}},
		{name: "--secret-ref", mode: policy.OnDetectRefuse, body: two, mode2: "refuse", choice: choiceRedacted, findings: 2,
			caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"gopass://acme/aws"}}}},
		{name: "no finding", mode: policy.OnDetectRefuse, body: "plain text", findings: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t, onDetect(t, c.mode), WithConfirmer(&yesConfirmer{answer: c.answer}),
				WithRedactHook(func(context.Context, RedactedWrite) error { return nil }))
			caller := c.caller
			caller.Actor = testActor
			_, _ = f.eng.Run(context.Background(), memAddOp(f, op.ConfirmRule{}), op.ArgsDecoder{Args: []string{c.body}}, caller)
			evs := secretEvents(f, ActionSecretDetected)
			if len(evs) != c.findings {
				t.Fatalf("secret.detected events = %d (%+v), want %d", len(evs), evs, c.findings)
			}
			want := "field=body rule=" + awsRule + " mode=" + c.mode2 + " choice=" + c.choice
			for _, ev := range evs {
				if ev.Secret.Result != want || ev.Op != "memory.add" || ev.Actor != testActor || ev.Table != schema.TableMemories {
					t.Fatalf("event = %+v, want result %q on memory.add", ev, want)
				}
			}
			noSecretIn(t, evs, fakeKey, second)
		})
	}
}

// TestURLCredentialsStrippedAudit (SECRETS 14.4, FA-AGENT-7): a url field
// that loses its userinfo on write records url.credentials_stripped naming
// the field and the row, never the removed token; a URL with only a user
// name, or none, records nothing.
func TestURLCredentialsStrippedAudit(t *testing.T) {
	t.Parallel()
	tok := "ghp_" + strings.Repeat("B", 24)
	cases := []struct {
		name, url string
		events    int
	}{
		{name: "token", url: "https://" + tok + "@github.com/acme/hub.git", events: 1},
		{name: "user and token", url: "https://mira:" + tok + "@github.com/acme/hub.git", events: 1},
		{name: "user only", url: "https://mira@github.com/acme/hub.git"},
		{name: "plain", url: "https://github.com/acme/hub.git"},
		{name: "ssh form", url: "git@github.com:acme/hub.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t)
			w, err := f.eng.Create(context.Background(), "", schema.TableProjects, map[string]value.Value{"name": txt("hub"), "origin_url": txt(c.url)})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(w.Row.Fields["origin_url"].String(), tok) {
				t.Fatalf("stored url keeps the token: %s", w.Row.Fields["origin_url"])
			}
			evs := secretEvents(f, ActionURLCredentialsStripped)
			if len(evs) != c.events {
				t.Fatalf("url.credentials_stripped events = %d, want %d", len(evs), c.events)
			}
			for _, ev := range evs {
				if !strings.HasPrefix(ev.Secret.Result, "field=origin_url ") || ev.ID != w.Row.ID || ev.Table != schema.TableProjects {
					t.Fatalf("event = %+v", ev)
				}
			}
			noSecretIn(t, evs, tok)
		})
	}
}
