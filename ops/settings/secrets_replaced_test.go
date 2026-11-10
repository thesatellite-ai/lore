package settings_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestSecretRulesReplaced (SECRETS 3.3): `lore secrets rules --replaced`
// lists every lore pack rule once, in pack order, with its gitleaks twins
// from gitleaks.Covers (or "lore only"), in text and in --json; without
// the flag the replaced block is absent from both.
func TestSecretRulesReplaced(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	pack := gitleaks.Pack()
	covers := gitleaks.Covers()

	res := h.Must(settings.OpSecretRules, `{"replaced":true}`)
	var out settings.RulesOut
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Replaced) != len(pack) {
		t.Fatalf("replaced lists %d rules, want %d (the pack)", len(out.Replaced), len(pack))
	}
	text := opstest.Text(res)
	if !strings.HasPrefix(text, "LORE RULE\tALSO COVERED BY GITLEAKS\n") {
		t.Fatalf("text header %q", text)
	}
	for i, r := range pack {
		got := out.Replaced[i]
		if got.RuleID != r.ID {
			t.Fatalf("replaced[%d] = %s, want %s (pack order)", i, got.RuleID, r.ID)
		}
		want := covers[r.ID]
		if strings.Join(got.Gitleaks, ",") != strings.Join(want, ",") {
			t.Errorf("%s: json gitleaks %v, want %v", r.ID, got.Gitleaks, want)
		}
		line := r.ID + "\t(none: lore only)\n"
		if len(want) > 0 {
			line = r.ID + "\t" + strings.Join(want, ", ") + "\n"
		}
		if !strings.Contains(text, line) {
			t.Errorf("text lacks line %q", line)
		}
	}

	plain := h.Must(settings.OpSecretRules, ``)
	if strings.Contains(opstest.Text(plain), "ALSO COVERED") || strings.Contains(h.JSON(plain), `"replaced"`) {
		t.Errorf("replaced block shown without --replaced: %s", h.JSON(plain))
	}
}
