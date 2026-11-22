package repoguard

// The docs runners (tests/docs and tests/team/docs) paste transcripts of
// real lore runs into the pages, and `paste --check` fails when a page
// differs from a fresh run. A machine id (mch_ + 64 hex, or its 8-hex short
// form) depends on the host, so a normaliser that left it raw made the
// check go stale on every other machine (run:docs:normalize-machine-id).
// Two runs on two hosts must normalise to the same text.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsNormaliseMachineIDs(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the docs runners need it and run in the full gate")
	}
	transcript := func(full string) string {
		return "$ lore machine show\nmachine mch_" + full + "\n" +
			"$ lore doctor\n  this machine: mch_" + full[:8] + "\n" +
			"$ lore task show tsk_0123456789abcdef0123456789abcdef\n  claimed on mch_" + full[:8] + "\n"
	}
	hostA := strings.Repeat("a1", 32)
	hostB := strings.Repeat("b2", 32)
	for _, script := range []string{"tests/docs/normalize.py", "tests/team/docs/normalize.py"} {
		run := func(text string) string {
			p := filepath.Join(t.TempDir(), "t.txt")
			if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(py, filepath.Join(repoRoot, script), p).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", script, err, out)
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			return string(got)
		}
		a, b := run(transcript(hostA)), run(transcript(hostB))
		if a != b {
			t.Errorf("%s: two hosts' transcripts normalise differently:\n%s\n---\n%s", script, a, b)
		}
		for _, raw := range []string{hostA[:8], hostB[:8]} {
			if strings.Contains(a+b, raw) {
				t.Errorf("%s: the host's machine id %s… survived:\n%s", script, raw, a)
			}
		}
	}
}

// TestDocsNormaliseKeepsMatchHash (H-173, devco:D-915): the hash after
// --match-hash in a printed allow command (`lore leaks allow --match-hash
// 1314822779`, `lore secrets allow --match-hash sha256:…`) depends only on
// the matched text, so it is the same on every run. The normalisers took a
// 10-digit one for a git object id and printed a hex stand-in, so the docs
// showed an allow command whose hash matches nothing. A sha elsewhere on the
// line still gets its stand-in.
func TestDocsNormaliseKeepsMatchHash(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the docs runners need it and run in the full gate")
	}
	const transcript = "$ git commit -m 'add notes'\n[main 4f2a9c1] add notes\n" +
		"  hint: lore leaks allow --note mem_0123456789abcdef0123456789abcdef --file src/notes.ts --match-hash 1314822779 --reason \"<why it is public>\"\n" +
		"  not a secret? lore secrets allow --row-id tsk_0123456789abcdef0123456789abcdef --field title --rule aws_access_key --match-hash sha256:1a5d44a2dca1 --reason \"<why>\"\n"
	for _, script := range []string{"tests/docs/normalize.py", "tests/team/docs/normalize.py"} {
		p := filepath.Join(t.TempDir(), "t.txt")
		if err := os.WriteFile(p, []byte(transcript), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(py, filepath.Join(repoRoot, script), p).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got := string(b)
		for _, keep := range []string{"--match-hash 1314822779 ", "--match-hash sha256:1a5d44a2dca1 "} {
			if !strings.Contains(got, keep) {
				t.Errorf("%s: %q was rewritten:\n%s", script, keep, got)
			}
		}
		if strings.Contains(got, "4f2a9c1") {
			t.Errorf("%s: the commit sha 4f2a9c1 kept its run's value; it needs a stand-in:\n%s", script, got)
		}
	}
}
