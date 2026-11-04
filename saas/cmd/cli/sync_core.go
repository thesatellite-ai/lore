// sync_core.go — the 0.1.18 half of `lore sync`: after the v0.1.17 pass,
// every new-format store attached in .lore/stores.yaml is synced through
// the core (spec 6.1 "lore sync per store"; O12b: there is no separate
// stores noun, `lore sync` is the one sync command).
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// syncReportJSON is the `lore sync --json` data: the v0.1.17 pass report
// with the new-format stores' results added under "stores". The --json
// contract only ever adds fields (OQ5), so every v0.1.17 key keeps its name
// and place; "stores" is left out when no new-format store is attached.
type syncReportJSON struct {
	lsync.Report
	Stores []opsstores.Report `json:"stores,omitempty"`
}

// syncCoreStores syncs every new-format store through the core. A nil core
// (the working directory or home could not be read at start-up, which
// mountCore already reported) has no stores to sync.
func syncCoreStores(ctx context.Context) (opsstores.SyncOut, error) {
	if coreApp == nil {
		return opsstores.SyncOut{}, nil
	}
	syncRepoAddresses(ctx)
	return coreApp.SyncStores(ctx, "")
}

// syncRepoAddresses is the repo identity half of `lore sync` (PROV1,
// PROV2, PROV3; PROVENANCE_DESIGN 10.3): the checkout gets its permanent
// repo id on its first 0.1.18 sync, and every remote of this clone that
// is not a known address of the repo is recorded `unconfirmed`. Sync only
// records and prints; it never decides. Everything goes to stderr, so
// `lore sync --json` keeps a clean stdout, and a problem here never fails
// the sync (the rows are unaffected: they carry the repo id, not a URL).
func syncRepoAddresses(ctx context.Context) {
	rep, err := coreApp.CheckRepoAddresses(ctx)
	if err != nil {
		if !errs.Has(err, errs.NotFound) { // no `lore init` yet: nothing to identify
			fmt.Fprintln(os.Stderr, style.Warn("warning: ")+"repo addresses not checked: "+err.Error())
		}
		return
	}
	printURLWarnings(rep.Warnings)
	for _, n := range rep.Notices {
		fmt.Fprint(os.Stderr, addressNotice(rep.RepoID, n))
		// Q2: lore asks once: only on the run that first records the
		// address, and only when a person can answer (never an agent or CI,
		// whose stdin is not a terminal: they get the notice above and may
		// run one of its commands themselves).
		if askAddress(n, stdinIsTTY()) {
			answerAddress(ctx, n)
		}
	}
}

// printURLWarnings prints the credential warning of each remote that held
// one (URL CREDENTIALS FIX): the stored clean form only, never the token,
// a part of it or its length.
func printURLWarnings(ws []opsinfra.URLWarning) {
	for _, w := range ws {
		fmt.Fprintln(os.Stderr, style.Warn("warning: ")+"a credential was in your git remote ("+w.Remote+"); lore stored the URL without it: "+w.Stored)
		fmt.Fprintln(os.Stderr, "         remove it from git config too: git remote set-url "+w.Remote+" "+w.Stored)
	}
}

// addressNotice is the notice of one unconfirmed address (10.3), with the
// commands that confirm it. PROV3: a case-only difference is the same
// repo, so its notice never offers `lore repo fork`.
func addressNotice(repoID string, n opsinfra.AddressNotice) string {
	var b strings.Builder
	if n.CaseOnly {
		b.WriteString("! remote changed: this clone uses " + n.Key + ", the repo record says " + n.Recorded + " (same repo, different case)\n")
		b.WriteString("  confirm it once with: lore repo set-url " + n.Key + "\n")
		return b.String()
	}
	recorded := n.Recorded
	if recorded == "" {
		recorded = "no confirmed address"
	}
	b.WriteString("! remote changed: this clone uses " + n.Key + ", the repo record says " + recorded + "\n")
	b.WriteString("  rows are not affected (they use repo id " + repoID + ")\n")
	b.WriteString("  store links still use the confirmed address until you confirm this one:\n")
	cmds := [][2]string{
		{"lore repo set-url " + n.Key, "the repo moved or was renamed"},
		{"lore repo set-url " + n.Key + " --mirror", "another address of the same repo"},
		{"lore repo fork", "this clone is a fork: give it its own repo id"},
	}
	width := 0
	for _, c := range cmds {
		width = max(width, len(c[0]))
	}
	for _, c := range cmds {
		b.WriteString("    " + c[0] + strings.Repeat(" ", width-len(c[0])+2) + c[1] + "\n")
	}
	return b.String()
}

// askAddress is Q2's rule: ask only on the run that first recorded the
// address, and only in a terminal.
func askAddress(n opsinfra.AddressNotice, terminal bool) bool { return n.New && terminal }

// The answers of the ask-once question (closed set).
const (
	answerMoved  = "1"
	answerMirror = "2"
	answerFork   = "3"
)

// answerAddress asks the question once and runs the chosen command
// through the core (so its audit fields record who answered). "later", an
// empty line or anything else leaves the address unconfirmed.
func answerAddress(ctx context.Context, n opsinfra.AddressNotice) {
	q := "? what is " + n.Key + "?  1) same repo, renamed or moved  2) another address of the same repo  "
	if !n.CaseOnly {
		q += "3) a fork  "
	}
	q += "[later]: "
	fmt.Fprint(os.Stderr, q)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	in, name := "", ""
	switch strings.TrimSpace(line) {
	case answerMoved:
		in, name = `{"url":`+strconv.Quote(n.URL)+`}`, opsinfra.OpRepoSetURL
	case answerMirror:
		in, name = `{"url":`+strconv.Quote(n.URL)+`,"mirror":true}`, opsinfra.OpRepoSetURL
	case answerFork:
		if n.CaseOnly {
			return
		}
		in, name = `{}`, opsinfra.OpRepoFork
	default:
		return
	}
	e, ok := coreApp.Registry().Lookup(name)
	if !ok {
		return
	}
	res, err := coreApp.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{Interactive: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+err.Error())
		return
	}
	// Interface() is the reflect boundary of an op result; narrowed at once.
	if v := res.Value(); v.IsValid() && v.CanInterface() {
		if t, ok := v.Interface().(op.Texter); ok {
			fmt.Fprint(os.Stderr, t.Text())
		}
	}
	if name == opsinfra.OpRepoSetURL {
		followStoreLinks(ctx)
	}
}

// printCoreSyncReport prints one line per new-format store and one indented
// line per problem, in the style of printSyncReport.
func printCoreSyncReport(out opsstores.SyncOut) {
	for _, r := range out.Stores {
		line := "store " + r.Store + ": sent " + strconv.Itoa(r.Sent) +
			", received " + strconv.Itoa(r.Received) +
			", merged " + strconv.Itoa(r.Merged) +
			", refused " + strconv.Itoa(r.Refused)
		if r.Offline {
			line += style.Muted(" (offline: local changes kept, sent on the next sync)")
		}
		fmt.Println(line)
		for _, p := range r.Problems {
			fmt.Println("  " + p)
		}
	}
}

// coreSyncRefusal is the error `lore sync` returns when a new-format store
// refused rows: E_STALE (exit 23), printed here because main treats every
// core error as already printed (coreExitCode). Nothing was lost: the
// refused rows stay local until resolved (spec 10.4).
func coreSyncRefusal(out opsstores.SyncOut) error {
	refused := 0
	for _, r := range out.Stores {
		refused += r.Refused
	}
	if refused == 0 {
		return nil
	}
	e := errs.New(errs.Stale, strconv.Itoa(refused)+" row(s) changed on both sides and could not be merged automatically").
		WithHint("the rows are listed above; edit each to the text to keep, then run lore sync again")
	fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+e.Error())
	fmt.Fprintln(os.Stderr, hintPrefix+e.Hint)
	return e
}
