package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// emptyRow / emptyOut are an answer that is only a list (team invite list).
type (
	emptyRow struct {
		ID string `json:"id"`
	}
	emptyOut struct {
		Invites   []emptyRow `json:"invites"`
		PastTeams []emptyRow `json:"past_teams,omitempty"`
	}
	emptyIn struct{}
)

// TestEmptyAnswerSaysNothingFound (devco:D-504): a read's answer whose
// lists are all empty prints "no <list>" for each, never nothing at all
// (lore team invite list with no invites exited 0 with no output); a list
// with rows prints its table only; a write may stay silent.
func TestEmptyAnswerSaysNothingFound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		out  emptyOut
		read bool
		want string
	}{
		{emptyOut{}, true, "no invites\nno past teams\n"},
		{emptyOut{Invites: []emptyRow{{ID: "inv_1"}}}, true, "invites:\n  ID\n  inv_1\n"},
		{emptyOut{}, false, ""},
	} {
		o := op.Define(op.Spec{Name: "team.invite_probe", Summary: "probe", Permission: op.PermRead, Access: op.AccessRead},
			func(_ context.Context, _ emptyIn) (emptyOut, error) { return tc.out, nil })
		in, err := o.Decode(op.JSONDecoder{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := o.Invoke(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := printHuman(&b, res, Palette{}, "lore team invite list", tc.read); err != nil {
			t.Fatal(err)
		}
		if b.String() != tc.want {
			t.Errorf("printed %q, want %q", b.String(), tc.want)
		}
	}
}
