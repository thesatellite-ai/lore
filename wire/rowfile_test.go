package wire

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// rowVersionRE is a row document's "_v" member (its format version).
var rowVersionRE = regexp.MustCompile(`"_v":\s*[0-9]+`)

// TestBrokenRowFileIsReported (fail closed; b11-sync's
// TestB11Sync_BrokenRowFileIsReported at the core): a row file that is not
// JSON, or whose text holds an unresolved conflict block, is left out of
// every read and never silently: the next command's notice names the
// file once, and doctor is broken (exit 2) naming it; once the file is
// fixed both are quiet again and the row reads. Every notice ends with a
// plain next step (devco D-281: a newer row's and a marked field's line had
// lost theirs).
func TestBrokenRowFileIsReported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		corrupt func(orig string) string
		// what and next are the parts of the notice that say what is wrong
		// and what to do about it.
		what, next string
	}{
		{"invalid JSON", func(string) string { return "{broken" }, "is not valid JSON", "fix it, or put it back: git -C "},
		{"JSON that is no row", func(string) string { return "[1,2]" }, "", "fix or remove it"},
		{"conflict markers in the text", func(orig string) string {
			return strings.Replace(orig, `"fine"`, `"<<<<<<< a\nx\n=======\ny\n>>>>>>> b"`, 1)
		}, "field body holds unresolved conflict markers", "delete the marker lines in the file, or set the value with the row's edit command (lore sync resolve can pick a side); then commit it"},
		{"written by a newer lore", func(orig string) string {
			return rowVersionRE.ReplaceAllString(orig, `"_v":`+strconv.Itoa(row.FormatVersion+1))
		}, "written by a newer lore (_v " + strconv.Itoa(row.FormatVersion+1) + ", this lore reads up to " + strconv.Itoa(row.FormatVersion) + ")", "upgrade lore: lore upgrade"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := gitRepo(t)
			home := t.TempDir()
			actor, aerr := ids.Parse[ids.Actor](testActor)
			if aerr != nil {
				t.Fatal(aerr)
			}
			openApp := func(notes *[]string) *App {
				t.Helper()
				a, nerr := New(Options{RepoDir: repo, Home: home, Actor: actor, Notice: func(m string) { *notes = append(*notes, m) }})
				if nerr != nil {
					t.Fatal(nerr)
				}
				t.Cleanup(func() { _ = a.Close() }) // test teardown
				return a
			}
			var first []string
			a := openApp(&first)
			withProject(t, a)
			out, rerr := run(t, a, opsmemory.OpAdd, `{"text":"fine"}`, false)
			if rerr != nil {
				t.Fatal(rerr)
			}
			id := idOf(t, out)
			file := memPath(repo, id)
			orig, rerr := os.ReadFile(filepath.Clean(file))
			if rerr != nil {
				t.Fatal(rerr)
			}
			bad := tc.corrupt(string(orig))
			if bad == string(orig) {
				t.Fatal("setup: the file did not change")
			}
			writeRepoFile(t, repo, file, bad)

			var notes []string
			b := openApp(&notes)
			list, lerr := run(t, b, opsmemory.OpList, ``, false)
			if lerr != nil {
				t.Fatal(lerr)
			}
			if strings.Contains(list, id) {
				t.Fatalf("a broken row must not be listed: %s", list)
			}
			rel := ".lore/data/memories/" + id + ".json"
			n := 0
			for _, m := range notes {
				if strings.HasPrefix(m, "lore: "+rel+" is not a usable row file: ") && !strings.Contains(m, "rowfs") && !strings.Contains(m, "[E_") &&
					strings.Contains(m, tc.what) && strings.Contains(m, "\n  next: ") && strings.Contains(m, tc.next) {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("want one notice naming %s, got %q", rel, notes)
			}
			r, derr := opsdoctor.Run(ctx, b, opsdoctor.RunIn{})
			if derr != nil {
				t.Fatal(derr)
			}
			if r.Status != opsdoctor.StatusBroken || r.Exit != 2 || !strings.Contains(strings.Join(r.Errors, "\n"), "this repo: "+rel+" is not a usable row file") {
				t.Fatalf("doctor must be broken naming the file: %s %d %q", r.Status, r.Exit, r.Errors)
			}

			writeRepoFile(t, repo, file, string(orig))
			var after []string
			c := openApp(&after)
			if list, lerr = run(t, c, opsmemory.OpList, ``, false); lerr != nil || !strings.Contains(list, id) {
				t.Fatalf("the fixed row must read: %s %v", list, lerr)
			}
			if r2 := runDoctorOp(t, c); len(after) != 0 || len(r2.Errors) != 0 {
				t.Fatalf("after the fix: notices %q errors %q", after, r2.Errors)
			}
		})
	}
}

// writeRepoFile writes body to file inside repo through an os.Root.
func writeRepoFile(t *testing.T, repo, file, body string) {
	t.Helper()
	rt, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }() // a directory handle; nothing to flush
	rel, err := filepath.Rel(repo, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.WriteFile(rel, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
