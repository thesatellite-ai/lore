// link_cmds.go — `lore link add/list/remove` + `lore commit-show`
//
// Polymorphic git-commit linker. Any entity (task, run, mission, decision,
// memory, …) can have one or more commits linked to it via the commit_link
// table. Lets users / agents anchor "this work shipped as <sha>" against
// the entity that drove the work
//
// Verbs:
//
//	link add     --entity=<id> --commit=<sha> [--message=...] [--repo=...]
//	                              [--author=...] [--committed-at=...]
//	link list    [--entity=<id>] [--commit=<sha>] [--json]
//	link remove  <link-id> [--confirm]
//	commit-show  <sha>    (find every entity linked to this commit)
//
// Ergonomics:
//
//	--commit=HEAD     resolves to `git rev-parse HEAD` + auto-fills message
//	                  via `git log -1 --format=%s` + author / committed-at
//	--commit=auto     same as HEAD (alias)
package main

import (
	"context"
	"fmt"
	"os/exec"
	"saas/pkg/constants"
	"strings"

	entCommitLink "dbent/gen/ent/commitlink"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newLinkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link git commits to any entity (task / run / mission / decision / …)",
		Long: `Polymorphic git-commit linker. Anchor "this work shipped as <sha>"
against the entity that drove the work. Use --commit=HEAD to auto-resolve
the current HEAD sha + capture message + author + committed-at via git.`,
	}
	cmd.AddCommand(newLinkAddCommand())
	cmd.AddCommand(newLinkListCommand())
	cmd.AddCommand(newLinkRemoveCommand())
	return cmd
}

func newCommitShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "commit-show <sha>",
		Short: "Show every entity linked to a given git commit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			sha := args[0]
			rows, err := client.CommitLink.Query().
				Where(entCommitLink.ProjectID(projectID), entCommitLink.Sha(sha)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "query links").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindCommitShow, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no entities linked to this commit)"))
				return nil
			}
			fmt.Printf("Commit %s — %d linked entities:\n", sha, len(rows))
			for _, r := range rows {
				fmt.Printf("  %s/%s\n", r.EntityTable, r.EntityID)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── link add ─────────────────────────────────────────────────────────────

func newLinkAddCommand() *cobra.Command {
	var f commonFlags
	var entityArg, commitArg, message, repoPath, author, committedAt string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Link a git commit to an entity (--entity=T-7 --commit=<sha>)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if entityArg == "" || commitArg == "" {
				return errcodes.New(errcodes.InvalidInput,
					"--entity and --commit are required").
					WithHint("example: lore link add --entity=T-7 --commit=HEAD")
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, "")
			if err != nil {
				return err
			}

			// Resolve pretty-form entity ID (e.g. T-7 → tsk_018f...) AND
			// derive its entity_table from the prefix
			opaqueID, err := resolvePrettyID(cmd.Context(), client, entityArg)
			if err != nil {
				return errcodes.New(errcodes.NotFound, "entity "+entityArg+" not found")
			}
			entityTable, err := tableForOpaqueID(opaqueID)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, err.Error())
			}

			// Resolve --commit=HEAD / auto via git
			sha, gitMsg, gitAuthor, gitWhen, gerr := resolveCommit(commitArg, repoPath)
			if gerr != nil {
				return errcodes.New(errcodes.InvalidInput, gerr.Error())
			}
			if message == "" {
				message = gitMsg
			}
			if author == "" {
				author = gitAuthor
			}
			if committedAt == "" {
				committedAt = gitWhen
			}

			actorID, err := resolveCurrentActorID(cmd.Context(), client)
			if err != nil {
				return err
			}

			create := client.CommitLink.Create().
				SetProjectID(projectID).
				SetEntityTable(entityTable).
				SetEntityID(opaqueID).
				SetSha(sha)
			if message != "" {
				create.SetMessage(message)
			}
			if repoPath != "" {
				create.SetRepoPath(repoPath)
			}
			if author != "" {
				create.SetAuthor(author)
			}
			if committedAt != "" {
				create.SetCommittedAt(committedAt)
			}
			create.SetCreatedByActorID(actorID)

			r, err := create.Save(cmd.Context())
			if err != nil {
				if strings.Contains(err.Error(), "UNIQUE constraint") {
					return errcodes.New(errcodes.InvalidInput,
						fmt.Sprintf("commit %s already linked to %s/%s", sha[:8], entityTable, opaqueID))
				}
				return errcodes.New(errcodes.Internal, "create link").WithCause(err)
			}
			shortMsg := message
			if len(shortMsg) > 60 {
				shortMsg = shortMsg[:57] + "..."
			}
			fmt.Printf("%s linked %s → %s/%s  (%s)\n",
				style.Success("✓"), sha[:8], entityTable, opaqueID, style.Code(r.ID))
			if shortMsg != "" {
				fmt.Printf("  message: %s\n", shortMsg)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&entityArg, constants.FlagEntity, "", "entity to link to (pretty form T-7, MS-2, D-3, etc. — or opaque ID)")
	cmd.Flags().StringVar(&commitArg, constants.FlagCommit, "", "git commit sha (or HEAD / auto for current HEAD)")
	cmd.Flags().StringVar(&message, "message", "", "commit message (auto-filled from git log when --commit=HEAD)")
	cmd.Flags().StringVar(&repoPath, "repo-path", "", "path to git repo on disk (default: cwd; distinct from --repo scope flag)")
	cmd.Flags().StringVar(&author, constants.FlagAuthor, "", "commit author (auto-filled from git)")
	cmd.Flags().StringVar(&committedAt, constants.FlagCommittedAt, "", "commit timestamp (auto-filled from git)")
	return cmd
}

// resolveCommit handles --commit=<sha> | HEAD | auto, returning the
// full sha plus auto-captured message / author / committed-at from git
// For a literal sha, all auto-fields stay empty (user can pass --message etc)
func resolveCommit(arg, repoPath string) (sha, msg, author, when string, err error) {
	if arg == "" {
		return "", "", "", "", fmt.Errorf("--commit required")
	}
	ref := arg
	autoFill := false
	if arg == "HEAD" || arg == "auto" {
		ref = "HEAD"
		autoFill = true
	}
	// Resolve sha
	args := []string{}
	if repoPath != "" {
		args = append(args, "-C", repoPath)
	}
	out, gerr := exec.Command("git", append(args, "rev-parse", ref)...).Output()
	if gerr != nil {
		// Not a git ref → if it looks like a literal sha, accept as-is
		if isLikelySha(arg) {
			return arg, "", "", "", nil
		}
		return "", "", "", "", fmt.Errorf("could not resolve %q via git rev-parse — use a full sha or run from inside a git repo", arg)
	}
	sha = strings.TrimSpace(string(out))
	if !autoFill {
		return sha, "", "", "", nil
	}
	// Auto-fill message + author + committed-at
	logOut, _ := exec.Command("git",
		append(args, "log", "-1", "--format=%s%n%an <%ae>%n%cI", ref)...).Output()
	parts := strings.SplitN(strings.TrimSpace(string(logOut)), "\n", 3)
	if len(parts) >= 1 {
		msg = parts[0]
	}
	if len(parts) >= 2 {
		author = parts[1]
	}
	if len(parts) >= 3 {
		when = parts[2]
	}
	return sha, msg, author, when, nil
}

func isLikelySha(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// tableForOpaqueID derives the SQL table name from an opaque ID's prefix
// e.g. tsk_xxx → tasks, mem_xxx → memories. Falls back to error for
// unsupported entities
func tableForOpaqueID(id string) (string, error) {
	prefix := id
	if i := strings.Index(id, "_"); i > 0 {
		prefix = id[:i]
	}
	switch prefix {
	case "tsk":
		return "tasks", nil
	case "mem":
		return "memories", nil
	case "msn":
		return "missions", nil
	case "tlt":
		return "task_lists", nil
	case "pln":
		return "plans", nil
	case "dec":
		return "decisions", nil
	case "rul":
		return "rules", nil
	case "hfx":
		return "hotfixes", nil
	case "pat":
		return "patterns", nil
	case "pbk":
		return "playbooks", nil
	case "prm":
		return "prompts", nil
	case "ann":
		return "architecture_notes", nil
	case "bhv":
		return "behaviours", nil
	case "ckr":
		return "cookbook_recipes", nil
	case "inc":
		return "incidents", nil
	case "sgg":
		return "suggestions", nil
	case "tpr":
		return "taste_prefs", nil
	case "snp":
		return "snapshots", nil
	case "wfl":
		return "workflows", nil
	case "wsp":
		return "workspaces", nil
	case "hnd":
		return "handoffs", nil
	case "rem":
		return "reminders", nil
	case "run":
		return "runs", nil
	case "tdc":
		return "tech_docs", nil
	case "cmt":
		return "comments", nil
	}
	return "", fmt.Errorf("unknown ID prefix %q — cannot derive entity table", prefix)
}

// ── link list ────────────────────────────────────────────────────────────

func newLinkListCommand() *cobra.Command {
	var f commonFlags
	var entityArg, commitArg string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List commit links (filter by --entity or --commit)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, "")
			if err != nil {
				return err
			}
			q := client.CommitLink.Query().Where(entCommitLink.ProjectID(projectID))
			if entityArg != "" {
				opaqueID, err := resolvePrettyID(cmd.Context(), client, entityArg)
				if err != nil {
					return errcodes.New(errcodes.NotFound, "entity "+entityArg+" not found")
				}
				q = q.Where(entCommitLink.EntityID(opaqueID))
			}
			if commitArg != "" {
				q = q.Where(entCommitLink.Sha(commitArg))
			}
			rows, err := q.All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list links").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindLinkList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no links)"))
				return nil
			}
			for _, r := range rows {
				msg := ""
				if r.Message != nil {
					m := *r.Message
					if len(m) > 60 {
						m = m[:57] + "..."
					}
					msg = " — " + m
				}
				fmt.Printf("%s  %s → %s/%s%s\n",
					style.Code(r.ID), r.Sha[:8], r.EntityTable, r.EntityID, msg)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&entityArg, constants.FlagEntity, "", "filter by entity (T-7, MS-2, opaque ID)")
	cmd.Flags().StringVar(&commitArg, constants.FlagCommit, "", "filter by sha")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── link remove ──────────────────────────────────────────────────────────

func newLinkRemoveCommand() *cobra.Command {
	var f commonFlags
	var confirm bool
	cmd := &cobra.Command{
		Use:   "remove <link-id>",
		Short: "Remove a commit link by its ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if !confirm {
				return errcodes.New(errcodes.InvalidInput, "--confirm required").
					WithHint("link remove deletes the row; pass --confirm to proceed")
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.CommitLink.DeleteOneID(args[0]).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "delete link").WithCause(err)
			}
			fmt.Printf("%s link %s removed\n", style.Success("✓"), args[0])
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&confirm, constants.FlagConfirm, false, "required to actually delete")
	return cmd
}

// Suppress unused-import warning when context isn't directly referenced
// in this file's top-level (it is via cmd.Context inside RunE closures)
var _ = context.Background
