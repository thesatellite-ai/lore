package cli

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestParseColorMode(t *testing.T) {
	for _, s := range []string{"auto", "always", "never"} {
		if m, err := ParseColorMode(s); err != nil || string(m) != s {
			t.Fatalf("%q: %v %v", s, m, err)
		}
	}
	for _, s := range []string{"", "yes", "ALWAYS", "colour", "1"} {
		if _, err := ParseColorMode(s); !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("%q must be E_INVALID_INPUT, got %v", s, err)
		}
	}
	var f ColorFlag
	if f.Mode() != ColorAuto {
		t.Fatal("an unset --color is auto")
	}
}

// ColorOn is the whole decision matrix: mode × terminal × NO_COLOR.
func TestColorOn(t *testing.T) {
	cases := []struct {
		mode    ColorMode
		tty     bool
		noColor string
		want    bool
	}{
		{ColorAlways, false, "", true}, {ColorAlways, false, "1", true},
		{ColorNever, true, "", false}, {ColorNever, true, "1", false},
		{ColorAuto, true, "", true}, {ColorAuto, true, "1", false},
		{ColorAuto, false, "", false}, {ColorAuto, false, "1", false},
	}
	for _, c := range cases {
		if got := ColorOn(c.mode, c.tty, c.noColor); got != c.want {
			t.Errorf("ColorOn(%s, tty=%v, NO_COLOR=%q) = %v", c.mode, c.tty, c.noColor, got)
		}
	}
}

func TestPaletteLines(t *testing.T) {
	in := "✓ mem_1\n  scope: x\nWARN: careful\n  warning: indented\n! blocked\nplain ✓ later\n"
	if got := (Palette{}).Lines(in); got != in {
		t.Fatalf("an off palette must not change a byte: %q", got)
	}
	want := ansiBrightGreen + "✓" + ansiReset + " mem_1\n  scope: x\n" +
		ansiYellow + "WARN: careful" + ansiReset + "\n  " + ansiYellow + "warning: indented" + ansiReset + "\n" +
		ansiYellow + "!" + ansiReset + " blocked\nplain ✓ later\n"
	if got := (Palette{On: true}).Lines(in); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := (Palette{On: true}).Lines("✓ no newline"); got != ansiBrightGreen+"✓"+ansiReset+" no newline" {
		t.Fatalf("last line without a newline: %q", got)
	}
}
