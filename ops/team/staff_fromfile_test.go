package team

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// staffAddFileErr runs member add --from on body (written as name) and
// returns the refusal; nothing may be written.
func staffAddFileErr(t *testing.T, name, body string) *errs.Error {
	t.Helper()
	x := newFixture(t)
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	before := x.rosterCount()
	_, err := x.ops().staffAdd(op.WithConfirmed(x.ctx, true), StaffAddIn{From: path})
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("member add --from %s: want a refusal, got %v", name, err)
	}
	if after := x.rosterCount(); after != before {
		t.Fatalf("a refused file changed the roster: %d people, was %d", after, before)
	}
	return e
}

// TestStaffFromFileListsEveryBadLine (B-2): one run names every bad line
// by its file line (header counted, a quoted cell over two lines counted
// too), a duplicate names both lines, and the hint is the fix for this
// file, never a made-up example address.
func TestStaffFromFileListsEveryBadLine(t *testing.T) {
	body := "email,name,type\n" +
		"ann@example.test,Ann,employee\n" +
		"bob@example.test,\"Bob\nTwo Lines\",employee\n" + // lines 3-4
		"not-an-email,Broken,employee\n" + // line 5
		"cat@example.test,Cat,boss\n" + // line 6
		"ann@example.test,Ann Again,employee\n" + // line 7
		"dee@example.test,Dee,employee,extra\n" // line 8
	e := staffAddFileErr(t, "staff.csv", body)
	for _, want := range []string{
		"4 lines need a fix, nothing written",
		`line 5: "not-an-email" is not an email address`,
		`line 6: type "boss" is not one of`,
		"line 7: ann@example.test is already on line 2",
		"line 8: it has 4 cells, the header has 3",
	} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message lacks %q:\n%s", want, e.Message)
		}
	}
	if e.Code != errs.InvalidInput || strings.Contains(e.Message, "person ") {
		t.Errorf("code %s, message %s", e.Code, e.Message)
	}
	if !strings.Contains(e.Hint, "in staff.csv fix line 5:") || !strings.Contains(e.Hint, "--dry-run") || strings.Contains(e.Hint, ".example") {
		t.Errorf("hint is not this file's fix: %s", e.Hint)
	}
}

// TestStaffFromFileBrokenQuote (B-2): a quote that is never closed names
// its line and the reason, with the fix, instead of a bare "line 2".
func TestStaffFromFileBrokenQuote(t *testing.T) {
	e := staffAddFileErr(t, "quote.csv", "email,name\n\"a@example.test,broken\nb@example.test,Ok\n")
	if !strings.Contains(e.Message, "line 2: a quote") || !strings.Contains(e.Message, "never closed") {
		t.Errorf("message: %s", e.Message)
	}
	if !strings.Contains(e.Hint, "quote.csv fix line 2: close the quote") {
		t.Errorf("hint: %s", e.Hint)
	}
}

// TestStaffFromFileJSON: a JSON file names entries, and a decode error
// names its line and reason.
func TestStaffFromFileJSON(t *testing.T) {
	e := staffAddFileErr(t, "staff.json", `[{"email":"a@example.test"},{"email":"nope"},{"email":"a@example.test"}]`)
	if !strings.Contains(e.Message, `entry 2: "nope" is not an email address`) || !strings.Contains(e.Message, "entry 3: a@example.test is already on entry 1") {
		t.Errorf("message: %s", e.Message)
	}
	e = staffAddFileErr(t, "broken.json", "[\n{\"email\": \"a@example.test\"},\n{\"email\": 5}\n]")
	if !strings.Contains(e.Message, "line 3: not a JSON list of people") {
		t.Errorf("message: %s", e.Message)
	}
}

// rosterCount is how many people the roster has.
func (x *fixture) rosterCount() int {
	x.t.Helper()
	r, done, err := x.e.Roster(x.ctx, false)
	defer done()
	if err != nil {
		x.t.Fatal(err)
	}
	rows, err := staffRows(x.ctx, r, true)
	if err != nil {
		x.t.Fatal(err)
	}
	return len(rows)
}
