package content_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// A field refused by value.TextOf prints its reason after the field name
// at every Texts site (errs.Prefix): before, the printed message was only
// "<noun>: field <k>", the reason sat in the never-printed Cause.
func TestFieldRefusalKeepsReason(t *testing.T) {
	bad := string([]byte{'a', 0xff, 'b'}) // invalid UTF-8, built at run time
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"content.Texts", func() error { _, err := content.Texts("lesson", map[string]string{"title": bad}); return err }, "lesson: field title: "},
		{"work.Texts", func() error { _, err := work.Texts("plan", map[string]string{"body": bad}); return err }, "plan: field body: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			b := errs.EnvelopeOf(err).Error
			if b.Code != errs.InvalidUTF8 || !strings.HasPrefix(b.Message, tc.want) || !strings.Contains(b.Message, "not valid UTF-8") {
				t.Fatalf("got %s %q, want %s starting %q with the UTF-8 reason", b.Code, b.Message, errs.InvalidUTF8, tc.want)
			}
		})
	}
	if _, err := value.TextOf(bad); err == nil {
		t.Fatal("fixture is valid UTF-8; the test proves nothing")
	}
}
