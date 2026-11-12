package content

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestShowText: the first line stays "<badge>  <id>"; title and body
// follow when present and are left out when empty.
func TestShowText(t *testing.T) {
	cases := []struct {
		label, title, body, want string
	}{
		{"title", "Retry", "twice on 502", "PA  pat_1\n  title: Retry\n\ntwice on 502\n"},
		{"name", "deploy", "", "PA  pat_1\n  name: deploy\n"},
		{"title", "", "only body", "PA  pat_1\n\nonly body\n"},
		{"title", "", "", "PA  pat_1\n"},
	}
	for _, c := range cases {
		if got := ShowText("PA", "pat_1", c.label, c.title, c.body); got != c.want {
			t.Fatalf("ShowText(%q, %q, %q) = %q, want %q", c.label, c.title, c.body, got, c.want)
		}
	}
}

// TestListLine: "<badge>  <id>" stays first; the title, name or body's
// first line follows, trimmed and cut on a character boundary.
func TestListLine(t *testing.T) {
	long := strings.Repeat("é", 100)
	cases := []struct{ text, want string }{
		{"Retry", "PA  pat_1  Retry\n"},
		{"", "PA  pat_1\n"},
		{"   \n  ", "PA  pat_1\n"},
		{"  first line \nsecond", "PA  pat_1  first line\n"},
		{"שלום 🚀", "PA  pat_1  שלום 🚀\n"},
		{long, "PA  pat_1  " + strings.Repeat("é", 38) + "...\n"},
	}
	for _, c := range cases {
		if got := ListLine("PA", "pat_1", c.text, ""); got != c.want {
			t.Fatalf("ListLine(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	// a row of another store keeps the layout, its label last (B3-7)
	if got, want := ListLine("PA", "pat_1", "Retry", "[me]"), "PA  pat_1  Retry [me]\n"; got != want {
		t.Fatalf("ListLine with a label = %q, want %q", got, want)
	}
}

// TestEditName: --title is --name's other spelling on edit; equal values
// are fine, different ones are refused, neither leaves the name alone.
func TestEditName(t *testing.T) {
	cases := []struct {
		name, title, want string
		bad               bool
	}{
		{"", "", "", false},
		{"deploy", "", "deploy", false},
		{"", "deploy", "deploy", false},
		{"deploy", "deploy", "deploy", false},
		{"deploy", "ship", "", true},
		{"   ", "", "", true},
		{"", " \t ", "", true},
		{"  deploy  ", "", "  deploy  ", false},
	}
	for _, c := range cases {
		got, err := EditName("playbook", c.name, c.title)
		if (err != nil) != c.bad || got != c.want {
			t.Fatalf("EditName(%q, %q) = %q, %v; want %q, refused %v", c.name, c.title, got, err, c.want, c.bad)
		}
		if c.bad && !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("EditName(%q, %q): want E_INVALID_INPUT, got %v", c.name, c.title, err)
		}
	}
}
