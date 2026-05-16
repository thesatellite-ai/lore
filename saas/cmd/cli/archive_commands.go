// archive_commands.go — Tier 1 (A–E) + Tier 2 (G, J) follow-ups from the
// post-Round-35 audit
//
//	A. memory list / memory show          (browse + detail; symmetric with rule/decision)
//	B. <kind> archive / unarchive         (soft-delete via archived_at; covers 9 entities)
//	D. handoff ack                        (status_str transition)
//	E. mission pause / mission resume     (status enum transitions)
//	G. memory invalidate                  (bitemporal valid_until = now)
//	J. project archive / repo archive     (same archive verb, project-mgmt entities)
//
// Wiring: each command is added to its existing group in the same init
// pass. See main.go / generated_commands.go for the group functions
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"saas/pkg/constants"
	"time"

	"dbent/gen/ent"
	entMemory "dbent/gen/ent/memory"
	entMission "dbent/gen/ent/mission"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// ── shared helpers ──────────────────────────────────────────────────────

// archiveTarget abstracts the per-entity update shape. Each entity gets a
// tiny wrapper that calls the generated SetArchivedAt / ClearArchivedAt /
// DeleteOneID
type archiveTarget struct {
	// get returns (rowID, prettyLabel, err) for the matched row. prettyLabel
	// is what we print in success messages — "M-12", "R-3", "myproject", etc
	get     func(ctx context.Context, client *ent.Client, id string) (string, string, error)
	archive func(ctx context.Context, client *ent.Client, id string, when time.Time) error
	unArch  func(ctx context.Context, client *ent.Client, id string) error
	// del is the hard-delete primitive. Nil = entity doesn't support delete
	del   func(ctx context.Context, client *ent.Client, id string) error
	label string // "memory", "rule", etc — used for error text
}

func runArchive(cmd *cobra.Command, t archiveTarget, idArg string) error {
	// Read --db/--project/--repo/--read-only values that were bound on cmd
	// when the command was built (in archiveCmdPair). Calling bindCommonFlags
	// here would re-register the flags and panic with "flag redefined"
	flags := extractCommon(cmd)
	if err := refuseIfReadOnly(flags); err != nil {
		return err
	}
	_, client, err := resolveContext(flags)
	if err != nil {
		return err
	}
	defer client.Close()
	resolvedArg, err := resolvePrettyID(cmd.Context(), client, idArg)
	if err != nil {
		return errcodes.New(errcodes.NotFound, fmt.Sprintf("%s %q not found", t.label, idArg))
	}
	id, pretty, err := t.get(cmd.Context(), client, resolvedArg)
	if err != nil {
		return errcodes.New(errcodes.NotFound, fmt.Sprintf("%s %q not found", t.label, idArg))
	}
	if err := t.archive(cmd.Context(), client, id, time.Now()); err != nil {
		return errcodes.New(errcodes.Internal, "archive "+t.label).WithCause(err)
	}
	fmt.Printf("%s %s archived\n", style.Success("✓"), pretty)
	return nil
}

func runUnarchive(cmd *cobra.Command, t archiveTarget, idArg string) error {
	flags := extractCommon(cmd)
	if err := refuseIfReadOnly(flags); err != nil {
		return err
	}
	_, client, err := resolveContext(flags)
	if err != nil {
		return err
	}
	defer client.Close()
	resolvedArg, err := resolvePrettyID(cmd.Context(), client, idArg)
	if err != nil {
		return errcodes.New(errcodes.NotFound, fmt.Sprintf("%s %q not found", t.label, idArg))
	}
	id, pretty, err := t.get(cmd.Context(), client, resolvedArg)
	if err != nil {
		return errcodes.New(errcodes.NotFound, fmt.Sprintf("%s %q not found", t.label, idArg))
	}
	if err := t.unArch(cmd.Context(), client, id); err != nil {
		return errcodes.New(errcodes.Internal, "unarchive "+t.label).WithCause(err)
	}
	fmt.Printf("%s %s unarchived\n", style.Success("✓"), pretty)
	return nil
}

// extractCommon reads --db/--project/--repo/--read-only flags from cmd back
// into a commonFlags struct. We use this when the archive command shares
// a binding setup helper
func extractCommon(cmd *cobra.Command) *commonFlags {
	f := &commonFlags{}
	if v, err := cmd.Flags().GetString("db"); err == nil {
		f.flagDB = v
	}
	if v, err := cmd.Flags().GetString("project"); err == nil {
		f.flagProject = v
	}
	if v, err := cmd.Flags().GetString("repo"); err == nil {
		f.flagRepo = v
	}
	if v, err := cmd.Flags().GetBool("read-only"); err == nil {
		f.flagReadOnly = v
	}
	return f
}

// archiveCmdPair builds the (archive, unarchive) cobra command pair for a
// given target. The two commands share flag-binding scaffolding
func archiveCmdPair(t archiveTarget) (*cobra.Command, *cobra.Command) {
	arch := &cobra.Command{
		Use:   "archive <id>",
		Short: fmt.Sprintf("Archive a %s (soft-delete via archived_at)", t.label),
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runArchive(cmd, t, args[0]) },
	}
	un := &cobra.Command{
		Use:   "unarchive <id>",
		Short: fmt.Sprintf("Unarchive a %s (clear archived_at)", t.label),
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runUnarchive(cmd, t, args[0]) },
	}
	for _, c := range []*cobra.Command{arch, un} {
		var f commonFlags
		bindCommonFlags(c, &f)
	}
	return arch, un
}

// newDeleteCommand builds a `<kind> delete <id>` cobra command for entities
// that support hard delete. Requires --confirm to fire — prevents the
// fat-finger / "wait that wasn't supposed to be a real delete" case
// Soft-delete archive should be preferred for entities that have it; this
// is the escape hatch for secrets-leaked / mistake-captured rows
func newDeleteCommand(t archiveTarget) *cobra.Command {
	if t.del == nil {
		return nil
	}
	var f commonFlags
	var confirm bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: fmt.Sprintf("HARD-delete a %s (no undo; archive is usually what you want)", t.label),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if !confirm {
				return errcodes.New(errcodes.InvalidInput,
					"--confirm required to hard-delete").
					WithHint(fmt.Sprintf("prefer `%s archive %s` for soft-delete (reversible); pass --confirm if you really mean hard-delete", t.label, args[0]))
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			resolvedArg, err := resolvePrettyID(cmd.Context(), client, args[0])
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("%s %q not found", t.label, args[0]))
			}
			id, pretty, err := t.get(cmd.Context(), client, resolvedArg)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("%s %q not found", t.label, args[0]))
			}
			if err := t.del(cmd.Context(), client, id); err != nil {
				return errcodes.New(errcodes.Internal, "delete "+t.label).WithCause(err)
			}
			fmt.Printf("%s %s deleted (hard)\n", style.Success("✓"), pretty)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&confirm, constants.FlagConfirm, false, "required to actually delete")
	return cmd
}

// ── per-entity targets ──────────────────────────────────────────────────

var memoryArchiveTarget = archiveTarget{
	label: "memory",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Memory.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Memory.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Memory.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Memory.DeleteOneID(id).Exec(ctx)
	},
}

var ruleArchiveTarget = archiveTarget{
	label: "rule",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Rule.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Rule.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Rule.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Rule.DeleteOneID(id).Exec(ctx)
	},
}

var decisionArchiveTarget = archiveTarget{
	label: "decision",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Decision.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Decision.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Decision.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Decision.DeleteOneID(id).Exec(ctx)
	},
}

var hotfixArchiveTarget = archiveTarget{
	label: "hotfix",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Hotfix.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Hotfix.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Hotfix.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Hotfix.DeleteOneID(id).Exec(ctx)
	},
}

var patternArchiveTarget = archiveTarget{
	label: "pattern",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Pattern.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Pattern.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Pattern.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Pattern.DeleteOneID(id).Exec(ctx)
	},
}

var playbookArchiveTarget = archiveTarget{
	label: "playbook",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Playbook.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Playbook.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Playbook.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Playbook.DeleteOneID(id).Exec(ctx)
	},
}

var promptArchiveTarget = archiveTarget{
	label: "prompt",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Prompt.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Prompt.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Prompt.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Prompt.DeleteOneID(id).Exec(ctx)
	},
}

var projectArchiveTarget = archiveTarget{
	label: "project",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Project.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, r.Name, nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Project.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Project.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Project.DeleteOneID(id).Exec(ctx)
	},
}

var repoArchiveTarget = archiveTarget{
	label: "repo",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Repo.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, r.MountName, nil
	},
	archive: func(ctx context.Context, c *ent.Client, id string, when time.Time) error {
		return c.Repo.UpdateOneID(id).SetArchivedAt(when).Exec(ctx)
	},
	unArch: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Repo.UpdateOneID(id).ClearArchivedAt().Exec(ctx)
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Repo.DeleteOneID(id).Exec(ctx)
	},
}

// ── memory list / show / invalidate (A + G) ────────────────────────────

func newMemoryListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut, includeArchived bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memories (active by default; pass --archived to include)",
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
			q := client.Memory.Query().Where(entMemory.ProjectID(projectID))
			if !includeArchived {
				q = q.Where(entMemory.ArchivedAtIsNil())
			}
			rows, err := q.Order(ent.Asc(entMemory.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list memories").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindMemoryList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no memories)"))
				return nil
			}
			for _, m := range rows {
				body := m.Body
				if len(body) > 80 {
					body = body[:77] + "..."
				}
				badge := ""
				if m.ArchivedAt != nil {
					badge = style.Muted(" [archived]")
				}
				fmt.Printf("%s [%s] %s%s\n", m.ID, m.Kind, body, badge)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	cmd.Flags().BoolVar(&includeArchived, constants.FlagArchived, false, "include archived memories")
	return cmd
}

func newMemoryShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show memory details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			r, err := client.Memory.Get(cmd.Context(), args[0])
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("memory %q not found", args[0]))
			}
			if jsonOut {
				printJSON(constants.KindMemoryShow, r, 0)
				return nil
			}
			fmt.Printf("%s %s\n", r.ID, style.Code(r.ID))
			fmt.Printf("  kind:   %s\n", r.Kind)
			fmt.Printf("  trust:  %.2f\n", r.TrustScore)
			fmt.Printf("  source: %s\n", r.SourceKind)
			if r.SupersededByID != nil {
				fmt.Printf("  superseded-by: %s\n", *r.SupersededByID)
			}
			if r.ArchivedAt != nil {
				fmt.Printf("  archived-at: %s\n", r.ArchivedAt.Format(time.RFC3339))
			}
			if !r.ValidUntil.IsZero() {
				fmt.Printf("  valid-until: %s\n", r.ValidUntil.Format(time.RFC3339))
			}
			fmt.Println()
			fmt.Println(r.Body)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newMemoryInvalidateCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "invalidate <id>",
		Short: "Mark a memory invalid as of now (bitemporal: sets valid_until)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			r, err := client.Memory.Get(cmd.Context(), args[0])
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("memory %q not found", args[0]))
			}
			if err := client.Memory.UpdateOneID(r.ID).SetValidUntil(time.Now()).Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "invalidate memory").WithCause(err)
			}
			fmt.Printf("%s %s invalidated (valid_until=now)\n", style.Success("✓"), r.ID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// ── mission pause / resume (E) ──────────────────────────────────────────

func newMissionPauseCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "pause <id>",
		Short: "Mark mission as paused",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setMissionStatus(cmd.Context(), &f, args[0], entMission.StatusPaused, "paused")
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newMissionResumeCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "resume <id>",
		Short: "Mark mission as active (resume from paused)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setMissionStatus(cmd.Context(), &f, args[0], entMission.StatusActive, "resumed")
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func setMissionStatus(ctx context.Context, f *commonFlags, idArg string, target entMission.Status, label string) error {
	if err := refuseIfReadOnly(f); err != nil {
		return err
	}
	_, client, err := resolveContext(f)
	if err != nil {
		return err
	}
	defer client.Close()
	m, err := client.Mission.Get(ctx, idArg)
	if err != nil {
		return errcodes.New(errcodes.NotFound, fmt.Sprintf("mission %q not found", idArg))
	}
	if err := client.Mission.UpdateOneID(m.ID).SetStatus(target).Exec(ctx); err != nil {
		return errcodes.New(errcodes.Internal, "update mission status").WithCause(err)
	}
	fmt.Printf("%s %s %s\n", style.Success("✓"), m.ID, label)
	return nil
}

// ── handoff ack (D) ─────────────────────────────────────────────────────

func newHandoffAckCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "ack <id>",
		Short: "Acknowledge a handoff (sets status_str=acked)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			r, err := client.Handoff.Get(cmd.Context(), args[0])
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("handoff %q not found", args[0]))
			}
			if err := client.Handoff.UpdateOneID(r.ID).SetStatusStr("acked").Exec(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "ack handoff").WithCause(err)
			}
			fmt.Printf("%s %s acked\n", style.Success("✓"), r.ID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// printJSON is a tiny wrapper; the project already has one but we keep this
// file self-contained for the archive flow. Skipped — using existing helper
var _ = json.Marshal

// ── delete-only targets (entities WITHOUT archived_at) ───────────────────
//
// These entities don't carry archived_at, so `archive` / `unarchive` aren't
// meaningful. Hard delete is the only escape hatch. Each target sets `del`
// + `get` only; archive / unArch stay nil because archiveCmdPair is never
// called against them — only newDeleteCommand is

var taskDeleteTarget = archiveTarget{
	label: "task",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Task.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Task.DeleteOneID(id).Exec(ctx)
	},
}

var missionDeleteTarget = archiveTarget{
	label: "mission",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Mission.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Mission.DeleteOneID(id).Exec(ctx)
	},
}

var tasklistDeleteTarget = archiveTarget{
	label: "tasklist",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.TaskList.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.TaskList.DeleteOneID(id).Exec(ctx)
	},
}

var planDeleteTarget = archiveTarget{
	label: "plan",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Plan.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Plan.DeleteOneID(id).Exec(ctx)
	},
}

var architectureNoteDeleteTarget = archiveTarget{
	label: "architecturenote",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.ArchitectureNote.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.ArchitectureNote.DeleteOneID(id).Exec(ctx)
	},
}

var behaviourDeleteTarget = archiveTarget{
	label: "behaviour",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Behaviour.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Behaviour.DeleteOneID(id).Exec(ctx)
	},
}

var cookbookRecipeDeleteTarget = archiveTarget{
	label: "cookbookrecipe",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.CookbookRecipe.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.CookbookRecipe.DeleteOneID(id).Exec(ctx)
	},
}

var incidentDeleteTarget = archiveTarget{
	label: "incident",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Incident.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Incident.DeleteOneID(id).Exec(ctx)
	},
}

var suggestionDeleteTarget = archiveTarget{
	label: "suggestion",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Suggestion.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Suggestion.DeleteOneID(id).Exec(ctx)
	},
}

var tastePrefDeleteTarget = archiveTarget{
	label: "tastepref",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.TastePref.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.TastePref.DeleteOneID(id).Exec(ctx)
	},
}

var workflowDeleteTarget = archiveTarget{
	label: "workflow",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Workflow.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Workflow.DeleteOneID(id).Exec(ctx)
	},
}

var workspaceDeleteTarget = archiveTarget{
	label: "workspace",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Workspace.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Workspace.DeleteOneID(id).Exec(ctx)
	},
}

var commentDeleteTarget = archiveTarget{
	label: "comment",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Comment.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, r.ID, nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Comment.DeleteOneID(id).Exec(ctx)
	},
}

var handoffDeleteTarget = archiveTarget{
	label: "handoff",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Handoff.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Handoff.DeleteOneID(id).Exec(ctx)
	},
}

var reminderDeleteTarget = archiveTarget{
	label: "reminder",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.Reminder.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, r.ID, nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.Reminder.DeleteOneID(id).Exec(ctx)
	},
}

var techDocDeleteTarget = archiveTarget{
	label: "techdoc",
	get: func(ctx context.Context, c *ent.Client, id string) (string, string, error) {
		r, err := c.TechDoc.Get(ctx, id)
		if err != nil {
			return "", "", err
		}
		return r.ID, fmt.Sprintf("%s", r.ID), nil
	},
	del: func(ctx context.Context, c *ent.Client, id string) error {
		return c.TechDoc.DeleteOneID(id).Exec(ctx)
	},
}
