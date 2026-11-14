package team

import "testing"

// TestPersonHandleFoldsAccents pins I-10: an accented handle keeps every
// letter (chloé → chloe), it is never cut at the accent (@chlo).
func TestPersonHandleFoldsAccents(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"chloé":       "chloe",
		"@Chloé":      "chloe",
		"zoë-ann":     "zoe-ann",
		"Øyvind":      "oyvind",
		"ılgaz":       "ilgaz",
		"OnurFromAgy": "onurfromagy",
	}
	for in, want := range cases {
		h, _, err := personHandle(in)
		if err != nil || h != want {
			t.Errorf("personHandle(%q) = %q, %v; want %q", in, h, err, want)
		}
	}
	if _, note, _ := personHandle("chloé"); note != "handle @chloé is stored as @chloe (handles are one lowercase word)" {
		t.Errorf("note = %q", note)
	}
}
