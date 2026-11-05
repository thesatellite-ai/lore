package wire

// repoaddr_notice.go — the repo identity half of `lore sync` on a host
// that has no v0.1.17 pass (PROV1, PROV2, PROV3; PROVENANCE_DESIGN 10.3,
// "lore asks once"), ported from the old binary's sync_core.go: the
// checkout gets its permanent repo id on its first 0.1.18 sync, every
// remote of this clone that is not a known address is recorded
// `unconfirmed` with a notice, and only on the run that first records it,
// in a terminal, lore asks once what the address is. Sync never decides;
// everything goes to w (stderr), so `lore sync --json` keeps a clean
// stdout, and a problem here never fails the sync.
//
//	CheckRepoAddresses ──▶ credential warnings ──▶ per notice: the text
//	   └─ New && terminal ──▶ ask (1 moved / 2 mirror / 3 fork / later)
//	        └─ repo.set_url / repo.fork through the app's Run (audited)
//
// Not ported: the old binary's follow-up that re-points org store links
// after a set-url (project_repo.go followStoreLinks, team config).

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// The answers of the ask-once question (closed set).
const (
	answerMoved  = "1"
	answerMirror = "2"
	answerFork   = "3"
)

// SyncRepoAddresses runs the address pass and writes its notices to w;
// with terminal (a person at stdin), a newly recorded address is asked
// about once, reading the answer from in.
func (a *App) SyncRepoAddresses(ctx context.Context, w io.Writer, in io.Reader, terminal bool) {
	rep, err := a.CheckRepoAddresses(ctx)
	if err != nil {
		if !errs.Has(err, errs.NotFound) { // no `lore init` yet: nothing to identify
			_, _ = fmt.Fprintln(w, "warning: repo addresses not checked: "+err.Error()) // stderr gone: nothing left to report to
		}
		return
	}
	for _, uw := range rep.Warnings {
		// URL CREDENTIALS FIX: the stored clean form only, never the token.
		_, _ = fmt.Fprintln(w, "warning: a credential was in your git remote ("+uw.Remote+"); lore stored the URL without it: "+uw.Stored)
		_, _ = fmt.Fprintln(w, "         remove it from git config too: git remote set-url "+uw.Remote+" "+uw.Stored)
	}
	reader := bufio.NewReader(in)
	for _, n := range rep.Notices {
		_, _ = io.WriteString(w, AddressNotice(rep.RepoID, n))
		if AskAddress(n, terminal) {
			a.answerAddress(ctx, w, reader, n)
		}
	}
}

// AddressNotice is the notice of one unconfirmed address (10.3) with the
// commands that confirm it. PROV3: a case-only difference is the same
// repo, so its notice never offers `lore repo fork`.
func AddressNotice(repoID string, n opsinfra.AddressNotice) string {
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

// AskAddress is Q2's rule: ask only on the run that first recorded the
// address, and only when a person can answer (never an agent or CI, whose
// stdin is not a terminal: they get the notice and may run a command).
func AskAddress(n opsinfra.AddressNotice, terminal bool) bool { return n.New && terminal }

// answerAddress asks once and runs the chosen command through the app
// (so its audit fields record who answered). "later", an empty line or
// anything else leaves the address unconfirmed.
func (a *App) answerAddress(ctx context.Context, w io.Writer, in *bufio.Reader, n opsinfra.AddressNotice) {
	q := "? what is " + n.Key + "?  1) same repo, renamed or moved  2) another address of the same repo  "
	if !n.CaseOnly {
		q += "3) a fork  "
	}
	_, _ = io.WriteString(w, q+"[later]: ")
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return
	}
	body, name := "", ""
	switch strings.TrimSpace(line) {
	case answerMoved:
		body, name = `{"url":`+strconv.Quote(n.URL)+`}`, opsinfra.OpRepoSetURL
	case answerMirror:
		body, name = `{"url":`+strconv.Quote(n.URL)+`,"mirror":true}`, opsinfra.OpRepoSetURL
	case answerFork:
		if n.CaseOnly {
			return
		}
		body, name = `{}`, opsinfra.OpRepoFork
	default:
		return
	}
	e, ok := a.Registry().Lookup(name)
	if !ok {
		return
	}
	res, err := a.Run(ctx, e, op.JSONDecoder{Body: []byte(body)}, op.Caller{Interactive: true})
	if err != nil {
		_, _ = fmt.Fprintln(w, "ERROR: "+err.Error())
		return
	}
	if t, ok := res.Text(); ok {
		_, _ = io.WriteString(w, t)
	}
}
