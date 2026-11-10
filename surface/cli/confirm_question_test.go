package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// questionRunner answers E_NEEDS_CONFIRM the way the engine does: a
// message with the package prefix and, when question is set, the question
// for the person (B-FLOW4-3). A yes runs the operation.
type questionRunner struct {
	*fake
	message, question string
}

func (q questionRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	if !c.Yes {
		err := errs.New(errs.NeedsConfirm, q.message)
		if q.question != "" {
			err = err.WithQuestion(q.question)
		}
		return op.Result{}, err
	}
	return q.fake.Run(ctx, e, d, op.Caller{Yes: true, Actor: c.Actor})
}

// TestConfirmAsksTheQuestion (B-FLOW4-3): at a terminal the [y/N] prompt
// is the envelope's question, never the "engine: …" message; an envelope
// without a question falls back to its message; a multi-line question is
// printed whole before the prompt. Without a terminal the error keeps the
// question for an agent to relay.
func TestConfirmAsksTheQuestion(t *testing.T) {
	t.Parallel()
	tty := WithInteractive(func() bool { return true })
	multi := "This becomes visible to everyone with access to repo (client):\n  \"note\" (mem_x, id kept), now in agency"
	cases := []struct {
		name, message, question, want, notWant string
	}{
		{"question wins over the message", "engine: move it?", "move it?", "move it? [y/N]", "engine:"},
		{"multi-line question", "engine: " + multi, multi, multi + " [y/N]", "engine:"},
		{"no question: the message", "really purge x?", "", "really purge x? [y/N]", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := c.want
			for answer, exit := range map[string]int{"y\n": 0, "n\n": errs.ExitNeedsConfirmation} {
				r := execute(Build(newFake(t).reg, questionRunner{fake: newFake(t), message: c.message, question: c.question}, tty), answer, "trash", "purge", "x")
				if r.exit != exit || !strings.Contains(r.stderr, want) {
					t.Fatalf("answer %q: exit %d stderr %q, want %q", answer, r.exit, r.stderr, want)
				}
				prompt := r.stderr
				if i := strings.Index(prompt, "[y/N]"); i >= 0 {
					prompt = prompt[:i]
				}
				if c.notWant != "" && strings.Contains(prompt, c.notWant) {
					t.Fatalf("answer %q: the prompt carries %q: %q", answer, c.notWant, r.stderr)
				}
			}
		})
	}
	// no terminal: the error goes back unchanged, question included
	f := newFake(t)
	r := execute(Build(f.reg, questionRunner{fake: f, message: "engine: move it?", question: "move it?"}), "", "trash", "purge", "x", "--json")
	var env errs.Envelope
	if jerr := json.Unmarshal([]byte(r.stdout), &env); jerr != nil || r.exit != errs.ExitNeedsConfirmation || env.Error.Question != "move it?" {
		t.Fatalf("no tty: exit %d out %q err %q (%v)", r.exit, r.stdout, r.stderr, jerr)
	}
}
