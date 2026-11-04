package direction

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestClassOf places every kind of store name: empty fails closed to
// client, me and me/<folder> are personal whatever the flag, any other name
// (repo, an org store, its folders, odd names) is internal unless flagged
// client.
func TestClassOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		client bool
		want   Class
	}{
		{"", false, Client},
		{"", true, Client},
		{"me", false, Personal},
		{"me", true, Personal},
		{"me/notes", false, Personal},
		{"me/notes", true, Personal},
		{"me/a/b", false, Personal},
		{"repo", false, Internal},
		{"repo", true, Client},
		{"agency", false, Internal},
		{"agency", true, Client},
		{"agency/org", false, Internal},
		{"agency/org", true, Client},
		// Names that merely start with "me" are not the personal store.
		{"meta", false, Internal},
		{"mex/x", true, Client},
		{"Me", false, Internal},
		{"/me", false, Internal},
		{"меня", false, Internal},
		{strings.Repeat("s", 4096), false, Internal},
	}
	for _, c := range cases {
		if got := ClassOf(c.name, c.client); got != c.want {
			t.Errorf("ClassOf(%q, %v) = %s, want %s", c.name, c.client, got, c.want)
		}
	}
}

// TestCheckTable walks every from × to of the F15 table in the package
// doc, plus unknown classes on either side (fail closed).
func TestCheckTable(t *testing.T) {
	t.Parallel()
	const bogus Class = "bogus"
	allowed := map[[2]Class]bool{
		{Client, Client}:     true,
		{Client, Internal}:   false,
		{Client, Personal}:   false,
		{Internal, Client}:   true,
		{Internal, Internal}: true,
		{Internal, Personal}: false,
		{Personal, Client}:   true,
		{Personal, Internal}: true,
		{Personal, Personal}: true,
		// Unknown source = client: only client targets.
		{"", Client}:      true,
		{"", Internal}:    false,
		{"", Personal}:    false,
		{bogus, Client}:   true,
		{bogus, Internal}: false,
		{bogus, Personal}: false,
		// Unknown target = personal: only a personal source.
		{Client, ""}:      false,
		{Internal, ""}:    false,
		{Personal, ""}:    true,
		{Internal, bogus}: false,
		{Personal, bogus}: true,
		{"", ""}:          false,
	}
	for pair, ok := range allowed {
		err := Check(pair[0], pair[1])
		if ok {
			if err != nil {
				t.Errorf("Check(%q, %q) refused: %v", pair[0], pair[1], err)
			}
			continue
		}
		if !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("Check(%q, %q) = %v, want E_LEAK_BLOCKED", pair[0], pair[1], err)
		}
	}
}

// TestCheckRefusalText: the refusal names the direction and the hint
// points at the store that may hold the pointer; it never says more.
func TestCheckRefusalText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to      Class
		msgPart, hint string
	}{
		{Client, Internal, "a client row cannot point at an internal row", hintInternal},
		{Client, Personal, "a client row cannot point at a personal row", hintPersonal},
		{Internal, Personal, "a shared row cannot point at a personal row", hintPersonal},
	}
	for _, c := range cases {
		err := Check(c.from, c.to)
		var e *errs.Error
		if !errors.As(err, &e) {
			t.Fatalf("%s->%s: not an *errs.Error: %v", c.from, c.to, err)
		}
		if !strings.Contains(e.Message, c.msgPart) || !strings.HasPrefix(e.Message, "F15: ") || !strings.HasSuffix(e.Message, "nothing written") {
			t.Errorf("%s->%s message %q", c.from, c.to, e.Message)
		}
		if e.Hint != c.hint {
			t.Errorf("%s->%s hint %q", c.from, c.to, e.Hint)
		}
	}
}
