package leakcheck

import (
	"strings"
	"testing"
)

const testSession = "3f2a9c1e-7b4d-4e8a-9c2f-1a2b3c4d5e6f"

// TestLocalPathSegment: a session id that is a folder or file name of a
// local path is exempt; anywhere else (prose, a url, a query, key=value,
// part of a longer name) it is still a hit.
func TestLocalPathSegment(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"/home/pat/scratch/" + testSession + "/lib", true},
		{"/home/pat/scratch/" + testSession, true},
		{"~/tmp/" + testSession + "/notes.md", true},
		{"file:///home/pat/" + testSession + "/lib.git", true},
		{"see ./" + testSession + "/out.log for it", true},
		{"/home/pat/" + testSession + ".jsonl", true},
		{`C:\Users\pat\` + testSession + `\lib`, true},
		{"session " + testSession + " was slow", false},
		{testSession, false},
		{"https://example.test/s/" + testSession + "/x", false},
		{"https://example.test/s?id=" + testSession, false},
		{"session_id=" + testSession, false},
		{"/home/pat/run-" + testSession + "/lib", false},
		{"/home/pat/" + testSession + "x/lib", false},
		{"relative/" + testSession + "/lib", false},
	}
	for _, c := range cases {
		off := strings.Index(c.text, testSession)
		if got := LocalPathSegment(c.text, off, len(testSession)); got != c.want {
			t.Errorf("LocalPathSegment(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// TestSessionHitsSkipLocalPaths: Check reports the id in prose even when
// the same text also names a folder of that id.
func TestSessionHitsSkipLocalPaths(t *testing.T) {
	it := Item{Where: "body"}
	if f := sessionHits(it, "clone in /home/pat/"+testSession+"/lib", []string{testSession}); len(f) != 0 {
		t.Errorf("a folder named after the session is flagged: %v", f)
	}
	if f := sessionHits(it, "clone in /home/pat/"+testSession+"/lib for session "+testSession, []string{testSession}); len(f) != 1 {
		t.Errorf("the id in prose after a path is not flagged: %v", f)
	}
}
