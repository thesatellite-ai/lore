package lint

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRunUnknownCode: `lore lint --code` with a code lint does not know is
// E_INVALID_INPUT before any store is read (the zero Env would panic if it
// were), naming the code, and its hint lists every finding code (full run
// and --context) so the person can pick one; a known code next to it does
// not save the call; codes are case sensitive.
func TestRunUnknownCode(t *testing.T) {
	cases := [][]string{{"nosuch"}, {""}, {"Dangling_Ref"}, {string(DanglingRef), "nosuch"}, {"dangling-ref"}}
	for _, in := range cases {
		t.Run(strings.Join(in, ","), func(t *testing.T) {
			_, err := ops{}.run(context.Background(), RunIn{Code: in})
			if !errs.Has(err, errs.InvalidInput) {
				t.Fatalf("err = %v, want E_INVALID_INPUT", err)
			}
			var le *errs.Error
			if !errors.As(err, &le) {
				t.Fatalf("not a lore error: %v", err)
			}
			for _, c := range append(Codes(), ContextCodes()...) {
				if !strings.Contains(le.Hint, string(c)) {
					t.Fatalf("hint %q does not list %s", le.Hint, c)
				}
			}
		})
	}
}
