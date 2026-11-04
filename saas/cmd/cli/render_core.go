// render_core.go — the rows `lore render` pins, read through the 0.1.18
// core instead of lore.db.
//
// rules and hotfixes are core-owned tables (lsync.CoreOwned): their row
// files are the truth and the engine reads them. The committed render
// (.lore/LORE.md) may hold only the repo's own rows and rows of stores whose
// visibility is shareable (CFG4, CFG6); internal stores go to the private
// render (render_internal.go). With no shareable store attached,
// renderSources is the repo store alone, which is exactly the set v0.1.17
// rendered from lore.db.
//
// The selection and order match v0.1.17's ent queries byte for byte:
//
//   - rules: severity=must, not archived, project_id = this project or
//     absent (a global rule);
//   - hotfixes: severity critical or high, not archived, project_id = this
//     project;
//   - both ordered by id ascending (string order, as SQLite compared them).
//
// 0.1.18 (CONTEXT_BUDGET 6.3, 6.12): a row with superseded_by_id set is
// no longer pinned (its replacement is), and an activation: manual rule
// never enters the core; both are counted in the render report instead.
//
// The project filter applies to repo-store rows only: a shareable store's
// rows carry its own folder's project id, and attaching the store is what
// puts them in scope.
package main

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"saas/pkg/aicoder/errcodes"
)

// Row fields render reads (schema/builtin_knowledge.go names).
const (
	renderFieldProjectID  = "project_id"
	renderFieldArchivedAt = "archived_at"
	renderFieldSeverity   = "severity"
	renderFieldTrustScore = "trust_score"
	renderFieldBody       = "body"
	renderFieldTitle      = "title"
	renderFieldSuperseded = "superseded_by_id"
	renderFieldActivation = "activation"
	renderFieldGlobs      = "globs"
)

// Severities render pins (v0.1.17 ent enum values, the same strings the
// core's OneOf accepts).
const (
	renderRuleSeverityMust       = "must"
	renderHotfixSeverityCritical = "critical"
	renderHotfixSeverityHigh     = "high"
)

// renderDefaultTrust is v0.1.17's LifecycleMixin default for trust_score:
// a row file without the field renders as ent read it back.
const renderDefaultTrust = 0.5

// pinnedRule is one rule as the render prints it.
type pinnedRule struct {
	ID         string
	Severity   string
	TrustScore float64
	Body       string
	// Budget inputs (CONTEXT_BUDGET 6.4, 6.6).
	Summary    string
	Activation string
	Globs      string
	Updated    time.Time
	TagIDs     []string
}

// pinnedHotfix is one hotfix as the render prints it.
type pinnedHotfix struct {
	ID       string
	Severity string
	Title    string
	Body     string
	Summary  string
	TagIDs   []string
}

// pinSkips counts rows that would have been pinned before 0.1.18 but are
// left out of the core, by reason (the render report's "moved out" rows).
type pinSkips struct {
	SupersededRules, SupersededHotfixes int
	ManualRules                         int
}

// renderSources are the stores whose rows may reach the committed render:
// the repo store first, then every attached store marked shareable.
func renderSources(ctx context.Context) ([]store.Source, error) {
	shared, err := coreApp.ShareableStores(ctx)
	if err != nil {
		return nil, coreErr(err)
	}
	return append([]store.Source{store.SourceRepo}, shared...), nil
}

// renderRow is a live row and the store it came from.
type renderRow struct {
	source store.Source
	row.Row
}

// inProject reports whether r is in scope for projectID: any row of a
// shareable store; a repo row whose project_id is projectID (or absent,
// when allowGlobal).
func (r renderRow) inProject(projectID string, allowGlobal bool) bool {
	if r.source != store.SourceRepo {
		return true
	}
	p := renderField(r.Row, renderFieldProjectID)
	return p == projectID || (allowGlobal && p == "")
}

// renderLiveRows lists the live, not archived rows of table across the
// render sources.
func renderLiveRows(ctx context.Context, table row.Table) ([]renderRow, error) {
	if coreApp == nil {
		return nil, errcodes.New(errcodes.Internal, "the core is not mounted")
	}
	srcs, err := renderSources(ctx)
	if err != nil {
		return nil, err
	}
	var out []renderRow
	for _, src := range srcs {
		rows, err := coreApp.List(ctx, src, table)
		if err != nil {
			return nil, coreErr(err)
		}
		for _, r := range rows {
			if r.Deleted || renderField(r, renderFieldArchivedAt) != "" {
				continue
			}
			out = append(out, renderRow{source: src, Row: r})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}

// renderField is a field's text form ("" when absent).
func renderField(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil {
		return ""
	}
	return v.String()
}

// renderTrust is trust_score as a float (the ent default when absent or
// not a number).
func renderTrust(r row.Row) float64 {
	v, ok := r.Fields[renderFieldTrustScore]
	if !ok {
		return renderDefaultTrust
	}
	if n, ok := v.(value.Number); ok {
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	if f, err := strconv.ParseFloat(v.String(), 64); err == nil {
		return f
	}
	return renderDefaultTrust
}

// loadPinnedRules returns the must rules of projectID (and global ones).
func loadPinnedRules(ctx context.Context, projectID string, skips *pinSkips) ([]pinnedRule, error) {
	rows, err := renderLiveRows(ctx, schema.TableRules)
	if err != nil {
		return nil, err
	}
	var out []pinnedRule
	for _, r := range rows {
		if renderField(r.Row, renderFieldSeverity) != renderRuleSeverityMust {
			continue
		}
		if !r.inProject(projectID, true) {
			continue
		}
		// 6.12: a superseded rule is replaced by its successor, never pinned.
		if renderField(r.Row, renderFieldSuperseded) != "" {
			skips.SupersededRules++
			continue
		}
		act := renderField(r.Row, renderFieldActivation)
		// 6.3: manual rules load only by lore show / lore context --topic.
		if act == schema.RuleActivationManual {
			skips.ManualRules++
			continue
		}
		out = append(out, pinnedRule{
			ID: r.ID.String(), Severity: renderRuleSeverityMust,
			TrustScore: renderTrust(r.Row), Body: renderField(r.Row, renderFieldBody),
			Summary: renderField(r.Row, schema.FieldSummary), Activation: act,
			Globs: renderField(r.Row, renderFieldGlobs), Updated: renderUpdated(r.Row),
			TagIDs: renderTagIDs(r.Row),
		})
	}
	return out, nil
}

// renderUpdated is the row's last edit time (updated, else created).
func renderUpdated(r row.Row) time.Time {
	if !r.UpdatedAt.IsZero() {
		return r.UpdatedAt
	}
	return r.CreatedAt
}

// renderTagIDs is a row's tag ids as text (nil when absent).
func renderTagIDs(r row.Row) []string {
	l, ok := r.Fields[schema.FieldTagIDs].(value.List)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(l.Items()))
	for _, it := range l.Items() {
		out = append(out, it.String())
	}
	return out
}

// loadPinnedHotfixes returns the critical and high hotfixes of projectID.
func loadPinnedHotfixes(ctx context.Context, projectID string, skips *pinSkips) ([]pinnedHotfix, error) {
	rows, err := renderLiveRows(ctx, schema.TableHotfixes)
	if err != nil {
		return nil, err
	}
	var out []pinnedHotfix
	for _, r := range rows {
		sev := renderField(r.Row, renderFieldSeverity)
		if sev != renderHotfixSeverityCritical && sev != renderHotfixSeverityHigh {
			continue
		}
		if !r.inProject(projectID, false) {
			continue
		}
		if renderField(r.Row, renderFieldSuperseded) != "" {
			skips.SupersededHotfixes++
			continue
		}
		out = append(out, pinnedHotfix{
			ID: r.ID.String(), Severity: sev,
			Title: renderField(r.Row, renderFieldTitle), Body: renderField(r.Row, renderFieldBody),
			Summary: renderField(r.Row, schema.FieldSummary), TagIDs: renderTagIDs(r.Row),
		})
	}
	return out, nil
}
