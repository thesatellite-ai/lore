package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// A meta edit --type whose rules do not fit the new type prints why after
// its own context (errs.Prefix): the printed Message carries the rule
// check's reason, not only "the rules of <key> do not fit <type>".
func TestMetaRetypeRulesMisfitSaysWhy(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"bounded","type":"number"}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"bounded","min":"1","max":"9"}`)
	_, err := h.RunYes(settings.OpMetaEdit, retypeIn("bounded", "text", ""))
	if err == nil {
		t.Fatal("retype of a bounded key to text went through")
	}
	msg := errs.EnvelopeOf(err).Error.Message
	for _, want := range []string{"meta edit --type: the rules of bounded do not fit text", "--min/--max apply to number"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q lacks %q", msg, want)
		}
	}
}
