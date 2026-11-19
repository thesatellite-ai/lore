//go:build journeys && !windows

package journeys

// parallel_writers_test.go — several agents write into one store at once
// (devco D-437, D-465). Lee's team store gets a folder nobody has written
// into yet; six processes (three of them coding-agent sessions) add a note
// there at the same moment (each with --create) from a plain folder while two readers list and
// search in the app repo and in the store clone. Every write is committed
// in the store clone (none printed "wrote" and stayed untracked, none lost,
// no E_STALE or E_CONTENDED), and a second round into the now-existing
// folder lands the same way. Reads run during the writes leave the app
// repo's tree and its .lore/lock untouched (a read took the repo lock and
// rewrote its holder line before D-465).
//
//	~/code (plain folder)  ── 6 × memory add --to <org>/<new folder> ──▶ store clone
//	~/code/app, store top  ── reads at the same time ──▶ no tree change, lock untouched

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Names of the parallel-writers journey (placeholders).
const (
	jpwFolder  = "billing"
	jpwOld     = "ops"
	jpwWriters = 6
	jpwReaders = 2
)

func TestJourneyParallelWritersOneStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	app := tt.repo[tlLee.Email]
	parent := filepath.Join(lee.Home, "code")
	plain := &teamharness.Clone{A: app.A, Dir: parent, Remote: app.Remote}
	target := tlOrg + "/" + jpwFolder
	var storeTop string

	w.Step("day1/company-store", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		// a folder the team already writes into; billing is new
		lee.Lore(parent, "store", "folder", "add", "team", jpwOld).OK(t)
		out := lee.Lore(parent, "rule", "add", "--to", tlOrg+"/"+jpwOld, "--body", "notes name their owner", "--yes", "--json").OK(t).Out()
		id := regexp.MustCompile(`rul_[0-9a-f]{32}`).FindString(out)
		if id == "" {
			t.Fatalf("rule add printed no id: %s", out)
		}
		p := jPath(t, lee.Lore(parent, "path", "--id", id, "--json").OK(t)).Path
		storeTop = strings.TrimSuffix(p, string(filepath.Separator)+filepath.Join(jpwOld, "rules", id+".json"))
		if storeTop == p {
			t.Fatalf("lore path --id %s = %s, not under the %s folder of the store clone", id, p, jpwOld)
		}
		if _, err := os.Stat(filepath.Join(storeTop, jpwFolder)); err == nil {
			t.Fatalf("the %s folder exists before anyone wrote into it", jpwFolder)
		}
		lee.Lore(parent, "sync").OK(t)
	})

	// round writes jpwWriters notes into target at once (half of them from
	// a coding-agent session) while readers read in the app repo and the
	// store clone; it returns what went wrong.
	round := func(tag string) []string {
		var mu sync.Mutex
		var bad []string
		fail := func(s string) { mu.Lock(); bad = append(bad, s); mu.Unlock() }
		var wg sync.WaitGroup
		for i := 0; i < jpwWriters; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := plain
				if i%2 == 1 {
					c = jAgentsAs(plain, &jClaude, fmt.Sprintf("pw-%s-%d", tag, i))
				}
				body := fmt.Sprintf("%s note %d from writer %d", tag, i, i)
				r := c.Lore("memory", "add", "--to", target, "--create", "--body", body)
				if r.Code != 0 || strings.Contains(r.Out(), "E_STALE") || strings.Contains(r.Out(), "E_CONTENDED") {
					fail(fmt.Sprintf("writer %d: %s", i, r))
				}
			}(i)
		}
		for i := 0; i < jpwReaders; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for _, args := range [][]string{
					{"memory", "list"},
					{"search", "--query", "note"},
					{"rule", "list"},
				} {
					dir := app
					if i == 1 {
						dir = &teamharness.Clone{A: app.A, Dir: storeTop, Remote: app.Remote}
					}
					if r := dir.Lore(args...); r.Code != 0 {
						fail(fmt.Sprintf("reader %d %v: %s", i, args, r))
					}
				}
			}(i)
		}
		wg.Wait()
		return bad
	}

	// committed checks the store clone: nothing left untracked or staged,
	// and every note of the round is in a commit.
	committed := func(tag string) {
		t.Helper()
		if st := w.Git(storeTop, "status", "--porcelain"); strings.TrimSpace(st) != "" {
			t.Errorf("the store clone has rows that are not committed after round %s:\n%s", tag, st)
		}
		logged := w.Git(storeTop, "log", "-p", "--format=%H", "--", jpwFolder)
		for i := 0; i < jpwWriters; i++ {
			if body := fmt.Sprintf("%s note %d from writer %d", tag, i, i); !strings.Contains(logged, body) {
				t.Errorf("%q is in no commit of the store clone", body)
			}
		}
		out := lee.Lore(parent, "memory", "list", "--store", target, "--json").OK(t).Out()
		if n := len(regexp.MustCompile(`"`+regexp.QuoteMeta(tag)+` note \d from writer \d"`).FindAllString(out, -1)); n != jpwWriters {
			t.Errorf("memory list --store %s shows %d of the %d notes of round %s:\n%s", target, n, jpwWriters, tag, out)
		}
	}

	// the app repo's state the reads must not change
	snap := func() string {
		lock, _ := os.ReadFile(filepath.Join(app.Dir, ".lore", "lock"))
		return app.Git("status", "--porcelain", "--ignored") + "\n--lock--\n" + string(lock)
	}

	w.Step("day1/six-writers-into-a-new-folder-at-once", func() {
		before := snap()
		for _, b := range round("first") {
			t.Error(b)
		}
		committed("first")
		if after := snap(); after != before {
			t.Errorf("reads during the writes changed the app repo (tree or .lore/lock):\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	w.Step("day1/six-more-into-the-folder-now-it-exists", func() {
		before := snap()
		for _, b := range round("second") {
			t.Error(b)
		}
		committed("second")
		if after := snap(); after != before {
			t.Errorf("reads during the writes changed the app repo (tree or .lore/lock):\nbefore:\n%s\nafter:\n%s", before, after)
		}
		lee.Lore(parent, "sync").OK(t)
		if st := w.Git(storeTop, "status", "--porcelain"); strings.TrimSpace(st) != "" {
			t.Errorf("the store clone is not clean after sync:\n%s", st)
		}
	})
}
