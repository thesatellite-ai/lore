package settings_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLeaksAllow (#129, devco:D-906): a text allow is a secret_allows row
// in the note's own store with rule internal-note, the note as row_id and
// the passage's hash (never its text); every refusal names its fix.
func TestLeaksAllow(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	const passage = "Your order has been received and a support agent will contact you soon."
	note := h.ID(h.Must("decision.add", `{"title":"Banner wording approved","body":"Approved wording: \"`+passage+`\" Keep it."}`))
	checkRefusals(t, h, []refusal{
		{settings.OpLeaksAllow, `{"note":"` + note + `","phrase":"` + passage + `","reason":"  "}`, errs.InvalidInput, "--reason is required"},
		{settings.OpLeaksAllow, `{"note":"` + note + `","reason":"x"}`, errs.InvalidInput, "one of them"},
		{settings.OpLeaksAllow, `{"note":"` + note + `","phrase":"` + passage + `","match_hash":"7","reason":"x"}`, errs.InvalidInput, "one of them"},
		{settings.OpLeaksAllow, `{"note":"nope","phrase":"` + passage + `","reason":"x"}`, errs.InvalidID, "--note takes the id"},
		{settings.OpLeaksAllow, `{"note":"` + note + `","phrase":"a support agent will contact you","reason":"x"}`, errs.InvalidInput, "only blocks 8 or more"},
		{settings.OpLeaksAllow, `{"note":"` + note + `","phrase":"a support agent will contact you within two business days","reason":"x"}`, errs.InvalidInput, "does not hold that passage"},
		{settings.OpLeaksAllow, `{"note":"` + note + `","match_hash":"sha256:abcdef12","reason":"x"}`, errs.InvalidInput, "not the number a refusal prints"},
	})
	res := h.Must(settings.OpLeaksAllow, `{"note":"`+note+`","phrase":"`+strings.ToUpper(passage)+`","reason":"approved public copy"}`)
	if got := opstest.Text(res); !strings.Contains(got, "may ship anywhere (approved public copy)") {
		t.Errorf("allow text %q", got)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableSecretAllows, ID: mustID(t, h.ID(res))})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fields["rule_id"].String() != leakcheck.TextAllowRule || r.Fields["row_id"].String() != note {
		t.Errorf("allow row %+v", r.Fields)
	}
	if got, want := r.Fields["match_hash"].String(), leakcheck.PassageHashText(passage); got != strconv.FormatUint(uint64(want), 10) {
		t.Errorf("match_hash %s, want the passage hash %d", got, want)
	}
	for f, v := range r.Fields {
		if strings.Contains(strings.ToLower(v.String()), "support agent") {
			t.Errorf("allow row field %s holds the passage: %s", f, v.String())
		}
	}
}
