package errcodes_test

import (
	"strings"
	"testing"

	coreerrs "github.com/thesatellite-ai/lore/model/errs"

	"saas/pkg/aicoder/errcodes"
)

// removedSpellings are command and flag spellings 0.1.18 removed (O12a:
// --allow-secrets has no alias; BATCH 2: `lore share` became `lore move`).
// An error description that still names one sends a person to a command
// that answers "unknown flag" or "unknown command".
var removedSpellings = []string{"--allow-secrets", "lore share"}

// TestDescriptions_NameNoRemovedSpelling walks both registries: the one
// `lore errors list` prints (errcodes) and the core's (model/errs, the
// generated reference table).
func TestDescriptions_NameNoRemovedSpelling(t *testing.T) {
	for _, c := range errcodes.All() {
		d := errcodes.Description(c)
		for _, bad := range removedSpellings {
			if strings.Contains(d, bad) {
				t.Errorf("errcodes %s description names removed %q: %s", c, bad, d)
			}
		}
	}
	for _, d := range coreerrs.All() {
		for _, bad := range removedSpellings {
			if strings.Contains(d.Description, bad) {
				t.Errorf("model/errs %s description names removed %q: %s", d.Code, bad, d.Description)
			}
		}
	}
}

// TestDescriptions_OverridesNamed pins the way out each description must
// name, in both registries: E_NETWORK_FS names its environment override
// (v0.1.17 text), E_SECRET_DETECTED the confirm path (--yes, recorded as a
// secret_allows row; decisions SQ2/SECRETS).
func TestDescriptions_OverridesNamed(t *testing.T) {
	cases := []struct {
		code string
		want []string
	}{
		{"E_NETWORK_FS", []string{"LORE_ALLOW_NETWORK_FS=1"}},
		{"E_SECRET_DETECTED", []string{"--yes", "not a secret"}},
	}
	for _, tc := range cases {
		old := errcodes.Description(errcodes.Code(tc.code))
		core, ok := coreerrs.Lookup(coreerrs.Code(tc.code))
		if !ok {
			t.Fatalf("model/errs has no %s", tc.code)
		}
		for _, w := range tc.want {
			if !strings.Contains(old, w) {
				t.Errorf("errcodes %s description %q lacks %q", tc.code, old, w)
			}
			if !strings.Contains(core.Description, w) {
				t.Errorf("model/errs %s description %q lacks %q", tc.code, core.Description, w)
			}
		}
	}
}
