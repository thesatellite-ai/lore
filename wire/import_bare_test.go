package wire

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestImportErrorMissingRepoNamesTheURL: `lore org import` of an address
// with no repository behind it says to check the address (a url never
// imported here), not the steps for a known store that moved (store
// set-url --name <org>, store push --restore).
func TestImportErrorMissingRepoNamesTheURL(t *testing.T) {
	const url = "https://github.com/agency/nothere"
	missing := fmt.Errorf("%w: github.com/agency/nothere ('/x/agency/nothere' does not appear to be a git repository)", orgcfg.ErrStoreOffline)
	err := importError(missing, url)
	for _, want := range []string{"E_OFFLINE", "no repository", "check the address", "lore org import --url", url} {
		if !strings.Contains(fullText(err), want) {
			t.Errorf("import of a missing repo: want %q in %q", want, fullText(err))
		}
	}
	for _, wrong := range []string{"store set-url", "--restore", "<org>"} {
		if strings.Contains(fullText(err), wrong) {
			t.Errorf("import of a missing repo names %q, a step for a known store: %q", wrong, fullText(err))
		}
	}
	// a store that does not answer keeps the wait-and-retry answer
	off := fmt.Errorf("%w: github.com/agency/x (Could not resolve host: github.com)", orgcfg.ErrStoreOffline)
	if got := fullText(importError(off, url)); !strings.Contains(got, "retry") {
		t.Errorf("an unreachable store says retry: %q", got)
	}
}

// TestBareLinesNameTheBareRepo: `lore sync status` (and `lore sync`) in a
// bare repository answer for the setup's stores and still say what the
// folder is and the way to its repo status: a clone.
func TestBareLinesNameTheBareRepo(t *testing.T) {
	ctx := context.Background()
	bare, plain := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	got := strings.Join(bareLines(ctx, bare), "\n")
	if !strings.Contains(got, "bare repository") || !strings.Contains(got, "git clone") || strings.Contains(got, "git init") {
		t.Errorf("in a bare repo: %q", got)
	}
	if l := bareLines(ctx, plain); len(l) != 0 {
		t.Errorf("a plain folder gets no bare line: %q", l)
	}
}

// fullText is an error's message with its hint, as the CLI prints both.
func fullText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	var e *errs.Error
	if errors.As(err, &e) {
		s += " " + string(e.Code) + " " + e.Hint
	}
	return s
}
