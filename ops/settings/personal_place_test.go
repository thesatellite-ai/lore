package settings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceDefinitionsRefuseMe: like `resource kind add --to me`, a
// field add or edit and an adopt into the personal store are refused
// (E_INVALID_INPUT naming the command and --to me, with a hint to drop
// it), because nothing there could be used by a resource. Nothing is
// written: the kind still has only its repo field afterwards.
func TestResourceDefinitionsRefuseMe(t *testing.T) {
	t.Parallel()
	h := opstest.WithStores(t, []string{"me", "kb"})
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`)
	for _, c := range []struct{ op, cmd, in string }{
		{settings.OpKindAdd, "resource kind add", `{"kind":"vm","to":"me"}`},
		{settings.OpFieldAdd, "resource field add", `{"kind":"host","field":"region","type":"text","to":"me"}`},
		{settings.OpFieldEdit, "resource field edit", `{"kind":"host","field":"provider","required":true,"to":"me"}`},
		{settings.OpResAdopt, "resource adopt", `{"from":"kb","to":"me"}`},
	} {
		_, err := h.RunYes(c.op, c.in)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.InvalidInput {
			t.Errorf("%s: want E_INVALID_INPUT, got %v", c.cmd, err)
			continue
		}
		if !strings.HasPrefix(e.Message, c.cmd+" --to me: ") || !strings.Contains(e.Hint, "drop --to me") {
			t.Errorf("%s: message %q hint %q", c.cmd, e.Message, e.Hint)
		}
	}
	if out := opstest.Text(h.Must(settings.OpKindShow, `{"id":"host"}`)); strings.Contains(out, "region") {
		t.Errorf("a refused field add wrote a field:\n%s", out)
	}
}
