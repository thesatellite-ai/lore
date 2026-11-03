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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/projresolve"

	"github.com/spf13/cobra"
)

// Flags of `lore check leaks`.
const (
	flagCheckCI        = "ci"
	flagCheckExplain   = "explain"
	flagCheckStaged    = "staged"
	flagCheckCommitMsg = "commit-msg"
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
	var ci, explain, staged bool
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
names no internal id, store or org.`,
		Example: `  lore check leaks                 # staged files, as the pre-commit hook does
  lore check leaks --commit-msg .git/COMMIT_EDITMSG
  lore check leaks --ci            # in the client's CI
  lore check leaks --explain`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			root, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			if ci {
				return runCheckCI(root)
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
			return runLeakCheck(ctx, root, items, explain, "this commit")
		},
	}
	cmd.Flags().BoolVar(&ci, flagCheckCI, false, "CI mode: ids and structure only, no store access, names nothing internal")
	cmd.Flags().BoolVar(&explain, flagCheckExplain, false, "print the rule, the source and the matched words of each finding")
	cmd.Flags().BoolVar(&staged, flagCheckStaged, false, "check the staged files (default unless --commit-msg is given alone)")
	cmd.Flags().StringVar(&commitMsg, flagCheckCommitMsg, "", "also check this commit message file (the commit-msg hook's $1)")
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
			if err := runLeakCheck(ctx, root, []leakcheck.Item{{Where: wherePRBody, Content: b}}, explain, "this text"); err != nil {
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
// the org's own repo (nothing there is read by a client). Fails closed.
func leakIndexFor(ctx context.Context, root string) (*leakcheck.Index, string, bool, error) {
	s, err := gatherOrgSituation(ctx, root)
	if err != nil {
		return nil, "", false, err
	}
	if s.ResolveErr != nil {
		return nil, "", false, orgResolveCLIError(s.ResolveErr)
	}
	role := s.Result.Role
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
		ir = append(ir, leakcheck.IndexRow{ID: r.ID, Table: r.Table, Label: r.Label, Source: r.Source, Texts: []string{r.Title, r.Body}})
	}
	return leakcheck.NewIndex(ir, projects), s.ProjectID, client, nil
}

// runLeakCheck checks items and prints a block report (E_LEAK_BLOCKED).
func runLeakCheck(ctx context.Context, root string, items []leakcheck.Item, explain bool, what string) error {
	idx, pid, client, err := leakIndexFor(ctx, root)
	if err != nil {
		if !leakcheck.NeedsIndex(items) {
			// Nothing here could match an internal row (no file, too few
			// words, no id): the check is decided without the store.
			fs := leakcheck.Blocking(leakcheck.Check(items, nil, leakcheck.Options{Secrets: true}))
			if len(fs) == 0 {
				return nil
			}
			return leakBlockedError(fs, explain, what)
		}
		return err
	}
	fs := leakcheck.Check(items, idx, leakcheck.Options{Secrets: true, RepoProjectID: pid, ClientRepo: client, DataPrefix: projectDataPrefix(ctx, root)})
	for _, f := range fs {
		if f.Warn {
			fmt.Fprintf(os.Stderr, "lore: warning: %s: %s\n", f.Where, f.Detail)
		}
	}
	block := leakcheck.Blocking(fs)
	if len(block) == 0 {
		return nil
	}
	return leakBlockedError(block, explain, what)
}

// leakBlockedError formats findings as the F16 block message.
func leakBlockedError(fs []leakcheck.Finding, explain bool, what string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "lore: blocked, %s would share internal content with the client repo:\n", what)
	var moves []string
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
		if f.RowID != "" && !seen[f.RowID] && f.Rule != leakcheck.RuleActor {
			seen[f.RowID] = true
			moves = append(moves, f.RowID)
		}
	}
	for _, id := range moves {
		fmt.Fprintf(&b, "Move it:  lore move %s --to <org>\nShare it on purpose: lore share %s --to repo\n", id, id)
	}
	b.WriteString("(a copy-paste check, not a wall: it cannot see paraphrase; see `lore check leaks --explain`)")
	if inCI() {
		return errcodes.New(errcodes.LeakBlocked, fmt.Sprintf("blocked: %d finding(s) of internal content (details hidden in CI)", len(fs)))
	}
	return errcodes.New(errcodes.LeakBlocked, b.String())
}

// stagedItems reads every staged file (added, copied, modified, renamed)
// from the index, not the work tree: what is checked is what is committed.
func stagedItems(ctx context.Context, root string) ([]leakcheck.Item, error) {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil // not a git checkout: nothing is staged
	}
	out, err := exec.CommandContext(ctx, "git", "-C", top, "diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR").Output()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "list staged files").WithCause(err)
	}
	var items []leakcheck.Item
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		path := string(p)
		content, err := exec.CommandContext(ctx, "git", "-C", top, "show", ":"+path).Output()
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

// runCheckCI is F16's CI mode over the checked-out tree's .lore/data.
func runCheckCI(root string) error {
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
	return runLeakCheck(ctx, root, items, false, "this "+where)
}
