package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaLintRegistryCap (later 6, FA-META-11): a registry at
// meta.max_keys_per_registry gets no lint line and refuses a new key
// (E_META_SIZE); one past it (rows that arrived another way, here the cap
// lowered after the keys were added) gets one "(registry <place>) N key
// rows, past meta.max_keys_per_registry M" finding with the hint, in text
// and --json; RegistriesOverCap reports the same.
func TestMetaLintRegistryCap(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	for _, k := range []string{"a_key", "b_key", "c_key"} {
		h.MustYes(settings.OpMetaAdd, `{"name":"`+k+`","type":"text"}`)
	}
	lint := func() string {
		res := h.Must(settings.OpMetaLint, `{}`)
		return opstest.Text(res) + h.JSON(res)
	}
	h.MustYes(settings.OpPolicySet, `{"key":"meta.max_keys_per_registry","value":"3"}`)
	if got := lint(); strings.Contains(got, "max_keys_per_registry") {
		t.Fatalf("at cap: %s", got)
	}
	h.Fails(settings.OpMetaAdd, `{"name":"d_key","type":"text"}`, errs.MetaSize)

	h.MustYes(settings.OpPolicySet, `{"key":"meta.max_keys_per_registry","value":"2"}`)
	got := lint()
	if strings.Count(got, "past meta.max_keys_per_registry 2") != 2 || !strings.Contains(got, "(registry this repo)") || !strings.Contains(got, "3 key rows") {
		t.Fatalf("over cap (want one finding in text and one in json): %s", got)
	}
	over, err := settings.RegistriesOverCap(t.Context(), h.App)
	if err != nil || len(over) != 1 || over[0].Store != "repo" || over[0].Keys != 3 || over[0].Cap != 2 {
		t.Fatalf("RegistriesOverCap: %+v %v", over, err)
	}
	if line := over[0].Line(); !strings.HasPrefix(line, "this repo: 3 key rows, past meta.max_keys_per_registry 2 (") {
		t.Fatalf("line %q", line)
	}
}
