package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestConfirmCarriesTheQuestion (B-FLOW4-3): an operation's confirm
// without a terminal answers E_NEEDS_CONFIRM whose question is exactly
// the operation's question (what an agent relays and a terminal prompt
// shows), never prefixed with the package name the message carries; a
// terminal confirmer is handed the same text. Matrix: a one-line rule
// question and a multi-line question from Asks.
func TestConfirmCarriesTheQuestion(t *testing.T) {
	ctx := context.Background()
	multi := "This becomes visible to everyone with access to repo (client):\n  \"hello\" (mem_x, id kept), now in agency"
	for _, c := range []struct {
		name string
		ask  func(context.Context, memAddIn) (string, error)
	}{
		{"rule question", nil},
		{"multi-line Asks question", func(context.Context, memAddIn) (string, error) { return multi, nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			conf := &yesConfirmer{answer: true}
			f := setup(t, WithConfirmer(conf))
			o := memAddOp(f, op.ConfirmRule{Always: true})
			var e op.Entry = o
			if c.ask != nil {
				e = o.Asks(c.ask)
			}
			_, err := f.eng.Run(ctx, e, op.ArgsDecoder{Args: []string{"hello"}}, op.Caller{})
			if !errs.Has(err, errs.NeedsConfirm) {
				t.Fatalf("no terminal, no yes: %v", err)
			}
			body := errs.EnvelopeOf(err).Error
			q := body.Question
			if q == "" || strings.HasPrefix(q, "engine:") {
				t.Fatalf("question %q (message %q)", q, body.Message)
			}
			if c.ask != nil && q != multi {
				t.Fatalf("question %q, want the operation's %q", q, multi)
			}
			if _, err = f.eng.Run(ctx, e, op.ArgsDecoder{Args: []string{"hello"}}, op.Caller{Interactive: true}); err != nil {
				t.Fatal(err)
			}
			if len(conf.asked) != 1 || conf.asked[0] != q {
				t.Fatalf("terminal asked %q, the error's question is %q", conf.asked, q)
			}
		})
	}
}
