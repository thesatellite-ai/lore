// bench.go — `lore bench` root command group + cross-subcommand helpers
//
// Subcommand groups live in sibling files (bench_eval.go etc.). This file
// only assembles the tree and shares helpers used by multiple groups
package main

import (
	"context"
	"fmt"
	"time"

	"saas/pkg/aicoder/signals/coord"

	"dbent/gen/ent"
	entDecision "dbent/gen/ent/decision"
	entHotfix "dbent/gen/ent/hotfix"
	entMemory "dbent/gen/ent/memory"
	entRule "dbent/gen/ent/rule"

	"github.com/spf13/cobra"
)

func newBenchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Benchmark engine — define + run + report on eval tasks",
		Long: `lore's evaluation engine. Three primary nouns:

  eval    — task definition (template; lives independent of runs)
  run     — one benchmark execution (N tasks × M arms × R attempts)
  result  — one task × arm × attempt outcome

Plus report/grader/config for analysis + meta-tools

See BENCH_DESIGN.md and EVAL_PLAN.md for methodology.`,
	}
	cmd.AddCommand(newBenchEvalGroup())
	cmd.AddCommand(newBenchRunGroup())
	cmd.AddCommand(newBenchResultGroup())
	cmd.AddCommand(newBenchReportGroup())
	cmd.AddCommand(newBenchGraderGroup())
	return cmd
}

// timeNow is a tiny wrapper so tests can stub time later
func timeNow() time.Time { return time.Now() }

// lookupRuleBody resolves "R-7" or "rul_<hex>" → (body, opaque_id)
// Returns ("", "") if not found (caller treats as no-link)
func lookupRuleBody(ctx context.Context, client *ent.Client, projectID, ref string) (string, string) {
	if d, ok := coreBenchRow(ctx, "rules", projectID, ref); ok {
		return d.Str("body"), ref
	} else if coreApp != nil {
		return "", ""
	}
	q := client.Rule.Query().Where(entRule.ProjectID(projectID))
	q = q.Where(entRule.ID(ref))
	r, err := q.Only(ctx)
	if err != nil {
		return "", ""
	}
	return r.Body, r.ID
}

func lookupHotfixBody(ctx context.Context, client *ent.Client, projectID, ref string) (string, string) {
	if d, ok := coreBenchRow(ctx, "hotfixes", projectID, ref); ok {
		return d.Str("body"), ref
	} else if coreApp != nil {
		return "", ""
	}
	q := client.Hotfix.Query().Where(entHotfix.ProjectID(projectID))
	q = q.Where(entHotfix.ID(ref))
	r, err := q.Only(ctx)
	if err != nil {
		return "", ""
	}
	return r.Body, r.ID
}

func lookupDecisionBody(ctx context.Context, client *ent.Client, projectID, ref string) (string, string) {
	if d, ok := coreBenchRow(ctx, "decisions", projectID, ref); ok {
		body := d.Str("title")
		if b := d.Str("body"); b != "" {
			body = body + "\n\n" + b
		}
		return body, ref
	} else if coreApp != nil {
		return "", ""
	}
	q := client.Decision.Query().Where(entDecision.ProjectID(projectID))
	q = q.Where(entDecision.ID(ref))
	r, err := q.Only(ctx)
	if err != nil {
		return "", ""
	}
	body := r.Title
	if r.Body != "" {
		body = body + "\n\n" + r.Body
	}
	return body, r.ID
}

func lookupMemoryBody(ctx context.Context, client *ent.Client, projectID, ref string) (string, string) {
	if d, ok := coreBenchRow(ctx, "memories", projectID, ref); ok {
		return d.Str("body"), ref
	} else if coreApp != nil {
		return "", ""
	}
	q := client.Memory.Query().Where(entMemory.ProjectID(projectID))
	q = q.Where(entMemory.ID(ref))
	r, err := q.Only(ctx)
	if err != nil {
		return "", ""
	}
	return r.Body, r.ID
}

// coreBenchRow reads a knowledge row a bench task links to through the
// core (rules, hotfixes, decisions and memories are core-owned, so lore.db
// only holds a bridge copy). A row of another project is not found, as in
// v0.1.17. With no core mounted (unit tests that build lore.db fixtures)
// ok is false and the callers read lore.db.
func coreBenchRow(ctx context.Context, table, projectID, ref string) (coord.Doc, bool) {
	if coreApp == nil {
		return nil, false
	}
	d, ok, err := coreDoc(ctx, table, ref)
	if err != nil || !ok {
		return nil, false
	}
	if pid := d.Str("project_id"); pid != "" && projectID != "" && pid != projectID {
		return nil, false
	}
	return d, true
}

// hardcoded fail-on-call so the linker doesn't drop unused imports
// when bench groups other than eval are still TODO
var _ = fmt.Sprintf
