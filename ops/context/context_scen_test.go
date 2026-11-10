package contextop

// context_scen_test.go — the mode matrix of `lore context` (render area,
// user scenarios): no mode is refused with the list of modes, every single
// mode reaches the host, the refinements (--session-start, --table,
// --search, --meta, --max-bytes) are not modes, and any two modes at once
// are refused with E_INVALID_INPUT before the host runs.

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// scenModes is every fetch mode, set alone.
var scenModes = map[string]FetchIn{
	"for-file":   {ForFile: "a.go"},
	"for-task":   {ForTask: "tsk_x"},
	"topic":      {Topic: "billing"},
	"topics":     {Topics: true},
	"for-prompt": {ForPrompt: "why"},
	"hook":       {Hook: "claude_code", HookStdin: true},
	"internal":   {Internal: true},
}

// scenMerge sets b's mode on top of a.
func scenMerge(a, b FetchIn) FetchIn {
	if b.ForFile != "" {
		a.ForFile = b.ForFile
	}
	if b.ForTask != "" {
		a.ForTask = b.ForTask
	}
	if b.Topic != "" {
		a.Topic = b.Topic
	}
	if b.ForPrompt != "" {
		a.ForPrompt = b.ForPrompt
	}
	if b.Hook != "" {
		a.Hook, a.HookStdin = b.Hook, b.HookStdin
	}
	a.Topics = a.Topics || b.Topics
	a.Internal = a.Internal || b.Internal
	return a
}

func TestRenderScenContextOneModeEach(t *testing.T) {
	for name, in := range scenModes {
		h := &fakeHost{}
		if _, err := Fetch(context.Background(), h, in, strings.NewReader("{}")); err != nil {
			t.Errorf("%s alone is answered: %v", name, err)
		}
		// Refinements stay one mode.
		in.SessionStart, in.Meta, in.MaxBytes, in.Table, in.Search = true, true, 100, "rules", "x"
		if _, err := Fetch(context.Background(), &fakeHost{}, in, strings.NewReader("{}")); err != nil {
			t.Errorf("%s with refinements is still one mode: %v", name, err)
		}
	}
}

func TestRenderScenContextTwoModesRefused(t *testing.T) {
	for an, a := range scenModes {
		for bn, b := range scenModes {
			if an >= bn {
				continue
			}
			h := &fakeHost{}
			_, err := Fetch(context.Background(), h, scenMerge(a, b), strings.NewReader("{}"))
			if errs.CodeOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), "pass only one of") {
				t.Errorf("--%s with --%s is refused with E_INVALID_INPUT: %v", an, bn, err)
			}
			if h.got != (FetchIn{}) {
				t.Errorf("--%s with --%s never reaches the host", an, bn)
			}
		}
	}
}

func TestRenderScenContextNoModeNamesEveryMode(t *testing.T) {
	_, err := Fetch(context.Background(), &fakeHost{}, FetchIn{Meta: true, MaxBytes: 10, Table: "rules"}, strings.NewReader(""))
	if errs.CodeOf(err) != errs.InvalidInput {
		t.Fatalf("no mode: E_INVALID_INPUT, got %v", err)
	}
	for _, m := range []string{"--for-file", "--for-task", "--topic", "--topics", "--for-prompt", "--hook", "--internal"} {
		if !strings.Contains(err.Error(), m) {
			t.Errorf("the refusal names %s: %v", m, err)
		}
	}
}
