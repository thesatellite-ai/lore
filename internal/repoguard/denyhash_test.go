package repoguard

// denyhash_test.go — devco D-981: names that must never appear in this public
// repository (clients, companies, people, private hosts) once reached tests
// and docs. tests/lib/denyhash.txt lists the sha256 of each such word,
// lowercased, and never the word itself, so the list can be public while
// the words stay private. This guard tokenizes every tracked text file and
// fails, naming file:line but never the word, when a token hashes into the
// list. tests/lib/denyhash.sh does the same for one file or stdin; the
// pre-push hook runs it on the commits being pushed (author, committer,
// message and added lines).
//
// A token is a run of letters, digits and _ . @ -, lowercased, with leading
// and trailing . @ - trimmed. Each token is checked whole, as each part split
// at . @ - _, and as each run of adjacent parts split at . and @ (so a domain
// is caught inside a longer host name or an email). Two adjacent tokens are
// also checked joined by one space when only whitespace (a line break
// included) separates them, for listed phrases of two words.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// denyHashFile is the committed list of denied-word hashes.
var denyHashFile = filepath.Join(repoRoot, "tests", "lib", "denyhash.txt")

// denyHashScript checks one file or stdin against the same list.
var denyHashScript = filepath.Join(repoRoot, "tests", "lib", "denyhash.sh")

// denyToken matches one token: the characters a name, a domain or an email
// address is spelled with.
var denyToken = regexp.MustCompile(`[A-Za-z0-9_.@-]+`)

// hexSHA256 is one line of denyhash.txt.
var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// denyHit is one denied token found: where, never what.
type denyHit struct {
	file string
	line int
}

func (h denyHit) String() string { return fmt.Sprintf("%s:%d", h.file, h.line) }

// loadDenyHashes reads a denyhash.txt: comments (#) and blank lines are
// skipped; every other line must be one lowercase hex sha256.
func loadDenyHashes(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a repo file or a test's temp file
	if err != nil {
		return nil, fmt.Errorf("loadDenyHashes: %w", err)
	}
	out := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !hexSHA256.MatchString(line) {
			return nil, fmt.Errorf("loadDenyHashes: %s:%d is not a lowercase hex sha256 (the list holds hashes only)", path, i+1)
		}
		out[line] = true
	}
	return out, nil
}

// denyHashOf is the list's hash of one lowercased word or phrase.
func denyHashOf(word string) string {
	sum := sha256.Sum256([]byte(word))
	return hex.EncodeToString(sum[:])
}

// denyCandidates lists every spelling of one lowercased token the guard
// checks: the token, its parts at . @ - _, and each run of its parts at . @.
func denyCandidates(tok string) []string {
	out := []string{tok}
	out = append(out, strings.FieldsFunc(tok, func(r rune) bool { return strings.ContainsRune(".@-_", r) })...)
	parts := strings.FieldsFunc(tok, func(r rune) bool { return r == '.' || r == '@' })
	for i := range parts {
		for j := i + 1; j <= len(parts); j++ {
			out = append(out, strings.Join(parts[i:j], "."))
		}
	}
	return out
}

// scanDenied returns the lines of text holding a denied token or a denied
// two-word phrase. The phrase check pairs adjacent tokens across lines too
// (a hard-wrapped comment), reported at the first token's line.
func scanDenied(text []byte, deny map[string]bool) []int {
	var lines []int
	seen := map[int]bool{}
	hit := func(line int) {
		if !seen[line] {
			seen[line] = true
			lines = append(lines, line)
		}
	}
	// prev is the last token while only whitespace follows it; a token
	// after anything else starts a new phrase.
	prev, prevLine := "", 0
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 0, 1<<16), 1<<26)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		end := 0
		for _, loc := range denyToken.FindAllStringIndex(line, -1) {
			if strings.TrimSpace(line[end:loc[0]]) != "" {
				prev = ""
			}
			end = loc[1]
			tok := strings.Trim(strings.ToLower(line[loc[0]:loc[1]]), ".@-")
			if tok == "" {
				prev = ""
				continue
			}
			for _, c := range denyCandidates(tok) {
				if deny[denyHashOf(c)] {
					hit(n)
					break
				}
			}
			if prev != "" && deny[denyHashOf(prev+" "+tok)] {
				hit(prevLine)
			}
			prev, prevLine = tok, n
		}
		if strings.TrimSpace(line[end:]) != "" {
			prev = ""
		}
	}
	return lines
}

// isBinary reports a file git would call binary: a NUL in its first 8000 bytes.
func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

// scanTrackedFiles checks every tracked text file under root.
func scanTrackedFiles(t *testing.T, root string, deny map[string]bool) []denyHit {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var hits []denyHit
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a tracked file of this repo
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // deleted in the working tree, not yet committed
			}
			t.Fatalf("read %s: %v", rel, err)
		}
		if isBinary(b) {
			continue // model data and images: not text a person wrote
		}
		for _, n := range scanDenied(b, deny) {
			hits = append(hits, denyHit{file: rel, line: n})
		}
		// a file or folder name is checked as one more line
		if len(scanDenied([]byte(rel), deny)) > 0 {
			hits = append(hits, denyHit{file: rel, line: 0})
		}
	}
	return hits
}

// TestNoDeniedWordsInTrackedFiles is the guard: no tracked file holds a word
// whose hash is in tests/lib/denyhash.txt. On a failure, replace the word on
// each named line with a made-up name (acme, alice, ...); the list never says
// which word it was.
func TestNoDeniedWordsInTrackedFiles(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	deny, err := loadDenyHashes(denyHashFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(deny) == 0 {
		t.Fatalf("%s lists no hashes", denyHashFile)
	}
	hits := scanTrackedFiles(t, repoRoot, deny)
	for _, h := range hits {
		t.Errorf("%s: a denied word (its sha256 is in tests/lib/denyhash.txt); replace it with a made-up name", h)
	}
}

// writeDenyList writes a temp denyhash.txt listing the given made-up words.
func writeDenyList(t *testing.T, words ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("# test list\n")
	for _, w := range words {
		b.WriteString(denyHashOf(w) + "\n")
	}
	p := filepath.Join(t.TempDir(), "denyhash.txt")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDenyScanSpellings pins which spellings the tokenizer catches, with
// made-up words only.
func TestDenyScanSpellings(t *testing.T) {
	t.Parallel()
	deny, err := loadDenyHashes(writeDenyList(t, "zorblax", "qux.example", "flim flam"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		text string
		hit  bool
	}{
		{"plain zorblax here", true},
		{"Zorblax, capitalised", true},
		{"repo zorblax_app and zorblax-web", true},
		{"mail ann@zorblax.io", true},
		{"host www.qux.example.", true},
		{"mail bob@qux.example", true},
		{"the flim flam phrase", true},
		{"wrapped flim\nflam", true},
		{"zorblaxing is not the word", false},
		{"unzorblax neither", false},
		{"qux and example apart", false},
		{"flim, flam", false}, // a phrase is two tokens with only whitespace between
		{"flim alone, then flam", false},
	}
	for _, c := range cases {
		if got := len(scanDenied([]byte(c.text), deny)) > 0; got != c.hit {
			t.Errorf("scan %q: hit %v, want %v", c.text, got, c.hit)
		}
	}
}

// TestDenyHashListHoldsHashesOnly: the committed list is comments and
// lowercase hex sha256 lines only, so it can never carry a word in clear.
func TestDenyHashListHoldsHashesOnly(t *testing.T) {
	t.Parallel()
	if _, err := loadDenyHashes(denyHashFile); err != nil {
		t.Fatal(err)
	}
}

// TestDenyGuardFailsNamingFileNotWord break-verifies the guard on a scratch
// repository: a tracked file holding a listed made-up word fails, naming the
// file and line, and the report never prints the word.
func TestDenyGuardFailsNamingFileNotWord(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	deny, err := loadDenyHashes(writeDenyList(t, "zorblax"))
	if err != nil {
		t.Fatal(err)
	}
	dir := gitInitRepo(t)
	for rel, body := range map[string]string{"ok.md": "nothing here\n", "docs/bad.md": "line one\nthe zorblax repo\n"} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	hits := scanTrackedFiles(t, dir, deny)
	if len(hits) != 1 || hits[0].String() != "docs/bad.md:2" {
		t.Fatalf("hits = %v, want [docs/bad.md:2]", hits)
	}
	if strings.Contains(hits[0].String(), "zorblax") {
		t.Fatalf("the report names the word: %s", hits[0])
	}
}

// TestDenyHashScript runs tests/lib/denyhash.sh (what the pre-push hook and
// the landing call) on a file and on stdin: a hit exits 1 naming the line and
// never the word; clean text exits 0. It agrees with the Go scan.
func TestDenyHashScript(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	list := writeDenyList(t, "zorblax", "qux.example", "flim flam")
	run := func(stdin string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{denyHashScript}, args...)...)
		cmd.Env = append(os.Environ(), "DENYHASH_FILE="+list)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("denyhash.sh: %v", err)
		}
		return string(out), code
	}
	for _, in := range []string{"one\nthe Zorblax-web repo\n", "a\nb\nmail bob@qux.example\n", "x\nthe flim flam phrase\n"} {
		out, code := run(in, "-")
		if code != 1 || !strings.Contains(out, "line ") || strings.Contains(strings.ToLower(out), "zorblax") || strings.Contains(out, "qux") || strings.Contains(out, "flim") {
			t.Errorf("stdin %q: exit %d, out %q; want exit 1 naming a line, never the word", in, code, out)
		}
	}
	f := filepath.Join(t.TempDir(), "msg.txt")
	if err := os.WriteFile(f, []byte("first\nsecond\nzorblax third\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := run("", f); code != 1 || !strings.Contains(out, "line 3") {
		t.Errorf("file: exit %d, out %q; want exit 1 naming line 3", code, out)
	}
	if out, code := run("zorblaxing and unzorblax are fine\nflim, flam\n", "-"); code != 0 {
		t.Errorf("clean text: exit %d, out %q; want 0", code, out)
	}
}

// TestPrePushRefusesDeniedWord: the pre-push hook refuses a commit whose
// message, author or added lines hold a listed (made-up) word, naming the
// commit and never the word, before any gate runs; a clean commit goes on.
func TestPrePushRefusesDeniedWord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		commit func(r *hookRepo)
	}{
		{"message", func(r *hookRepo) {
			r.write("a.txt", "two\n")
			r.git("commit", "-qam", "fix: the Zorblax repo")
		}},
		{"author", func(r *hookRepo) {
			r.write("a.txt", "two\n")
			r.git("-c", "user.email=dev@zorblax.example", "commit", "-qam", "fix: plain")
		}},
		{"added line", func(r *hookRepo) {
			r.write("a.txt", "two\nthe zorblax_app repo\n")
			r.git("commit", "-qam", "fix: plain")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newHookRepo(t)
			r.env = []string{"DENYHASH_FILE=" + writeDenyList(t, "zorblax")}
			r.standInTask(standInPass)
			tc.commit(r)
			head := r.git("rev-parse", "HEAD")
			code, out, runs := r.push(head, r.pathWithTask())
			if code == 0 || runs != 0 || !strings.Contains(out, head) || !strings.Contains(out, "a denied word") || strings.Contains(strings.ToLower(out), "zorblax") {
				t.Fatalf("want refused naming %s and never the word, no gate run: %d runs=%d %s", head, code, runs, out)
			}
		})
	}
	r := newHookRepo(t)
	r.env = []string{"DENYHASH_FILE=" + writeDenyList(t, "zorblax")}
	r.standInTask(standInPass)
	r.write("a.txt", "two\nunzorblax is fine\n")
	r.git("commit", "-qam", "fix: zorblaxing is not the word")
	if code, out, runs := r.push(r.git("rev-parse", "HEAD"), r.pathWithTask()); code != 0 || runs != 1 {
		t.Fatalf("a clean commit must go on to the gate: %d runs=%d %s", code, runs, out)
	}
}
