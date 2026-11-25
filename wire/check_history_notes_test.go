package wire

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestHistoryScanNamesInternalNotes (devco D-994, bug hunt H-262, owner
// org questions (a)): `lore check leaks --history` applies the
// internal-note rules to every file version and commit message, not only
// the secret rules. A note pasted into a file and removed later is named
// with the commit that added it, its path and line, and its
// `lore leaks allow --file` line; a note kept through several versions is
// one finding; a note in a commit message is named; a text allow for the
// passage lets it through (counted on the ✓ line); with no note check (an
// own repo) the ✓ line says notes were not checked. No note text is
// printed. Break-verified: with blobHits and msgHits returning nothing,
// the refusal cases pass clean.
//
// Not parallel: it swaps the package's historyProgress (as
// TestHistoryScanNamesRemovedSecrets does) and restores it, before the
// parallel tests run.
func TestHistoryScanNamesInternalNotes(t *testing.T) {
	ctx := context.Background()
	const noteID = "mem_019a0000000070008000000000000001"
	note := "the widgets client keeps its forty percent discount until the spring pricing review ends"
	dir := t.TempDir()
	gitInitIn(t, dir)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test"}, args...)...)
		cmd.Dir = dir
		cmd.Env = gitenv.Clean(os.Environ())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "--no-verify", "-m", msg)
	}
	var progress bytes.Buffer
	prev := historyProgress
	historyProgress = &progress
	t.Cleanup(func() { historyProgress = prev })
	idx := leakcheck.NewIndex([]leakcheck.IndexRow{{ID: noteID, Table: "memories", Label: "store acme", Texts: []string{note}}}, nil)
	notesWith := func(allows []leakcheck.TextAllow) *historyNotes {
		opts := leakcheck.Options{ClientRepo: true, TextAllows: allows}
		return &historyNotes{asClient: true, run: func(items []leakcheck.Item) []leakcheck.Finding {
			return leakcheck.Check(items, idx, opts)
		}}
	}
	check := func(at historyPlace) (string, error) {
		t.Helper()
		sc, err := newScanner(ctx, nil, withRepoGitleaks(dir))
		if err != nil {
			t.Fatal(err)
		}
		return scanHistory(ctx, dir, []string{"HEAD"}, sc, hookAllows{}, nil, at)
	}

	write("README.md", "# widgets\n")
	commit("init")
	write("PRICING.md", "# Pricing\n\n"+note+".\n")
	commit("pricing")
	added := gitHead(t, dir)
	write("PRICING.md", "# Pricing\n\n"+note+".\n\nmore\n")
	commit("pricing, more")
	git("rm", "-q", "PRICING.md")
	commit("drop pricing: " + note)
	tip := gitHead(t, dir)

	// an own repo (no note check): clean, and the ✓ line says notes were not read
	msg, err := check(historyPlace{notesSkipped: historyNotesSkippedOwn})
	if err != nil || !strings.Contains(msg, "no secret in 4 commit(s)") || !strings.Contains(msg, "internal notes not checked: this is an own repo") {
		t.Fatalf("own repo: %q, %v", msg, err)
	}

	_, err = check(historyPlace{notes: notesWith(nil)})
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED for a note removed after it was committed", err)
	}
	out := err.Error() + "\nhint: " + hintOf(err)
	for _, want := range []string{
		"2 internal note(s) in the history of HEAD (4 commits", "read as the client would",
		shortSHA(added) + " PRICING.md:3", "internal: matches internal note " + noteID, "(removed since, still in history, in 2 versions of the file)",
		"lore leaks allow --note " + noteID + " --file PRICING.md --match-hash ",
		shortSHA(tip) + " commit message", "the client receives every commit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the history refusal misses %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "PRICING.md:3 ") != 1 || strings.Contains(out, "secret(s)") {
		t.Errorf("one note in two versions is listed more than once, or secrets were counted:\n%s", out)
	}
	if strings.Contains(out, "forty percent discount") || strings.Contains(progress.String(), "forty") {
		t.Fatalf("the history check printed the note's words:\n%s", out)
	}

	// the printed allow line's passage (this file only) lets the file's versions through
	i := strings.Index(out, "--match-hash ")
	hash, perr := strconv.ParseUint(strings.Fields(out[i+len("--match-hash "):])[0], 10, 32)
	if perr != nil {
		t.Fatalf("no hash in %q: %v", out, perr)
	}
	allow := leakcheck.TextAllow{ID: "sal_019a0000000070008000000000000002", RowID: noteID, Hash: uint32(hash), Path: "PRICING.md", Reason: "approved public copy"}
	_, err = check(historyPlace{notes: notesWith([]leakcheck.TextAllow{allow})})
	if err == nil || strings.Contains(err.Error(), "PRICING.md:3") || !strings.Contains(err.Error(), "1 internal note(s)") {
		t.Fatalf("the allow did not let the file's passage through, or the commit message was let through too: %v", err)
	}
	allow.Path = ""
	msg, err = check(historyPlace{notes: notesWith([]leakcheck.TextAllow{allow})})
	if err != nil || !strings.Contains(msg, "no secret or internal note in 4 commit(s)") || !strings.Contains(msg, "passage(s) a lore leaks allow entry lets through") {
		t.Fatalf("with an allow for every path: %q, %v", msg, err)
	}
}
