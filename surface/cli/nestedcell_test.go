package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/op"
)

// cellFix is a nested object in a table row, shaped like a setup check's
// fix (text, an argv, two flags).
type cellFix struct {
	Text     string   `json:"text"`
	Command  []string `json:"command"`
	Runnable bool     `json:"runnable"`
}

// cellCheck is one row of a checks table with an object cell and a time.
type cellCheck struct {
	Check     string    `json:"check"`
	Fix       cellFix   `json:"fix"`
	CheckedAt time.Time `json:"checked_at"`
}

// cellField is an item of a nested list (a proposal's fields).
type cellField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// cellProposal is a list item that holds a list of objects.
type cellProposal struct {
	Name   string      `json:"name"`
	Fields []cellField `json:"fields"`
}

type cellIn struct{}

type cellOut struct {
	Checks    []cellCheck    `json:"checks"`
	Proposals []cellProposal `json:"proposals"`
}

// TestNestedCellsReadable: a table cell holding an object prints its set
// fields as text (blank when none is set, never the object's JSON), a time
// prints to the second, and a list whose items hold a list of objects
// prints one block per item with the inner list as its own table (B1-3:
// `lore setup apply` showed {"text":"","command":null,...} in FIX and
// import-scan put whole JSON lists in one cell). --json is unchanged.
func TestNestedCellsReadable(t *testing.T) {
	at := time.Date(2026, 11, 12, 19, 39, 43, 342161897, time.UTC)
	o := op.Define(op.Spec{Name: "setup.cells", Summary: "cells", Permission: op.PermRead, Access: op.AccessRead},
		func(_ context.Context, _ cellIn) (cellOut, error) {
			return cellOut{
				Checks: []cellCheck{
					{Check: "name_free", CheckedAt: at},
					{Check: "store_reach", Fix: cellFix{Text: "add the store", Command: []string{"lore", "store", "add"}, Runnable: true}, CheckedAt: at},
					{Check: "only_text", Fix: cellFix{Text: "edit the file"}, CheckedAt: at},
				},
				Proposals: []cellProposal{{Name: "acme", Fields: []cellField{{Field: "kind", Value: "member"}, {Field: "org", Value: "acme"}}}},
			}, nil
		})
	in, err := o.Decode(op.JSONDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := o.Invoke(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printHuman(&out, res, Palette{}, "lore setup cells"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, bad := range []string{"{", "\"text\"", "null", ".342161897"} {
		if strings.Contains(got, bad) {
			t.Errorf("human output holds %q:\n%s", bad, got)
		}
	}
	for _, want := range []string{
		"text: add the store; command: lore, store, add; runnable: true",
		"edit the file",
		"2026-11-12T19:39:43Z",
		"  - name: acme\n    fields:\n",
		"FIELD  VALUE",
		"org    acme",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("human output lacks %q:\n%s", want, got)
		}
	}
	var line string
	for l := range strings.SplitSeq(got, "\n") {
		if strings.Contains(l, "name_free") {
			line = l
		}
	}
	if f := strings.Fields(line); len(f) != 2 || f[1] != "2026-11-12T19:39:43Z" {
		t.Errorf("an empty fix is not a blank cell: %q", line)
	}
	var js bytes.Buffer
	if err := printJSON(&js, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"command": null`) || !strings.Contains(js.String(), "19:39:43.342161897Z") {
		t.Errorf("--json changed:\n%s", js.String())
	}
}

type wideRow struct {
	ID   string `json:"id"`
	Help string `json:"help"`
}

type wideOut struct {
	Wrote []string  `json:"wrote"`
	Rows  []wideRow `json:"rows"`
}

// TestWideCellsCutAndEmptyListsLeftOut: a table cell longer than maxCell
// is cut with an ellipsis, so one long help text no longer makes every
// row hundreds of characters wide, and a required list with no items is
// left out of human output instead of printing "wrote: " (B2-17); --json
// keeps both whole.
func TestWideCellsCutAndEmptyListsLeftOut(t *testing.T) {
	long := strings.Repeat("word ", 60)
	o := op.Define(op.Spec{Name: "setup.wide", Summary: "wide", Permission: op.PermRead, Access: op.AccessRead},
		func(_ context.Context, _ cellIn) (wideOut, error) {
			return wideOut{Wrote: []string{}, Rows: []wideRow{{ID: "kind", Help: long}}}, nil
		})
	in, err := o.Decode(op.JSONDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := o.Invoke(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printHuman(&out, res, Palette{}, "lore setup wide"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "wrote") {
		t.Errorf("an empty list printed:\n%s", got)
	}
	for l := range strings.SplitSeq(got, "\n") {
		if n := len([]rune(l)); n > maxCell+20 {
			t.Errorf("line %d characters wide: %q", n, l)
		}
	}
	if !strings.Contains(got, "…") {
		t.Errorf("long cell not marked as cut:\n%s", got)
	}
	var js bytes.Buffer
	if err := printJSON(&js, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"wrote": []`) || !strings.Contains(js.String(), strings.TrimSpace(long)) {
		t.Errorf("--json changed:\n%s", js.String())
	}
}
