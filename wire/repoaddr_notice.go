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
//	        └─ repo.set_url / repo.fork through the app's Run (audited),
//	           store links left alone (opsinfra.WithoutLinkFollow)
//	             └─ 1 moved only ──▶ followMovedLinks: plan the org store
//	                links that lack the new address, show them, ask
//	                (--yes / REST yes answers; no answer: the command)
//
// v0.1.17's followStoreLinks (project_repo.go) re-pointed the org store
// links after every set-url without asking; here only a move does, and it
// asks first because the links are written into org stores the whole org
// reads (INTEG-CFG7-1). A mirror or fork answer changes no link.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
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
//
// A client repo is skipped (B-8, B-11): minting its id writes the tracked
// _meta.json (with the client's remote) and a repos row file into the
// client's checkout, which a sync must never do there.
//
// I-7: in an own repo whose .lore/data matches the commit, the pass only
// reads (RepoAddressStatus): minting the id or recording a new address
// writes _meta.json, a repos row and the person's actor row, and every
// clone of a repo without an id writes the same ones; once one teammate
// commits them, every other clone's `git pull` aborts over its own copies.
// The writes wait for a sync that finds rows waiting for a commit, which
// then carries them.
func (a *App) SyncRepoAddresses(ctx context.Context, w io.Writer, in io.Reader, terminal bool) {
	if client, cerr := a.ClientRepo(ctx); cerr == nil && client {
		return
	}
	check := a.CheckRepoAddresses
	if pending, ok := a.uncommittedData(ctx); ok && len(pending) == 0 {
		check = a.RepoAddressStatus
	}
	rep, err := check(ctx)
	if err != nil {
		switch {
		case errs.Has(err, errs.NotFound): // no `lore init` yet: nothing to identify
		case errs.Has(err, errs.Stale):
			// G-18: printed as "[E_STALE] store: row changed since it was
			// read" with no cause or step; the repo's row was changed by a
			// commit this clone has not merged (a teammate's lore repo set-url)
			_, _ = fmt.Fprintln(w, "warning: repo addresses not checked: this repo's lore row changed in a commit this clone has not merged yet (a teammate may have confirmed a new address); run `git pull`, then `lore sync` again (if the remote moved: lore repo set-url --url <new-url>)")
		default:
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
		b.WriteString("  confirm it once with: lore repo set-url --url " + n.Key + "\n")
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
		{"lore repo set-url --url " + n.Key, "the repo moved or was renamed"},
		{"lore repo set-url --url " + n.Key + " --mirror", "another address of the same repo"},
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
	// The store links are this pass's to follow (a move, after asking),
	// never set-url's own unasked follow-up.
	res, err := a.Run(opsinfra.WithoutLinkFollow(ctx), e, op.JSONDecoder{Body: []byte(body)}, op.Caller{Interactive: true})
	if err != nil {
		_, _ = fmt.Fprintln(w, "ERROR: "+err.Error())
		return
	}
	if t, ok := res.Text(); ok {
		_, _ = io.WriteString(w, t)
	}
	if strings.TrimSpace(line) != answerMoved {
		return
	}
	b, err := res.JSON()
	if err != nil {
		return
	}
	var out opsinfra.RepoSetURLOut
	if json.Unmarshal(b, &out) != nil {
		return
	}
	pid, err := a.Project(ctx)
	if err != nil {
		return
	}
	a.followMovedLinks(ctx, w, in, true, engine.Confirmed(ctx), pid.String(), out.Key, out.Confirmed)
}

// followMovedLinks is the link half of a move (issue #23): every org
// store folder linking this project under a confirmed address gains a
// link for each confirmed address it lacks (the new one, after a move).
// It writes into org stores, so it lists the changes and asks first
// (INTEG-CFG7-1): yes (the caller's, engine.Confirmed: REST `yes`) or a
// "y" on the terminal writes them, naming each folder updated; anything
// else changes nothing and names the command that writes them later.
func (a *App) followMovedLinks(ctx context.Context, w io.Writer, in *bufio.Reader, terminal, yes bool, projectID, key string, confirmed []string) {
	plan := a.planRepoLinks(ctx, projectID, confirmed)
	for _, l := range plan.Warnings() {
		_, _ = fmt.Fprintln(w, l)
	}
	n := plan.Adds()
	if n == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "org store links of this repo that lack %s (the org reads them):\n", key)
	for _, l := range plan.Preview() {
		_, _ = fmt.Fprintln(w, "  "+l)
	}
	if !yes && terminal {
		_, _ = fmt.Fprintf(w, "? write %s into the org stores? [y/N]: ", plural(n, "link"))
		ans, _ := in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case answerYes, answerYesLong:
			yes = true
		}
	}
	if !yes {
		_, _ = fmt.Fprintln(w, "store links not changed; to write them: lore repo set-url --url "+key)
		return
	}
	for _, l := range a.applyRepoLinks(ctx, plan) {
		_, _ = fmt.Fprintln(w, l)
	}
}

// The yes answers of the link question (closed set; anything else is no).
const (
	answerYes     = "y"
	answerYesLong = "yes"
)

// plural is "1 link" / "2 links".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
