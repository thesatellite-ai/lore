//go:build journeys && !windows

package journeys

// readserver_test.go — devco:D-833 (bug hunt H-90): a warm `lore ask`
// could not answer in under 50 ms one-shot, so a local read server runs
// reads with its caches open. It runs each command in the caller's folder
// with the caller's environment: an ask from inside a code repo (the repo
// changes what a read covers), with --json, and a search must answer as
// the one-shot process does, and a write is never handed to it.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
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

// TestJourneyReadServerStaysFlatAcrossWritesToABigFolder (devco:D-866,
// public issue #96): every write to a big store folder made a new version
// of its table tree, and the read server's kept git object store parsed
// and kept each one for good, so the server grew ~1.3 MB of live heap per
// write to a 5,000-row folder until it exited on its live-heap limit and
// the calls ran one-shot. A folder of a few thousand rows committed by
// hand (an import), then rounds of one write and one served ask: every
// ask is served by the one server and its live heap stays where it was
// once warm.
func TestJourneyReadServerStaysFlatAcrossWritesToABigFolder(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	const rows = 2500
	w.Step("day1/an-import-fills-the-feeds-folder", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		out := lap.Lore(lap.Home, "memory", "add", "--to", "me/feeds", "--create", "--body", "seed event: Dana reviews the deploy checklist on mondays.", "--yes").OK(t)
		seed := regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(out.Out())
		if seed == "" {
			t.Fatalf("no memory id in: %s", out.Out())
		}
		seedFile := strings.TrimSpace(lap.Lore(lap.Home, "path", "--id", seed).OK(t).Stdout)
		doc, err := os.ReadFile(seedFile)
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			id := fmt.Sprintf("%s%012x", seed[:len(seed)-12], 0x100000+i) // same prefix and version bits, new tail
			body := strings.Replace(strings.ReplaceAll(string(doc), seed, id), "seed event", fmt.Sprintf("feed event %d", i), 1)
			if err := os.WriteFile(filepath.Join(filepath.Dir(seedFile), id+".json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		store := &teamharness.Clone{A: lap.Actor, Dir: filepath.Dir(filepath.Dir(filepath.Dir(seedFile)))}
		store.Git("add", "-A")
		store.Git("commit", "-q", "-m", "import feed events")
	})
	w.Step("day1/writes-between-served-asks-keep-the-server-flat", func() {
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
		ask := []string{"ask", "--store", "me", "--query", "who reviews the deploy checklist"}
		lap.Lore(lap.Home, ask...).OK(t) // imports the rows and starts the server
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
		const n, warm = 36, 8
		for i := range n {
			lap.Lore(lap.Home, "memory", "add", "--to", "me/feeds", "--body", fmt.Sprintf("cycle %d: Dana moved the deploy checklist review to slot %d.", i, i), "--yes").OK(t)
			lap.Lore(lap.Home, ask...).OK(t)
		}
		if b, _ := os.ReadFile(log); strings.Count(string(b), "served\n") != n || strings.Contains(string(b), "one-shot") {
			t.Fatalf("want all %d asks served by the one server (the writes run one-shot and are not logged), the log says:\n%s", n, b)
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
		base := slices.Max(live[warm-4 : warm])
		top := slices.Max(live[warm:])
		t.Logf("live heap once warm %d KB, highest over the next %d write+ask rounds %d KB", base>>10, n-warm, top>>10)
		if top > base+slack {
			t.Errorf("the read server's live heap grew from %d KB once warm (round %d) to %d KB over %d more write+ask rounds of a %d-row folder: it keeps every version a write replaced", base>>10, warm, top>>10, n-warm, rows)
		}
	})
}

// TestJourneyReadServerAnswersAReleasedLore (devco:D-864, bug hunt H-130,
// public issue #94): a release build passes its version to the app; the
// client stamped its call before that version was applied, so its read
// server declined every read as "another lore binary" and each one ran
// one-shot. A lore built with a version, reached through a package
// manager's symlink and through its real path, must have every read
// served by the one server it started; a copy of the same bytes (another
// file) is never answered.
func TestJourneyReadServerAnswersAReleasedLore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/comet"), "comet")
	dir := t.TempDir()
	rel := filepath.Join(dir, "cellar", "lore")
	if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-ldflags", "-s -w -X main.version=0.99.1", "-o", rel, "./cmd/lore")
	c.Dir = filepath.Join("..", "..")
	c.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("build a release-shaped lore: %v: %s", err, out)
	}
	link := filepath.Join(dir, "bin", "lore")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, link); err != nil {
		t.Fatal(err)
	}
	w.Step("day1/notes", func() {
		withBin(t, rel, code, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		withBin(t, rel, code, "memory", "add", "--body", "Dana reviews the comet deploy checklist on mondays.", "--to", "me/work", "--create", "--yes").OK(t)
	})
	w.Step("day1/release-reads-served", func() {
		q := []string{"ask", "--store", "me", "--query", "who reviews the comet deploy checklist"}
		oneShot := withBin(t, rel, code, q...).OK(t).Says(t, "Dana reviews")
		log := filepath.Join(t.TempDir(), "read-server.log")
		sock := lap.LorePath("run", "read.sock")
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		defer func() {
			lap.SetEnv("LORE_READ_SERVER", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_LOG", "")
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		withBin(t, link, code, q...).OK(t) // starts the server; runs one-shot itself
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("[D-864] no read server listens on %s after a read of lore 0.99.1 started it", sock)
			}
		}
		_ = os.Remove(log)
		for _, bin := range []string{rel, link, rel, link} {
			if got := withBin(t, bin, code, q...); got.Stdout != oneShot.Stdout || got.Code != oneShot.Code {
				t.Errorf("[D-864] served ask through %s differs from one-shot:\nserved: %s\none-shot: %s", bin, got, oneShot)
			}
		}
		if b, _ := os.ReadFile(log); string(b) != strings.Repeat("served\n", 4) {
			t.Errorf("[D-864] want all 4 reads of the released lore served (symlink and real path alike), the log says:\n%s", b)
		}
		cp := filepath.Join(dir, "copy", "lore")
		if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cp, b, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(log)
		withBin(t, cp, code, q...).OK(t).Says(t, "Dana reviews")
		if b, _ := os.ReadFile(log); string(b) != "one-shot\n" {
			t.Errorf("[D-864] a copy of the binary (another file) must run one-shot, never be answered; the log says:\n%s", b)
		}
	})
}

// TestJourneyReadServerLimitsFollowWhatItKeeps (devco:D-887, public issue
// #96 follow-up): lore ask keeps every row's text of the stores it reads,
// so on a big store what the read server keeps on purpose passed its
// fixed live-heap limit and it exited after every call (each ask ran
// one-shot, ~4x slower at 100,000 rows). Its limits now add what it keeps.
// A folder of 3,000 rows of ~8 KB committed by hand (an import); the
// server reports its live heap and kept bytes; a new server is given a
// base live heap between its working heap and its live heap (half the
// kept bytes below what it holds): with limits sized to the kept state it
// serves every ask, with the fixed base it would exit after the first.
func TestJourneyReadServerLimitsFollowWhatItKeeps(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	const rows = 3000
	filler := strings.Repeat("the checklist covers rollback, owners and alerts. ", 160) // ~8 KB
	w.Step("day1/an-import-fills-the-feeds-folder", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		out := lap.Lore(lap.Home, "memory", "add", "--to", "me/feeds", "--create", "--body", "seed event: Dana reviews the deploy checklist on mondays.", "--yes").OK(t)
		seed := regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(out.Out())
		if seed == "" {
			t.Fatalf("no memory id in: %s", out.Out())
		}
		seedFile := strings.TrimSpace(lap.Lore(lap.Home, "path", "--id", seed).OK(t).Stdout)
		doc, err := os.ReadFile(seedFile)
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			id := fmt.Sprintf("%s%012x", seed[:len(seed)-12], 0x100000+i)
			body := strings.Replace(strings.ReplaceAll(string(doc), seed, id), "seed event", fmt.Sprintf("feed event %d: %s", i, filler), 1)
			if err := os.WriteFile(filepath.Join(filepath.Dir(seedFile), id+".json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		store := &teamharness.Clone{A: lap.Actor, Dir: filepath.Dir(filepath.Dir(filepath.Dir(seedFile)))}
		store.Git("add", "-A")
		store.Git("commit", "-q", "-m", "import feed events")
	})
	w.Step("day1/a-server-keeping-big-answers-keeps-serving", func() {
		dir := t.TempDir()
		log, heap := filepath.Join(dir, "read-server.log"), filepath.Join(dir, "heap")
		sock := lap.LorePath("run", "read.sock")
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		defer func() {
			lap.SetEnv("LORE_READ_SERVER", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_LOG", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_HEAP", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_BASE_LIVE", "")
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		ask := []string{"ask", "--store", "me", "--query", "who reviews the deploy checklist"}
		startServer := func() {
			t.Helper()
			_ = os.Remove(sock) // a running server sees it gone and exits
			// wait until no server holds the home's lock: a server started
			// while the old one still runs finds it held and exits at once
			lock, err := os.OpenFile(lap.LorePath("run", "read.lock"), os.O_RDWR, 0)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				lock, _ = os.Open(os.DevNull) // no server ran yet: nothing to wait for
			case err != nil:
				t.Fatal(err)
			}
			for deadline := time.Now().Add(20 * time.Second); syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil; time.Sleep(50 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the old read server still runs 20 s after its socket was removed")
				}
			}
			_ = lock.Close() // releases the lock
			_ = os.Remove(lap.LorePath("run", "read.start"))
			lap.Lore(lap.Home, ask...).OK(t) // starts a server
			for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
				if _, err := os.Stat(sock); err == nil {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("no read server listens on %s", sock)
				}
			}
		}
		numbers := func(p string) []int64 {
			t.Helper()
			raw, _ := os.ReadFile(p)
			var v []int64
			for f := range strings.FieldsSeq(string(raw)) {
				n, err := strconv.ParseInt(f, 10, 64)
				if err != nil {
					t.Fatalf("%s holds %q", p, f)
				}
				v = append(v, n)
			}
			return v
		}
		startServer()
		lap.SetEnv("LORE_TEST_READ_SERVER_HEAP", heap)
		const warm = 4
		for range warm {
			lap.Lore(lap.Home, ask...).OK(t)
		}
		live, kept := numbers(heap), numbers(heap+".kept")
		if len(live) != warm || len(kept) != warm {
			t.Fatalf("the server reported its heap after %d and its kept bytes after %d of %d asks", len(live), len(kept), warm)
		}
		k := kept[warm-1]
		if k < rows*8000 {
			t.Fatalf("the server keeps %d KB after asks over %d rows of ~8 KB, want at least their text (ask keeps every row's text)", k>>10, rows)
		}
		var working int64
		for i := range warm {
			working = max(working, live[i]-kept[i])
		}
		base := working + k/2 // under what it holds, over its working heap
		t.Logf("live heap %d KB, kept %d KB, working heap %d KB: base live heap %d KB", live[warm-1]>>10, k>>10, working>>10, base>>10)
		lap.SetEnv("LORE_TEST_READ_SERVER_HEAP", "")
		lap.SetEnv("LORE_TEST_READ_SERVER_BASE_LIVE", strconv.FormatInt(base, 10))
		startServer() // takes the base from the call that starts it
		lap.SetEnv("LORE_TEST_READ_SERVER_BASE_LIVE", "")
		_ = os.Remove(log)
		// no heap report now: it collects before reading, and a release
		// build reads the live heap as of the last collection, which may
		// have run mid-call (overLiveLimit collects before it exits)
		const n = 8
		for range n {
			lap.Lore(lap.Home, ask...).OK(t)
		}
		if b, _ := os.ReadFile(log); strings.Count(string(b), "served\n") != n || strings.Contains(string(b), "one-shot") {
			t.Errorf("want all %d asks served by the one server (its limits add the %d KB it keeps), the log says:\n%s", n, k>>10, b)
		}
	})
}

// TestJourneyReadServerKeptOpenSeesHandEdits (bug hunt H-216): the read
// server keeps a repo's whole open (every attached store's backend, the
// caller's identity, the cache refresh, the pick-up of hand edits) across
// commands while none of the files it was built from changed, so a warm
// ask stops opening everything again. A hand edit in a store clone and in
// the repo's .lore, even one that keeps the file's size, must still show
// in the very next served read, as it shows one-shot.
func TestJourneyReadServerKeptOpenSeesHandEdits(t *testing.T) {
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
	// edit replaces old with new (same length) in the one row file under
	// root that holds old
	edit := func(root, old, new string) {
		t.Helper()
		var hit string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() && d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), old) {
				hit = p
			}
			return nil
		})
		if hit == "" {
			t.Fatalf("no row file under %s holds %q", root, old)
		}
		b, err := os.ReadFile(hit)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hit, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w.Step("day1/served-reads-see-hand-edits", func() {
		log := filepath.Join(t.TempDir(), "read-server.log")
		sock := lap.LorePath("run", "read.sock")
		lap.SetEnv("LORE_READ_SERVER", "1")
		lap.SetEnv("LORE_TEST_READ_SERVER_LOG", log)
		defer func() {
			lap.SetEnv("LORE_READ_SERVER", "")
			lap.SetEnv("LORE_TEST_READ_SERVER_LOG", "")
			_ = os.Remove(sock) // the server stops once its socket is gone
		}()
		lap.LoreIn(code, "search", "comet").OK(t) // starts the server
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(sock); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no read server listens on %s", sock)
			}
		}
		_ = os.Remove(log)
		mine := []string{"ask", "--store", "me", "--query", "what is the status of comet"}
		ours := []string{"search", "comet"}
		// served reads run back to back, so each after the first is handed
		// the open the one before kept (a one-shot run between them would
		// move the caches and make the next one open afresh)
		lap.LoreIn(code, ours...).OK(t).Says(t, "make release")
		lap.LoreIn(code, ours...).OK(t).Says(t, "make release")
		edit(filepath.Join(code.Dir, ".lore"), "make release", "make RELEASE") // same size: only the content changes
		lap.LoreIn(code, ours...).OK(t).Says(t, "make RELEASE").Never(t, "make release")
		lastOurs := lap.LoreIn(code, ours...).OK(t).Says(t, "make RELEASE")
		lap.LoreIn(code, mine...).OK(t).Says(t, "beta with 4 teams")
		lap.LoreIn(code, mine...).OK(t).Says(t, "beta with 4 teams")
		edit(lap.LorePath("stores"), "beta with 4 teams", "beta with 9 teams")
		lastMine := lap.LoreIn(code, mine...).OK(t)
		// the repo's reads attach the store's folder too: the edit there
		// (which waits for a command inside that folder) is said by every
		// read, the second one included
		lap.LoreIn(code, ours...).OK(t).Says(t, "waits")
		lastOurs = lap.LoreIn(code, ours...).OK(t).Says(t, "waits")
		if b, _ := os.ReadFile(log); strings.Count(string(b), "served\n") != 9 || strings.Contains(string(b), "one-shot") {
			t.Errorf("want each read served, the log says:\n%s", b)
		}
		lap.SetEnv("LORE_READ_SERVER", "0") // one-shot from here
		for _, c := range []struct {
			args   []string
			served teamharness.Result
		}{{ours, lastOurs}, {mine, lastMine}} {
			if got := lap.LoreIn(code, c.args...); got.Stdout != c.served.Stdout || got.Stderr != c.served.Stderr {
				t.Errorf("served `lore %s` after a hand edit differs from one-shot:\nserved: %s\none-shot: %s", strings.Join(c.args, " "), c.served, got)
			}
		}
	})
}
