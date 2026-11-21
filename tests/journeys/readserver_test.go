//go:build journeys && !windows

package journeys

// readserver_test.go — devco:D-833 (bug hunt H-90): a warm `lore ask`
// could not answer in under 50 ms one-shot, so a local read server runs
// reads with its caches open. It runs each command in the caller's folder
// with the caller's environment: an ask from inside a code repo (the repo
// changes what a read covers), with --json, and a search must answer as
// the one-shot process does, and a write is never handed to it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyReadServerAnswersInTheCallersFolder(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/comet"), "comet")
	w.Step("day1/notes-and-a-repo", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		lap.Lore(lap.Home, "memory", "add", "--body", "Status update comet: beta with 4 teams.", "--to", "me/work", "--yes").OK(t)
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "comet builds with make release, never make all.").OK(t)
	})
	w.Step("day1/served-reads-answer-as-one-shot", func() {
		calls := [][]string{
			{"ask", "--store", "me", "--query", "what is the status of comet"},
			{"ask", "--store", "me", "--query", "what is the status of comet", "--json"},
			{"ask", "--query", "how does comet build"},
			{"search", "comet"},
			{"memory", "list"},
		}
		in := func(args []string) teamharness.Result { return lap.LoreIn(code, args...) }
		oneShot := make([]teamharness.Result, len(calls))
		for i, c := range calls {
			oneShot[i] = in(c)
		}
		log := filepath.Join(t.TempDir(), "read-server.log")
		sock := lap.LorePath("run", "read.sock")
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		defer func() {
			lap.SetEnv("LORE_READ_SERVER", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_LOG", "")
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		in(calls[0]) // starts the server
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(log)
				ents, _ := os.ReadDir(filepath.Dir(sock))
				names := []string{}
				for _, e := range ents {
					names = append(names, e.Name())
				}
				t.Fatalf("no read server listens on %s (run/ holds %v; the CLI logged %q)", sock, names, b)
			}
		}
		_ = os.Remove(log)
		for i, c := range calls {
			got := in(c)
			if got.Stdout != oneShot[i].Stdout || got.Stderr != oneShot[i].Stderr || got.Code != oneShot[i].Code {
				t.Errorf("served `lore %s` differs from one-shot:\nserved: %s\none-shot: %s", strings.Join(c, " "), got, oneShot[i])
			}
		}
		lap.LoreIn(code, "memory", "add", "--body", "comet ships on fridays only.").OK(t) // a write: never offered
		if b, _ := os.ReadFile(log); strings.Count(string(b), "served\n") != len(calls) || strings.Contains(string(b), "one-shot") {
			t.Errorf("want each read served and the write not offered, the log says:\n%s", b)
		}
		lap.LoreIn(code, "search", "fridays").OK(t).Says(t, "comet ships on fridays only")
	})
}

// TestJourneyReadServerEndsLikeOneShotOnAClosedPipe (devco:D-838, H-90
// LEFT 3): `lore sql … | head -c 5` reads five bytes and closes the pipe.
// A one-shot lore dies of SIGPIPE at its next write (exit 141 in bash,
// nothing on stderr); a served read must end the same way, print nothing
// more, and leave the server serving the next read.
func TestJourneyReadServerEndsLikeOneShotOnAClosedPipe(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	w.Step("day1/many-notes", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		for _, b := range []string{"comet beta has 4 teams.", "comet ships on fridays.", "comet builds with make release."} {
			lap.Lore(lap.Home, "memory", "add", "--body", b, "--to", "me/work", "--yes").OK(t)
		}
	})
	// pipe runs `lore sql --store me --query <big> | head -c 5` under bash
	// and returns what head printed, lore's stderr and both exit codes.
	pipe := func() (string, string, string) {
		t.Helper()
		script := `"$LORE_UNDER_TEST" sql --store me --query "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 200000) SELECT i FROM n" 2>"$ERR_FILE" | head -c 5; s=("${PIPESTATUS[@]}"); echo; echo "${s[@]}"`
		errFile := filepath.Join(t.TempDir(), "stderr")
		cmd := exec.Command("bash", "-c", script)
		cmd.Dir = lap.Home
		cmd.Env = append(lap.Environ(), "LORE_UNDER_TEST="+lap.Bin(), "ERR_FILE="+errFile)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bash pipeline: %v\n%s", err, out)
		}
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		stderr, _ := os.ReadFile(errFile)
		return strings.Join(lines[:len(lines)-1], "\n"), string(stderr), lines[len(lines)-1]
	}
	w.Step("day1/closed-pipe-served-as-one-shot", func() {
		oneOut, oneErr, oneCodes := pipe()
		if oneCodes != "141 0" || oneErr != "" {
			t.Fatalf("one-shot `lore sql … | head -c 5`: exit codes %q, stderr %q; want \"141 0\" and nothing", oneCodes, oneErr)
		}
		log := filepath.Join(t.TempDir(), "read-server.log")
		sock := lap.LorePath("run", "read.sock")
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		defer func() {
			lap.SetEnv("LORE_READ_SERVER", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_LOG", "")
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		lap.Lore(lap.Home, "search", "comet").OK(t) // starts the server
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no read server listens on %s", sock)
			}
		}
		_ = os.Remove(log)
		for range 3 {
			out, errs, codes := pipe()
			if out != oneOut || errs != oneErr || codes != oneCodes {
				t.Errorf("served `lore sql … | head -c 5` ends unlike one-shot:\nserved:   out %q stderr %q codes %q\none-shot: out %q stderr %q codes %q", out, errs, codes, oneOut, oneErr, oneCodes)
			}
		}
		lap.Lore(lap.Home, "search", "fridays").OK(t).Says(t, "comet ships on fridays")
		// A piped call dies of SIGPIPE before it logs "served"; one that ran
		// one-shot would have logged "one-shot" first.
		if b, _ := os.ReadFile(log); string(b) != "served\n" {
			t.Errorf("want the three piped reads served (no one-shot line) and the read after them served, the log says:\n%s", b)
		}
	})
}

// TestJourneyReadServerKeepsItsMemoryBounded (devco:D-845, bug hunt
// H-102): the read server lives for up to an hour and served ~600 MB RSS
// after a few hundred calls: each call's schema registry stayed held by a
// process-wide memo keyed by it. Over 160 served reads (asks, searches,
// sql, lists) the server's live heap after a full collection must stay
// where it was once warm, and every call must be served by the one server.
func TestJourneyReadServerKeepsItsMemoryBounded(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	w.Step("day1/notes", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		for _, b := range []string{"comet beta has 4 teams.", "comet ships on fridays.", "Decision: comet bills monthly because finance asked."} {
			lap.Lore(lap.Home, "memory", "add", "--body", b, "--to", "me/work", "--yes").OK(t)
		}
	})
	w.Step("day1/many-served-reads-stay-small", func() {
		dir := t.TempDir()
		log, heap := filepath.Join(dir, "read-server.log"), filepath.Join(dir, "heap")
		sock := lap.LorePath("run", "read.sock")
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		defer func() {
			lap.SetEnv("LORE_READ_SERVER", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_LOG", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_HEAP", "")
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		lap.Lore(lap.Home, "search", "comet").OK(t) // starts the server
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no read server listens on %s", sock)
			}
		}
		_ = os.Remove(log)
		lap.SetEnv("LORE_TEST_READ_SERVER_HEAP", heap)
		calls := [][]string{
			{"ask", "--store", "me", "--query", "what is the status of comet"},
			{"search", "fridays"},
			{"sql", "--store", "me", "--query", "SELECT count(*) FROM memories"},
			{"ask", "--store", "me", "--query", "what did we decide about billing"},
			{"memory", "list", "--store", "me"},
		}
		const n, warm = 160, 40
		for i := range n {
			lap.Lore(lap.Home, calls[i%len(calls)]...).OK(t)
		}
		if b, _ := os.ReadFile(log); strings.Count(string(b), "served\n") != n || strings.Contains(string(b), "one-shot") {
			t.Fatalf("want all %d reads served by the one server, the log says:\n%s", n, b)
		}
		raw, _ := os.ReadFile(heap)
		var live []int
		for f := range strings.FieldsSeq(string(raw)) {
			v, err := strconv.Atoi(f)
			if err != nil {
				t.Fatalf("heap file holds %q", f)
			}
			live = append(live, v)
		}
		if len(live) != n {
			t.Fatalf("the server reported its heap after %d of %d calls", len(live), n)
		}
		const slack = 6 << 20 // what one more kept lens or a memo refill may add
		base := slices.Max(live[warm-len(calls) : warm])
		top := slices.Max(live[warm:])
		t.Logf("live heap once warm %d KB, highest over the next %d calls %d KB", base>>10, n-warm, top>>10)
		if top > base+slack {
			t.Errorf("the read server's live heap grew from %d KB once warm (call %d) to %d KB over %d more calls: something it keeps across calls has no bound", base>>10, warm, top>>10, n-warm)
		}
	})
}
