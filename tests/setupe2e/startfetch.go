//go:build setupe2e

package setupe2e

// startfetch.go — the judge of the startup fetch of a v0.1.17 store.
//
// A v0117 home names its old personal store as personal.store:
// git@github.com:<owner>/lore-me.git (world.go StoreOwner, seedOldStore).
// `lore setup web` started in a lore repo updates the schema first, which
// opens the attached stores and so fetches that store over ssh before the
// page link is printed. For every v0117 shape:
//
//   - the fetch happens exactly when it should: an account owns the store
//     AND the start folder is a lore repo. Documented skips, asserted as
//     "no call": with no gh account the old store is `local` (no remote);
//     outside a lore repo the page opens no store (ops/setup/setup.go Run,
//     A13 bug 3), so nothing is fetched.
//   - a fetch that authenticated as the store's owner succeeded: no
//     "schema update skipped" line;
//   - a fetch that did not (another login's key, or no key at all: ssh's
//     own config picks the key, as v0.1.17 did, since no setup names the
//     account yet) is reported as one typed line naming the access
//     problem (adapters/backend/git msgAccessRefused), never as "cannot be
//     reached" / "go online" and never with ssh's raw text;
//   - the page still answers (Walk goes on to setup.list and setup.plan;
//     World.Start already failed a server that printed no link).

import (
	"fmt"
	"strings"
)

// Lines the server prints (ops/setup/setup.go schemaSkippedLine,
// adapters/backend/git msgAccessRefused). Copied, not imported: the judge
// checks the text a person reads.
const (
	startSkipped       = "schema update skipped"
	startAccessRefused = "its remote refused this machine's access"
)

// rawSSHText is ssh's / git's own wording, which the startup line must
// not show (fakes.go sshScript prints exactly these).
var rawSSHText = []string{"Permission denied", "Repository not found", "Could not read from remote repository", "fatal:"}

// judgeStartFetch judges the startup ssh lines (startSSH: the log as it
// stood when the page link was printed) and the server output so far.
func judgeStartFetch(o *Outcome, w *World, startSSH []LogLine, output string) {
	if w.Shape.LoreHome != HomeV0117 {
		return
	}
	owner := w.StoreOwner()
	var fetches []LogLine
	for _, l := range startSSH {
		if owner != "" && sshRepoOwner(l.Args) == owner && strings.Contains(l.Args, personalRepo) {
			fetches = append(fetches, l)
		}
	}
	want := owner != "" && w.Shape.Start == StartLoreRepo
	switch {
	case !want && len(fetches) > 0:
		o.fail(fmt.Sprintf("startup fetched the v0.1.17 store although it should not (start %s, owner %q): ssh %s", w.Shape.Start, owner, fetches[0].Args))
		return
	case !want:
		o.trace("startup fetch: skipped as documented (start " + w.Shape.Start + ", owner " + owner + ")")
		return
	case len(fetches) == 0:
		o.fail(fmt.Sprintf("startup did not fetch the v0.1.17 store %s/%s from a lore repo", owner, personalRepo))
		return
	}
	if len(fetches) != 1 {
		o.fail(fmt.Sprintf("the first open of the v0.1.17 store %s/%s made %d ssh connections, want exactly one (one fetch of every ref it needs):\n%s", owner, personalRepo, len(fetches), startCalls(w, startSSH)))
	}
	asOwner := true
	for _, l := range fetches {
		o.Judged++
		if l.Account != owner {
			asOwner = false
		}
	}
	skipped := startLine(output, startSkipped)
	switch {
	case asOwner && skipped != "":
		o.fail(fmt.Sprintf("the startup fetch ran as the store's owner %s but the schema update was skipped: %s", owner, skipped))
	case !asOwner && skipped == "":
		o.fail(fmt.Sprintf("the startup fetch of %s's store ran as %s and nothing told the person", owner, fetches[0].Account))
	case !asOwner && !strings.Contains(skipped, startAccessRefused):
		o.fail(fmt.Sprintf("the startup fetch of %s's store ran as %s (refused) but the line does not name the access problem: %s", owner, fetches[0].Account, skipped))
	}
	for _, raw := range rawSSHText {
		if strings.Contains(output, raw) {
			o.fail(fmt.Sprintf("the server output shows ssh's raw text %q:\n%s", raw, output))
		}
	}
	o.trace(fmt.Sprintf("startup fetch: %d call(s) for %s/%s, as owner %v, line %q", len(fetches), owner, personalRepo, asOwner, skipped))
}

// startCalls lists the startup ssh calls and the git calls logged so far,
// one per line, for a failure that counts round trips.
func startCalls(w *World, startSSH []LogLine) string {
	var b strings.Builder
	for _, l := range startSSH {
		fmt.Fprintf(&b, "  ssh account=%s args=%s\n", l.Account, l.Args)
	}
	gitLines, err := ReadLog(w.Info().Logs.Git)
	if err != nil {
		fmt.Fprintf(&b, "  (git log unreadable: %v)\n", err)
	}
	for _, l := range gitLines {
		fmt.Fprintf(&b, "  git account=%s args=%s\n", l.Account, l.Args)
	}
	return b.String()
}

// startLine is the first output line containing text, "" when none.
func startLine(output, text string) string {
	for l := range strings.SplitSeq(output, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	return ""
}
