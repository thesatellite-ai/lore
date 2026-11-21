package opstest

import (
	"fmt"
	"testing"
)

// failRecorder is a testing.TB that records a failure instead of failing,
// so a test can assert that the helpers refuse a broken note.
type failRecorder struct {
	testing.TB
	failed bool
	msg    string
}

func (r *failRecorder) Helper() {}

// Fatalf and Errorf take ...any because testing.TB declares them so.
func (r *failRecorder) Fatalf(format string, args ...any) {
	r.failed, r.msg = true, fmt.Sprintf(format, args...)
}

func (r *failRecorder) Errorf(format string, args ...any) {
	r.failed, r.msg = true, fmt.Sprintf(format, args...)
}

// TestEmptyTextSplitsTheNote (devco:D-832): the v0.1.17 part comes back
// exactly and a whole note passes; a missing, partial or extra note line,
// a try that is no lore command, or words other than the query fail.
func TestEmptyTextSplitsTheNote(t *testing.T) {
	const note = "  searched: repo, me (rule)\n  read as: words: zzzz\n  try: lore search --query \"zzzz\"\n  try: lore ask --query \"zzzz\"\n"
	if got := EmptyTextOf(t, "(no matches)\n"+note, "zzzz"); got != "(no matches)\n" {
		t.Fatalf("base %q", got)
	}
	_, n := EmptyText(t, "(no matches)\n  hint: lore search looks in every kind\n"+note)
	if fmt.Sprint(n.Stores, n.Kinds, n.Words, len(n.Try)) != "[repo me] [rule] [zzzz] 2" {
		t.Fatalf("note %+v", n)
	}
	for name, text := range map[string]string{
		"no note":         "(no matches)\n",
		"no try":          "(no matches)\n  searched: repo (rule)\n  read as: words: zzzz\n",
		"no read as":      "(no matches)\n  searched: repo (rule)\n  try: lore ask --query \"zzzz\"\n",
		"no kinds":        "(no matches)\n  searched: repo\n  read as: words: zzzz\n  try: lore ask --query zzzz\n",
		"line after note": "(no matches)\n" + note + "a stray line\n",
		"four tries":      "(no matches)\n" + note + "  try: lore a\n  try: lore b\n",
		"try not lore":    "(no matches)\n  searched: repo (rule)\n  read as: words: zzzz\n  try: grep zzzz\n",
		"other words":     "(no matches)\n  searched: repo (rule)\n  read as: words: yyyy\n  try: lore ask --query yyyy\n",
	} {
		r := &failRecorder{TB: t}
		EmptyTextOf(r, text, "zzzz")
		if !r.failed {
			t.Errorf("%s: accepted %q", name, text)
		}
	}
}

// TestEmptyJSONSplitsTheNote (devco:D-832): the envelope after the note
// comes back exactly, keys in order; a note out of place, null lists, an
// unknown field, a try that is no lore command or other words fail.
func TestEmptyJSONSplitsTheNote(t *testing.T) {
	const env = `{"schema_version":1,"kind":"memory.search","query":"gateway","count":0,"data":null}`
	const ok = `{"searched":{"stores":["repo"],"kinds":["memory"],"words":["gateway"]},"try":["lore search --query \"gateway\""],"schema_version":1,"kind":"memory.search","query":"gateway","count":0,"data":null}`
	if got := EmptyJSONOf(t, ok, "gateway"); got != env {
		t.Fatalf("rest %s", got)
	}
	for name, js := range map[string]string{
		"no note":       env,
		"note last":     `{"schema_version":1,"count":0,"data":null,"searched":{"stores":["repo"],"kinds":["memory"],"words":["gateway"]},"try":["lore ask"]}`,
		"try first":     `{"try":["lore ask"],"searched":{"stores":["repo"],"kinds":["memory"],"words":["gateway"]},"count":0}`,
		"null words":    `{"searched":{"stores":["repo"],"kinds":["memory"],"words":null},"try":["lore ask"],"count":0}`,
		"unknown field": `{"searched":{"stores":["repo"],"kinds":["memory"],"words":["gateway"],"extra":1},"try":["lore ask"],"count":0}`,
		"empty try":     `{"searched":{"stores":["repo"],"kinds":["memory"],"words":["gateway"]},"try":[],"count":0}`,
		"try not lore":  `{"searched":{"stores":["repo"],"kinds":["memory"],"words":["gateway"]},"try":["grep gateway"],"count":0}`,
		"other words":   `{"searched":{"stores":["repo"],"kinds":["memory"],"words":["other"]},"try":["lore ask"],"count":0}`,
	} {
		r := &failRecorder{TB: t}
		EmptyJSONOf(r, js, "gateway")
		if !r.failed {
			t.Errorf("%s: accepted %s", name, js)
		}
	}
}
