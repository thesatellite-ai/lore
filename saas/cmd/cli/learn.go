// learn.go — `lore learn-from docs` + `lore learn list/promote/reject` (S2.8, S2.9)
//
// Bootstrap mini's knowledge from existing markdown:
//
//	lore learn-from docs           default sources: CLAUDE.md, AGENTS.md,
//	                                  README.md, .ai/**/*.md
//	lore learn-from docs --paths=foo.md,bar.md
//
// Each source file becomes one snapshot row + multiple learn_candidate rows
// (one per H2/H3 section). User reviews via:
//
//	lore learn list
//	lore learn promote <id> --target=memories
//	lore learn reject  <id> --reason="..."
//
// Catches: R16 #21 (resumable + skip-if-unchanged), R27 #16, R33 A5
// (hot/background staging — agents NEVER write directly to active tables)
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent/gen/ent"
	entLearnCandidate "dbent/gen/ent/learncandidate"
	entSnapshot "dbent/gen/ent/snapshot"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

func newLearnCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "learn",
		Short: "Manage learn_candidates (background-learning staging)",
	}
	cmd.AddCommand(newLearnFromCommand())
	cmd.AddCommand(newLearnListCommand())
	cmd.AddCommand(newLearnPromoteCommand())
	cmd.AddCommand(newLearnRejectCommand())
	return cmd
}

// newLearnFromRootAliasCommand exposes `lore learn-from docs` at the root
// (the "learn from" subcommand under `lore learn` works the same way)
func newLearnFromRootAliasCommand() *cobra.Command {
	cmd := newLearnFromCommand()
	cmd.Use = "learn-from <kind>"
	return cmd
}

// `lore learn from docs` is the canonical form under the learn group
func newLearnFromCommand() *cobra.Command {
	var f commonFlags
	var paths []string
	cmd := &cobra.Command{
		Use:   "from <kind>",
		Short: "Bootstrap knowledge from existing sources",
		Long: `Imports existing markdown into staging (learn_candidates)

Subkinds:
  docs   import .md files (default: CLAUDE.md, AGENTS.md, README.md, .ai/**/*.md)

Each source file becomes one snapshot. Each H2/H3 section becomes one
learn_candidate. Review and promote with:

  lore learn list
  lore learn promote <id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "docs" {
				return errcodes.New(errcodes.NotImplemented,
					fmt.Sprintf("learn-from %q not yet supported", args[0])).
					WithHint("use `learn-from docs` (the only subkind in v0.1)")
			}
			return runLearnFromDocs(cmd.Context(), &f, paths)
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringSliceVar(&paths, constants.FlagPaths, nil,
		"explicit paths (default: CLAUDE.md, AGENTS.md, README.md, .ai/**/*.md)")
	return cmd
}

func runLearnFromDocs(ctx context.Context, f *commonFlags, explicitPaths []string) error {
	rctx, client, err := resolveContext(f)
	if err != nil {
		return err
	}
	defer client.Close()

	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}

	var sources []string
	if len(explicitPaths) > 0 {
		// Expand any directories in --paths to their `*.md` files (recursive)
		// Round 38 #9 — silent "is a directory" skips are confusing
		for _, p := range explicitPaths {
			info, err := os.Stat(p)
			if err != nil {
				return errcodes.New(errcodes.BadPath, p+": "+err.Error())
			}
			if !info.IsDir() {
				sources = append(sources, p)
				continue
			}
			_ = filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if !info.IsDir() && strings.HasSuffix(path, ".md") {
					sources = append(sources, path)
				}
				return nil
			})
		}
	} else {
		// Default v0.1 sources per Round 38 / Tier 2 #8
		for _, name := range []string{"CLAUDE.md", "AGENTS.md", "README.md"} {
			p := filepath.Join(rctx.ProjectRoot, name)
			if _, err := os.Stat(p); err == nil {
				sources = append(sources, p)
			}
		}
		// .ai/**/*.md
		aiDir := filepath.Join(rctx.ProjectRoot, ".ai")
		if _, err := os.Stat(aiDir); err == nil {
			_ = filepath.Walk(aiDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if !info.IsDir() && strings.HasSuffix(path, ".md") {
					sources = append(sources, path)
				}
				return nil
			})
		}
	}

	if len(sources) == 0 {
		return errcodes.New(errcodes.NotFound,
			"no source files found").
			WithHint("expected CLAUDE.md, AGENTS.md, README.md, or .ai/**/*.md")
	}

	jobID := ids.MustNew(ids.PrefixLearnCandidate)
	snapshots := 0
	candidates := 0

	for _, path := range sources {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipped %s: %v\n", path, err)
			continue
		}
		body, err := textnorm.Normalize(string(data))
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipped %s: %v\n", path, err)
			continue
		}
		contentSha := sha256Hex([]byte(body))
		sourceRef := relPath(rctx.ProjectRoot, path)

		// Skip if we already imported this exact content
		existing, err := client.Snapshot.Query().
			Where(
				entSnapshot.ProjectID(projectID),
				entSnapshot.SourceRef(sourceRef+"@"+contentSha),
			).
			Limit(1).
			All(ctx)
		if err == nil && len(existing) > 0 {
			fmt.Printf("%s %s (unchanged, skipped)\n", style.Muted("·"), sourceRef)
			continue
		}

		// Snapshot row (full file)
		title := filepath.Base(path)
		_, err = client.Snapshot.Create().
			SetProjectID(projectID).
			SetTitle(title).
			SetBody(body).
			SetTakenAt(time.Now()).
			SetSourceKind(constants.SourceLearnFrom.String()).
			SetSourceRef(sourceRef + "@" + contentSha).
			Save(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "snapshot %s: %v\n", path, err)
			continue
		}
		snapshots++

		// Section-level learn_candidates
		sections := splitMarkdownSections(body)
		for _, sec := range sections {
			if strings.TrimSpace(sec.body) == "" {
				continue
			}
			_, err := client.LearnCandidate.Create().
				SetProjectID(projectID).
				SetProposedKind(string(constants.EntityMemory)).
				SetProposedBody(sec.body).
				SetSourceKind(entLearnCandidate.SourceKindLearnFrom).
				SetSourceRef(sourceRef + "#" + sec.heading).
				SetJobID(jobID).
				Save(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "candidate %s#%s: %v\n", sourceRef, sec.heading, err)
				continue
			}
			candidates++
		}
	}

	fmt.Printf("%s learn-from docs complete\n", style.Success("✓"))
	fmt.Printf("  snapshots:  %d\n", snapshots)
	fmt.Printf("  candidates: %d (job_id=%s)\n", candidates, jobID)
	fmt.Println()
	fmt.Println(style.Hint("  next:"))
	fmt.Println(style.Hint("    lore learn list                       # review pending candidates"))
	fmt.Println(style.Hint("    lore learn promote <id>               # accept one"))
	fmt.Println(style.Hint("    lore learn promote --job=" + jobID + " --all   # bulk accept (deferred)"))
	return nil
}

// SetTakenAtNow doesn't exist — use SetTakenAt(time.Now()) helper
// Provide a wrapper in this file rather than touching the generated client
// (The compiler will confirm.)

// markdownSection captures a heading + its body content
type markdownSection struct {
	heading string
	body    string
}

// splitMarkdownSections returns sections delimited by H2/H3 headers
// First section (before any H2) is included as "_intro"
func splitMarkdownSections(md string) []markdownSection {
	var sections []markdownSection
	current := markdownSection{heading: "_intro"}
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			if strings.TrimSpace(current.body) != "" {
				sections = append(sections, current)
			}
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			current = markdownSection{heading: heading}
			continue
		}
		current.body += line + "\n"
	}
	if strings.TrimSpace(current.body) != "" {
		sections = append(sections, current)
	}
	return sections
}

// relPath returns path relative to root if possible; absolute otherwise
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// sha256Hex is defined in render.go; re-declare guard removed

// ── learn list ───────────────────────────────────────────────────────────

func newLearnListCommand() *cobra.Command {
	var f commonFlags
	var status string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List learn_candidates",
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

			q := client.LearnCandidate.Query().
				Where(entLearnCandidate.ProjectID(projectID))
			if status != "" {
				q = q.Where(entLearnCandidate.StatusEQ(learnStatus(status)))
			}
			rows, err := q.Order(ent.Asc("id")).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list candidates").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindLearnList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no candidates)"))
				return nil
			}
			for _, r := range rows {
				excerpt := strings.ReplaceAll(r.ProposedBody, "\n", " ")
				if len(excerpt) > 80 {
					excerpt = excerpt[:77] + "..."
				}
				fmt.Printf("[%s] %s → %s   %s\n",
					r.Status, style.Code(r.ID), r.ProposedKind, excerpt)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&status, constants.FlagStatus, "pending", "filter: pending | accepted | rejected | expired (empty = all)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output (stable envelope)")
	return cmd
}

// ── learn promote ────────────────────────────────────────────────────────

func newLearnPromoteCommand() *cobra.Command {
	var f commonFlags
	var target string
	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a learn_candidate to its target table",
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

			candidate, err := client.LearnCandidate.Query().
				Where(
					entLearnCandidate.ProjectID(projectID),
					entLearnCandidate.ID(args[0]),
				).
				Only(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.NotFound,
					fmt.Sprintf("candidate %q not found", args[0]))
			}

			tk := target
			if tk == "" {
				tk = candidate.ProposedKind
			}

			switch tk {
			case "memories":
				_, err = client.Memory.Create().
					SetProjectID(projectID).
					SetBody(candidate.ProposedBody).
					SetSourceKind(candidate.SourceKind.String()).
					Save(cmd.Context())
			default:
				return errcodes.New(errcodes.NotImplemented,
					fmt.Sprintf("promote target %q not yet supported in v0.1", tk)).
					WithHint("only --target=memories is implemented")
			}
			if err != nil {
				return errcodes.New(errcodes.Internal, "promote").WithCause(err)
			}

			_, err = client.LearnCandidate.UpdateOne(candidate).
				SetStatus(entLearnCandidate.StatusAccepted).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "mark accepted").WithCause(err)
			}

			fmt.Printf("%s promoted %s → %s\n", style.Success("✓"), candidate.ID, tk)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&target, constants.FlagTarget, "", "target table (default: candidate's proposed_kind)")
	return cmd
}

// ── learn reject ─────────────────────────────────────────────────────────

func newLearnRejectCommand() *cobra.Command {
	var f commonFlags
	var reason, ids string
	var all bool
	cmd := &cobra.Command{
		Use:   "reject [id]",
		Short: "Mark a learn_candidate as rejected (single, --ids=a,b,c, or --all)",
		Args:  cobra.MaximumNArgs(1),
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

			// Build target ID set from {arg, --ids, --all}
			targets := []string{}
			if len(args) > 0 {
				targets = append(targets, args[0])
			}
			if ids != "" {
				for _, id := range strings.Split(ids, ",") {
					if id = strings.TrimSpace(id); id != "" {
						targets = append(targets, id)
					}
				}
			}
			if all {
				pending, err := client.LearnCandidate.Query().
					Where(entLearnCandidate.ProjectID(projectID)).
					Where(entLearnCandidate.StatusEQ(entLearnCandidate.StatusPending)).
					All(cmd.Context())
				if err != nil {
					return errcodes.New(errcodes.Internal, "list pending").WithCause(err)
				}
				for _, p := range pending {
					targets = append(targets, p.ID)
				}
			}
			if len(targets) == 0 {
				return errcodes.New(errcodes.InvalidInput,
					"no targets — pass <id>, --ids=a,b,c, or --all")
			}

			rejected := 0
			for _, id := range targets {
				if err := client.LearnCandidate.UpdateOneID(id).
					SetStatus(entLearnCandidate.StatusRejected).
					Exec(cmd.Context()); err != nil {
					fmt.Printf("%s skip %s (%s)\n", style.Warn("✗"), id, err.Error())
					continue
				}
				rejected++
			}
			suffix := ""
			if reason != "" {
				suffix = " (" + reason + ")"
			}
			fmt.Printf("%s rejected %d candidate(s)%s\n", style.Success("✓"), rejected, suffix)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&reason, "reason", "", "human reason for rejection (logged)")
	cmd.Flags().StringVar(&ids, constants.FlagIDs, "", "comma-separated list of candidate IDs to reject in one call")
	cmd.Flags().BoolVar(&all, constants.FlagAll, false, "reject every pending candidate in the current project")
	return cmd
}

func learnStatus(s string) entLearnCandidate.Status {
	v := entLearnCandidate.Status(s)
	if err := entLearnCandidate.StatusValidator(v); err != nil {
		return entLearnCandidate.StatusPending
	}
	return v
}

// SetTakenAtNow helper — wraps SetTakenAt with time.Now to keep the call site
// readable in runLearnFromDocs
func init() {
	_ = sha256.New
	_ = hex.EncodeToString
}
