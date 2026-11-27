//go:build journeys && !windows

package journeys

// cache_cold_build_test.go — devco:D-1020 (bug hunt H-282): the first
// read after lore's cache is gone (a new machine, a new lore version, a
// removed ~/.lore/cache) builds the cache from the row files. It now takes
// a quicker path for rows the cache never held: no clearing of what an
// older copy left, the full-text entries written in batches, the next
// store folder read while the one before is written. The answers must be
// the same as from the cache the writes built row by row: search, lists,
// the sub-task tree and a note's filled-in reference, after the cache is
// removed and after `lore search rebuild`.
//
// devco:D-1022 (bug hunt H-284): the build no longer keeps each store
// folder's rows for later readers, decodes each row once, and collects
// less often while it runs. lore doctor run on a removed cache (it builds
// the cache first) must report what it reports on the built cache.

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyCacheBuiltFromNothingAnswersTheSame(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	memID := regexp.MustCompile(`"id": "(mem_[0-9a-f]{32})"`)
	id := func(re *regexp.Regexp, out string) string {
		t.Helper()
		m := re.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no id in: %s", out)
		}
		return m[1]
	}
	var cited string
	reads := [][]string{
		{"search", "--query", "deploy", "--json"},
		{"search", "--query", "retries", "--json"},
		// the note citing ${row:…} is found by the cited note's words in
		// every build (devco:D-1021)
		{"search", "--query", "gateway", "--json"},
		{"memory", "list", "--store", "me", "--json"},
		{"task", "list", "--store", "me", "--json"},
	}
	answers := func() []string {
		t.Helper()
		var out []string
		for _, args := range reads {
			out = append(out, lap.Lore(lap.Home, args...).OK(t).Stdout)
		}
		return out
	}
	same := func(step string, want, got []string) {
		t.Helper()
		for i := range reads {
			if got[i] != want[i] {
				t.Errorf("%s: lore %v answers differently from the cache built row by row:\nbefore: %s\nnow:    %s", step, reads[i], want[i], got[i])
			}
		}
	}

	// doctorOf is a doctor report as status, exit and each check's
	// target and status (their wording may name times).
	doctorOf := func(r teamharness.Result) []string {
		t.Helper()
		var d doctorReport
		if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil || d.Status == "" {
			t.Fatalf("lore doctor --json printed no report (%v): %s", err, r)
		}
		out := []string{d.Status}
		for _, ch := range append(slices.Clone(d.Machine.Checks), d.Checks...) {
			out = append(out, ch.Check+" "+ch.Target+" "+ch.Status)
		}
		for _, su := range d.Setups {
			for _, ch := range su.Checks {
				out = append(out, ch.Check+" "+ch.Target+" "+ch.Status)
			}
		}
		slices.Sort(out[1:])
		return out
	}
	var want, wantDoctor []string
	w.Step("day1/notes-tasks-and-a-reference", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		for _, f := range []string{"work", "archive"} {
			lap.Lore(lap.Home, "store", "folder", "add", "me", f).OK(t)
			for _, body := range []string{"The deploy gateway retries twice on 502 (" + f + ").", "Dana reviews the deploy checklist on Mondays (" + f + ")."} {
				lap.Lore(lap.Home, "memory", "add", "--body", body, "--to", "me/"+f, "--yes").OK(t)
			}
		}
		cited = id(memID, lap.Lore(lap.Home, "memory", "add", "--body", "Gateway timeouts are 30 s.", "--to", "me/work", "--json").OK(t).Stdout)
		citing := id(memID, lap.Lore(lap.Home, "memory", "add", "--body", "See ${row:"+cited+"} before a deploy.", "--to", "me/archive", "--json").OK(t).Stdout)
		reads = append(reads, []string{"show", "--id", citing, "--json"})
		parent := jID(t, lap.Lore(lap.Home, "task", "add", "Ship the gateway fix", "--to", "me/work", "--json").OK(t))
		lap.Lore(lap.Home, "task", "add", "Write the gateway test", "--parent", parent, "--to", "me/work", "--json").OK(t)
		want = answers()
		wantDoctor = doctorOf(lap.Lore(lap.Home, "doctor", "--json"))
	})

	w.Step("day2/cache-removed-then-read", func() {
		if err := os.RemoveAll(lap.LorePath("cache")); err != nil {
			t.Fatal(err)
		}
		same("after ~/.lore/cache was removed", want, answers())
	})

	w.Step("day2/search-rebuild", func() {
		lap.Lore(lap.Home, "search", "rebuild", "--store", "me").OK(t)
		same("after lore search rebuild", want, answers())
	})

	w.Step("day3/doctor-builds-the-removed-cache", func() {
		if err := os.RemoveAll(lap.LorePath("cache")); err != nil {
			t.Fatal(err)
		}
		got := doctorOf(lap.Lore(lap.Home, "doctor", "--json"))
		if !slices.Equal(got, wantDoctor) {
			t.Errorf("lore doctor on a removed cache reports differently from the built cache:\nbefore: %v\nnow:    %v", wantDoctor, got)
		}
		same("after lore doctor built the removed cache", want, answers())
	})
}
