package meta

import (
	"strings"
	"testing"
)

func TestCheckSizeWarn(t *testing.T) {
	es := []Entry{{Name: "x", V: []byte(`"abcdefghijklmnopqrstuvwxyz"`)}}
	if w, err := CheckSizeWarn(es, 0, 10); err != nil || !strings.Contains(w, "past 10") {
		t.Fatalf("warn 10: %q %v", w, err)
	}
	if w, err := CheckSizeWarn(es, 0, 0); err != nil || w != "" {
		t.Fatalf("warn default (4096): %q %v", w, err)
	}
	if w, err := CheckSizeWarn(es, 0, 100000); err != nil || w != "" {
		t.Fatalf("warn large: %q %v", w, err)
	}
}
