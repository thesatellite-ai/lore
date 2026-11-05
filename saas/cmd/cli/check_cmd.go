// check_cmd.go — `lore check leaks` and `lore check pr-body` (F16, §6.3):
// the copy-paste catch between internal or personal content and anything a
// client can read. The pre-commit and commit-msg hooks run `lore check
// leaks --staged` / `--commit-msg <file>`; agents run `lore check pr-body`
// before opening a pull request or posting a comment; the client's CI runs
// `lore check leaks --ci`, which needs no store and names nothing internal.
//
// What this cannot stop is listed in LORE_TEAM_SPEC §6.6: --no-verify, the
// web UI, paraphrase, a teammate without lore.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/projresolve"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// Flags of `lore check leaks`.
const (
	flagCheckCI        = "ci"
	flagCheckExplain   = "explain"
	flagCheckStaged    = "staged"
	flagCheckCommitMsg = "commit-msg"
	flagCheckAsClient  = "as-client"
)

// Places a finding can be in.
const (
	whereCommitMessage = "commit message"
	wherePRBody        = "PR body"
	stdinArg           = "-"
)

func newCheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Leak checks: internal or personal content must never reach the client",
	}
	cmd.AddCommand(newCheckLeaksCommand(), newCheckPRBodyCommand())
	return cmd
}

func newCheckLeaksCommand() *cobra.Command {
	var ci, explain, staged, asClient bool
	var commitMsg string
	cmd := &cobra.Command{
		Use:   "leaks",
		Short: "Check staged files and the commit message (hooks), or the tree structure (--ci)",
		Long: `Without flags, checks every staged file (not only .lore/) against the internal
and personal rows loaded for this repo: a row whose id belongs elsewhere, a
pointer to an internal row, 8 or more consecutive words of an internal note,
or a secret. It fails closed when it cannot read the store.

--ci needs no store access: it checks only ids and structure of .lore/data
(a row of another project, a reference field pointing outside) and its output
names no internal id, store or org.

--as-client checks this repo as the client would see it, before handing an
own repo over (RES 3.9): every tracked file against the internal rows,
resources with internal, secret_ref or sensitive values, refs and kinds that
name store rows, and those values in git history. It refuses until each is
cleared; it never prints a value.`,
		Example: `  lore check leaks                 # staged files, as the pre-commit hook does
  lore check leaks --commit-msg .git/COMMIT_EDITMSG
  lore check leaks --ci            # in the client's CI
  lore check leaks --as-client     # before handing an own repo to the client
  lore check leaks --explain`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			root, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			if ci {
				return runCheckCI(ctx, root)
			}
			if asClient {
				return runCheckAsClient(ctx, root, explain)
			}
			var items []leakcheck.Item
			if commitMsg != "" {
				it, err := commitMessageItem(ctx, root, commitMsg)
				if err != nil {
					return err
				}
				items = append(items, it)
			}
			if staged || commitMsg == "" {
				st, err := stagedItems(ctx, root)
				if err != nil {
					return err
				}
				items = append(items, st...)
			}
			return runLeakCheck(ctx, root, items, explain, leakContext{action: leakActionCommit, what: "this commit"})
		},
	}
	cmd.Flags().BoolVar(&ci, flagCheckCI, false, "CI mode: ids and structure only, no store access, names nothing internal")
	cmd.Flags().BoolVar(&explain, flagCheckExplain, false, "print the rule, the source and the matched words of each finding")
	cmd.Flags().BoolVar(&staged, flagCheckStaged, false, "check the staged files (default unless --commit-msg is given alone)")
	cmd.Flags().StringVar(&commitMsg, flagCheckCommitMsg, "", "also check this commit message file (the commit-msg hook's $1)")
	cmd.Flags().BoolVar(&asClient, flagCheckAsClient, false, "check the whole repo as the client would see it (before a hand-over)")
	cmd.MarkFlagsMutuallyExclusive(flagCheckCI, flagCheckAsClient)
	return cmd
}

func newCheckPRBodyCommand() *cobra.Command {
	var explain bool
	cmd := &cobra.Command{
		Use:   "pr-body <file|->",
		Short: "Check a pull request body or comment before posting it (agents: always)",
		Example: `  lore check pr-body body.md
  gh pr view 12 --json body -q .body | lore check pr-body -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			var b []byte
			if args[0] == stdinArg {
				b, err = io.ReadAll(os.Stdin)
			} else {
				b, err = os.ReadFile(args[0])
			}
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "read the body").WithCause(err)
			}
			if err := runLeakCheck(ctx, root, []leakcheck.Item{{Where: wherePRBody, Content: b}}, explain, leakContext{action: leakActionText, what: "this text"}); err != nil {
				return err
			}
			fmt.Println("✓ nothing internal found (a copy-paste check: a paraphrase passes it)")
			return nil
		},
	}
	cmd.Flags().BoolVar(&explain, flagCheckExplain, false, "print the rule, the source and the matched words of each finding")
	return cmd
}

// leakIndexFor builds the index for the project at root: every external
// row for a client repo; personal rows only for a repo with no org; nil for
// the org's own repo (nothing there is read by a client). External rows are
// the v0.1.17 cache's plus the core's attached stores (coreLeakRows).
// Fails closed.
func leakIndexFor(ctx context.Context, root string) (*leakcheck.Index, string, bool, error) {
	return leakIndexWith(ctx, root, false)
}

// leakIndexWith is leakIndexFor; asClient reads an own repo as a client
// repo (`--as-client`, RES 3.9): every internal row is indexed.
func leakIndexWith(ctx context.Context, root string, asClient bool) (*leakcheck.Index, string, bool, error) {
	s, err := gatherOrgSituation(ctx, root)
	if err != nil {
		return nil, "", false, err
	}
	if s.ResolveErr != nil {
		return nil, "", false, orgResolveCLIError(s.ResolveErr)
	}
	role := s.Result.Role
	if asClient {
		role = orgresolve.RoleClient
	}
	client := role == orgresolve.RoleClient
	if role == orgresolve.RoleOwn && !s.Result.LinkUnknown {
		return nil, s.ProjectID, false, nil
	}
	if s.ProjectID == "" {
		return nil, "", false, nil
	}
	db, _, err := openExternalCache(ctx, s, cacheForCheck)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = db.Close() }() // read-only use
	rows, err := db.Rows(ctx, cachedb.Query{})
	if err != nil {
		return nil, "", false, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).WithCause(err)
	}
	var ir []leakcheck.IndexRow
	projects := map[string]string{}
	for _, r := range rows {
		if role != orgresolve.RoleClient && !strings.HasPrefix(r.Source, cachedb.SourcePersonal+":") {
			continue
		}
		if r.Table == tableProjects {
			projects[r.ID] = r.Label
			continue
		}
		ir = append(ir, leakcheck.IndexRow{ID: r.ID, Table: r.Table, Label: r.Label, Source: r.Source, Texts: []string{r.Title, r.Body}, SharedAs: sharedCopies(r.Doc)})
	}
	// The 0.1.18 core's stores: their rows never reach the v0.1.17 cache.
	coreRows, err := coreLeakRows(ctx, client)
	if err != nil {
		return nil, "", false, err
	}
	ir = append(ir, coreRows...)
	return leakcheck.NewIndex(ir, projects), s.ProjectID, client, nil
}

// sharedCopies reads an internal row's shareProvenanceField: the ids of
// the copies `lore share` made of it, whose row files may carry its text.
// A document without the field (or unreadable) has none: the safe answer.
func sharedCopies(doc []byte) []string {
	var d struct {
		SharedAs []string `json:"shared_as"`
	}
	if json.Unmarshal(doc, &d) != nil {
		return nil
	}
	return d.SharedAs
}

// runLeakCheck checks items and prints a block report (E_LEAK_BLOCKED).
//
// Secrets go through the one core scanner with the committed allow entries
// (D4, secret_scanner.go); a finding always refuses (D3: no switch turns
// the scan off). A scanner that cannot be built refuses too.
func runLeakCheck(ctx context.Context, root string, items []leakcheck.Item, explain bool, lc leakContext) error {
	warnRemovedSyncAllow()
	secrets, err := checkSecretScan(ctx, root)
	if err != nil {
		return err
	}
	idx, pid, client, err := leakIndexFor(ctx, root)
	if err != nil {
		if !leakcheck.NeedsIndex(items) {
			// Nothing here could match an internal row (no file, too few
			// words, no id): the check is decided without the store.
			fs := leakcheck.Blocking(leakcheck.Check(items, nil, leakcheck.Options{Secrets: true, SecretScan: secrets, SessionIDs: knownSessionIDs(ctx)}))
			if len(fs) == 0 {
				return nil
			}
			return leakBlockedError(fs, explain, lc)
		}
		return err
	}
	fs := leakcheck.Check(items, idx, leakcheck.Options{Secrets: true, SecretScan: secrets, RepoProjectID: pid, ClientRepo: client, DataPrefix: projectDataPrefix(ctx, root), SessionIDs: knownSessionIDs(ctx)})
	for _, f := range fs {
		if f.Warn {
			fmt.Fprintf(os.Stderr, "lore: warning: %s: %s\n", f.Where, f.Detail)
		}
	}
	block := leakcheck.Blocking(fs)
	if len(block) == 0 {
		return nil
	}
	return leakBlockedError(block, explain, lc)
}

// leakBlockedError formats findings as the F16 block message. Each
// finding gets a fix that applies to what lc says was refused (F10): a row
// file in this repo is named by ITS id (move it to the org or delete it),
// never by the internal note it matched, which is internal already.
func leakBlockedError(fs []leakcheck.Finding, explain bool, lc leakContext) error {
	var b strings.Builder
	fmt.Fprintf(&b, "lore: blocked, %s would share internal content with the client repo:\n", lc.what)
	var fixes []string
	seen := map[string]bool{}
	for _, f := range fs {
		where := f.Where
		if f.Line > 0 && where != whereCommitMessage && where != wherePRBody {
			where = fmt.Sprintf("%s:%d", where, f.Line)
		}
		detail := f.Detail
		if !explain && f.Rule == leakcheck.RuleText {
			detail = fmt.Sprintf("matches internal note %s (%d words)", f.RowID, f.Words)
		}
		if explain {
			detail = fmt.Sprintf("[%s rule] %s %s", f.Rule, detail, f.Label)
		}
		fmt.Fprintf(&b, "  %-40s %s\n", where, detail)
		if fix := leakFix(f, lc); fix != "" && !seen[fix] {
			seen[fix] = true
			fixes = append(fixes, fix)
		}
	}
	for _, fix := range fixes {
		b.WriteString(fix + "\n")
	}
	if lc.action != leakActionShare && lc.action != leakActionMove {
		// BATCH 2: share is removed; a move hands the row over, same id.
		b.WriteString("To give the client an internal note on purpose: lore move <id> --to repo (it keeps the id; it shows the text and asks)\n")
	}
	b.WriteString("(a copy-paste check, not a wall: it cannot see paraphrase; see `lore check leaks --explain`)")
	if inCI() {
		return errcodes.New(errcodes.LeakBlocked, fmt.Sprintf("blocked: %d finding(s) of internal content (details hidden in CI)", len(fs)))
	}
	return errcodes.New(errcodes.LeakBlocked, b.String())
}

// leakAction is the operation a leak check refused. The fix the block
// message offers depends on it: a staged file is edited, a pushed commit
// must be rewritten, a refused write never happened.
type leakAction int

// Leak actions.
const (
	// leakActionCommit: staged files and the commit message (pre-commit,
	// commit-msg, `lore check leaks`).
	leakActionCommit leakAction = iota
	// leakActionPush: the commits a `git push` would send (pre-push).
	leakActionPush
	// leakActionText: a pull request body or comment (`lore check pr-body`).
	leakActionText
	// leakActionWrite: a row about to be written to this repo; nothing was
	// written.
	leakActionWrite
	// leakActionShare: `lore share … --to repo`; the copy carries text of
	// an internal row other than the one shared.
	leakActionShare
	// leakActionLive: a live write or landing commit to this repo's refs.
	leakActionLive
	// leakActionMove: `lore move <id> --to repo` in a client repo; the row
	// quotes text of an internal row other than itself. Nothing written.
	leakActionMove
)

// leakContext says what a leak check guards, for its block message.
type leakContext struct {
	action leakAction
	// what names the refused thing in the first line ("this commit").
	what string
	// source is the row being shared or moved (leakActionShare,
	// leakActionMove).
	source string
}

// secretAllowFix is the 19.4 fix of a secret finding in a repo row field:
// the vault, or an allow entry committed with the row ("" for any other
// finding). The hooks honour only committed entries, so the sal_ file must
// be in the same commit.
func secretAllowFix(f leakcheck.Finding) string {
	id, _, ok := repoRowOf(f.Path)
	if f.Rule != leakcheck.RuleSecret || f.Secret.Field == "" || !ok {
		return ""
	}
	return fmt.Sprintf("Store the value in your vault and write ${secret:…} in %s. If it is not a secret: lore secrets allow %s --rule %s --field %s --match-hash %d --reason \"<why>\", add the new .lore/data/secret_allows file, then commit again",
		id, id, f.Secret.Rule, f.Secret.Field, f.Secret.Hash)
}

// leakFix is the one-line fix for a finding, or "" when the finding needs
// none beyond the listing (the same fix is printed once).
func leakFix(f leakcheck.Finding, lc leakContext) string {
	if f.Rule == leakcheck.RuleActor {
		return ""
	}
	if fix := secretAllowFix(f); fix != "" {
		return fix
	}
	switch lc.action {
	case leakActionShare:
		if f.RowID == "" || f.RowID == lc.source {
			return "Not shared: the copy breaks a leak rule that sharing does not lift; nothing written"
		}
		return fmt.Sprintf("Not shared: %s also carries text of internal note %s, which stays internal. Remove that part from %s, then share it again; nothing written", lc.source, f.RowID, lc.source)
	case leakActionMove:
		if f.RowID == "" || f.RowID == lc.source {
			return "Not moved: the row breaks a leak rule that a move does not lift; nothing written"
		}
		return fmt.Sprintf("Not moved: %s also carries text of internal note %s, which stays internal. Remove that part from %s, then move it again; nothing written", lc.source, f.RowID, lc.source)
	case leakActionWrite:
		return "Nothing written. Keep it internal: write it with --to <org> instead of --to repo, or leave out the internal text"
	case leakActionText:
		return "Remove the internal text before posting"
	case leakActionCommit, leakActionPush, leakActionLive:
		// advice depends on the path: below
	}
	if id, table, ok := repoRowOf(f.Path); ok {
		kind := singularKind(table)
		switch lc.action {
		case leakActionPush:
			return fmt.Sprintf("%s (a %s in this repo) is in a commit you are pushing: rewrite that commit without it (git commit --amend, or git rebase), then keep it internal: lore move %s --to <org>", id, kind, id)
		case leakActionLive:
			return fmt.Sprintf("Keep %s internal: lore move %s --to <org>, or edit it so it no longer carries the internal text", id, id)
		case leakActionCommit, leakActionText, leakActionWrite, leakActionShare, leakActionMove:
			// the general repo-row advice below
		}
		return fmt.Sprintf("%s is a %s in this repo. Keep it internal: lore move %s --to <org>   or remove it: lore %s delete %s --confirm", id, kind, id, kind, id)
	}
	where := f.Path
	if where == "" {
		where = "the commit message"
	}
	if lc.action == leakActionPush {
		return "Remove the internal text from " + where + " and rewrite the commit(s) that carry it (git commit --amend, or git rebase) before pushing"
	}
	return "Remove the internal text from " + where
}

// repoRowPathRe matches a row file of this repo's .lore/data (any project
// folder of a monorepo): .lore/data/<table>/<id>.json.
var repoRowPathRe = regexp.MustCompile(`(?:^|/)\.lore/data/([a-z_]+)/([a-z]{3}_[0-9a-f]{32})\.json$`)

// repoRowOf returns the row id and table of a repo row file path.
func repoRowOf(path string) (id, table string, ok bool) {
	m := repoRowPathRe.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	return m[2], m[1], true
}

// stagedItems reads every staged file (added, copied, modified, renamed)
// from the index, not the work tree: what is checked is what is committed.
func stagedItems(ctx context.Context, root string) ([]leakcheck.Item, error) {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil // not a git checkout: nothing is staged
	}
	out, err := newProgramCmd(ctx, "git", "-C", top, "diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR").Output()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "list staged files").WithCause(err)
	}
	var items []leakcheck.Item
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		path := string(p)
		content, err := newProgramCmd(ctx, "git", "-C", top, "show", ":"+path).Output()
		if err != nil {
			continue // a submodule or an unreadable blob: nothing to read
		}
		items = append(items, leakcheck.Item{Where: path, Path: path, Content: content})
	}
	return items, nil
}

// projectDataPrefix is the project's ".lore/data/" relative to the repo top.
func projectDataPrefix(ctx context.Context, root string) string {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	rel := projectRelPath(top, root)
	if isRepoRoot(rel) {
		return ".lore/data/"
	}
	return rel + "/.lore/data/"
}

// commitMsgPath resolves the commit-msg hook's $1: git passes it relative
// to the repository's top level, while a monorepo project's hook block runs
// from the project folder.
func commitMsgPath(ctx context.Context, root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if _, err := os.Stat(filepath.Join(root, p)); err == nil {
		return filepath.Join(root, p)
	}
	if top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel"); err == nil {
		return filepath.Join(top, p)
	}
	return p
}

// commitMessageItem reads the commit-msg hook's file as one leak-check
// item. Only git's "#" comment lines are dropped: every other line —
// trailers included, lore's own Lore-Task trailer too (coordinator Q13:
// never exempt it) — is checked by the id, text and secret rules.
func commitMessageItem(ctx context.Context, root, path string) (leakcheck.Item, error) {
	b, err := os.ReadFile(commitMsgPath(ctx, root, path))
	if err != nil {
		return leakcheck.Item{}, errcodes.New(errcodes.InvalidInput, "read the commit message").WithCause(err)
	}
	return leakcheck.Item{Where: whereCommitMessage, Content: stripGitComments(b)}, nil
}

// stripGitComments drops git's "#" comment lines from a commit message.
func stripGitComments(b []byte) []byte {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

// keyLooseningError turns wire.KeyLoosenings findings into one
// E_LEAK_BLOCKED (METADATA 3.8 RX7, OV64): every meta_keys row that drops
// internal, sensitive or strict without a new changed_by record, each
// with the meta edit command that records it. nil when none.
func keyLooseningError(found []error) error {
	if len(found) == 0 {
		return nil
	}
	lines := make([]string, 0, len(found))
	hints := make([]string, 0, len(found))
	for _, f := range found {
		var e *errs.Error
		if errors.As(f, &e) {
			lines = append(lines, e.Message)
			hints = append(hints, e.Hint)
			continue
		}
		lines = append(lines, f.Error())
	}
	return errcodes.New(errcodes.LeakBlocked, strings.Join(lines, "; ")).WithHint(strings.Join(hints, "; "))
}

// checkKeyLoosening runs the loosening check over what HEAD added since
// base ("" = the default remote branch, else the previous commit): the
// `check --ci` and ci-merge part of RX7. A directory that is not a git
// checkout has nothing to compare.
func checkKeyLoosening(ctx context.Context, root, base string) error {
	found, err := wire.KeyLooseningsSince(ctx, root, base)
	if err != nil {
		return nil // not a git checkout, or no common history: nothing to compare
	}
	return keyLooseningError(found)
}

// runCheckCI is F16's CI mode over the checked-out tree's .lore/data.
func runCheckCI(ctx context.Context, root string) error {
	if err := checkKeyLoosening(ctx, root, ""); err != nil {
		return err
	}
	dataDir := filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName)
	meta, err := lsync.ReadProjectMeta(dataDir)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, "no .lore/data/_meta.json here; run from the project root")
	}
	files, err := readDirFiles(dataDir)
	if err != nil {
		return err
	}
	tree := make(map[string][]byte, len(files))
	for rel, b := range files {
		tree[".lore/data/"+rel] = b
	}
	fs := leakcheck.CheckCI(tree, meta.ProjectID)
	if len(fs) == 0 {
		fmt.Println("✓ lore check leaks --ci: every row in .lore/data belongs to this project")
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "lore check leaks --ci: %d row file(s) must not be in this repo:\n", len(fs))
	for _, f := range fs {
		fmt.Fprintf(&b, "  %s: %s\n", f.Where, f.Detail)
	}
	b.WriteString("ask the author to remove them (and run `lore check leaks` locally, which names them)")
	return errcodes.New(errcodes.LeakBlocked, b.String())
}

// storeWriteKey marks a context whose live writes and landings target a
// store (set by the store mirror around its live session).
type storeWriteKey struct{}

// withStoreWrite marks ctx as a store write.
func withStoreWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, storeWriteKey{}, true)
}

// storeWrite reports a context marked by withStoreWrite.
func storeWrite(ctx context.Context) bool {
	v, _ := ctx.Value(storeWriteKey{}).(bool) // absent key reads false: not a store write
	return v
}

// leakCheckDocs is the F16 check for writes that never pass a git hook: a
// write to this (client) repo's live ref and a landing commit (lore-live
// calls it before pushing). docs maps a display path to the row document.
func leakCheckDocs(ctx context.Context, where string, docs map[string][]byte) error {
	if storeWrite(ctx) {
		// A store live write or landing goes to the org's own store, which
		// the client cannot read: nothing to check (F16 guards client refs).
		return nil
	}
	root, err := os.Getwd()
	if err != nil {
		return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	items := make([]leakcheck.Item, 0, len(docs))
	for p, b := range docs {
		items = append(items, leakcheck.Item{Where: where + " " + p, Path: p, Content: b})
	}
	return runLeakCheck(ctx, root, items, false, leakContext{action: leakActionLive, what: "this " + where})
}

// leakCheckShare is the F16 check of a `lore share … --to repo` copy before
// it is written. Only the text of the shared row itself is allowed in the
// copy (§6.4: sharing is the deliberate exception); every other internal
// row, the id and reference rules and the secret rule still block.
func leakCheckShare(ctx context.Context, sourceID, path string, body []byte) error {
	root, err := os.Getwd()
	if err != nil {
		return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	items := []leakcheck.Item{{Where: "share " + path, Path: path, Content: body, AllowRows: []string{sourceID}}}
	return runLeakCheck(ctx, root, items, false, leakContext{action: leakActionShare, what: "this share", source: sourceID})
}
