package value

import (
	"strings"
	"testing"
	"time"
)

// TestDurationInputOf: the CLI duration grammar (#22b) is Go's plus whole
// days and weeks, refuses what it cannot read naming the accepted form,
// and stores the Go form; DurationOf stays Go-only.
func TestDurationInputOf(t *testing.T) {
	t.Parallel()
	ok := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"0d", 0},
		{"1h30m", 90 * time.Minute},
		{"1.5h", 90 * time.Minute},
		{"45m", 45 * time.Minute},
		{"-1h", -time.Hour},
		{"0", 0},
	}
	for _, c := range ok {
		v, err := DurationInputOf(c.in)
		if err != nil || v.Duration() != c.want {
			t.Errorf("DurationInputOf(%q) = %v, %v; want %v", c.in, v.Duration(), err, c.want)
		}
	}
	for _, bad := range []string{"", "d", "w", "1.5d", "-2d", "1d12h", "3y", "2mo", "30x", "99999999999999999d"} {
		_, err := DurationInputOf(bad)
		if err == nil || !strings.Contains(err.Error(), DurationForm) {
			t.Errorf("DurationInputOf(%q) = %v; want a refusal naming %q", bad, err, DurationForm)
		}
	}
	if _, err := DurationOf("30d"); err == nil {
		t.Error("DurationOf must stay Go-only (task time refuses a day unit)")
	}
	v, _ := DurationInputOf("30d")
	if b, _ := v.MarshalJSON(); string(b) != `"720h0m0s"` {
		t.Errorf("stored form %s; want Go's", b)
	}
}

// TestShortForms: read-back text keeps only the precision a value has,
// while String/JSON keep the stored form.
func TestShortForms(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"1h30m0s": "1h30m", "720h0m0s": "720h", "1h0m0s": "1h", "10m0s": "10m", "1h0m5s": "1h0m5s",
		"1.5s": "1.5s", "0s": "0s", "-1h0m0s": "-1h", "300ms": "300ms",
	} {
		if got := ShortText(KindDuration, in); got != want {
			t.Errorf("duration %q → %q; want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"2026-11-07T08:00:00.000000000Z": "2026-11-07T08:00:00Z",
		"2026-11-07T08:00:00.500000000Z": "2026-11-07T08:00:00.5Z",
		"2026-11-07T08:00:00.123456789Z": "2026-11-07T08:00:00.123456789Z",
	} {
		if got := ShortText(KindDateTime, in); got != want {
			t.Errorf("datetime %q → %q; want %q", in, got, want)
		}
	}
	// other kinds and unparsable text come back unchanged
	for _, c := range [][2]string{{string(KindText), "1h30m0s"}, {string(KindDuration), "soon"}, {string(KindDateTime), "yesterday"}, {"", "x"}} {
		if got := ShortText(Kind(c[0]), c[1]); got != c[1] {
			t.Errorf("ShortText(%s, %q) = %q; want unchanged", c[0], c[1], got)
		}
	}
	d, _ := DurationOf("1h30m")
	if d.String() != "1h30m0s" {
		t.Errorf("String must keep the stored form, got %s", d)
	}
}

// TestFormNamesEveryTypedKind: every kind with a fixed input form names
// it with an example, so a refusal can say what would be accepted.
func TestFormNamesEveryTypedKind(t *testing.T) {
	t.Parallel()
	free := map[Kind]bool{KindText: true, KindJSON: true, KindList: true, KindRef: true, KindNull: true}
	for _, k := range Kinds() {
		if f := Form(k); (f == "") != free[k] {
			t.Errorf("Form(%s) = %q", k, f)
		}
	}
}
