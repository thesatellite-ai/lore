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
// Expansion (SECRETS_AND_REFERENCES 12, 13; SQ11, SQ18, SQ23, D23):
//
//	pinned rows ─▶ expandPinned ─▶ refpoint Render point (Client in a client repo)
//	                 var: shareable and not personal; res/row: home is a render source
//	                 secret: as written (ours) | ${secret:redacted} (client); env never
//	               ─▶ scanner on the expanded text: a new hit refuses the render
//
// Every committed output (LORE.md, LORE.full.md, folder files) is built
// from the expanded rows, so one pass covers them all; row files keep the
// references (SQ3).
//
// The project filter applies to repo-store rows only: a shareable store's
// rows carry its own folder's project id, and attaching the store is what
// puts them in scope.

package renderhost

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opspin "github.com/thesatellite-ai/lore/ops/pin"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"github.com/thesatellite-ai/lore/model/errs"
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
	// Pin / PinnedAt: `lore pin --context full|line` (FA-CTX-6).
	Pin      contextfit.Form
	PinnedAt time.Time
	// ScopeID / OverridesID: the folder scope and the wider rule this one
	// replaces there (6.2, render_scopes.go); repo-store rows only.
	ScopeID     string
	OverridesID string
	// Dir is the folder of the rule's dir: scope when the rule is printed
	// outside that folder's own file (the root LORE.md or a parent
	// folder's file: no file of its own, folder missing, too few entries),
	// so the reader still sees where it applies (planDirs; "" otherwise).
	Dir string
	// Origin is where the row came from (render_stamp.go): its store, id
	// and HLC, the input the files' stamps hash.
	Origin string
}

// pinnedHotfix is one hotfix as the render prints it.
type pinnedHotfix struct {
	ID       string
	Severity string
	Title    string
	Body     string
	Summary  string
	TagIDs   []string
	// Pin: a full pin puts a high hotfix in full (a critical one is always
	// full and a line pin does not shorten it, CQ3; FA-CTX-6).
	Pin contextfit.Form
	// ScopeID is the folder scope (repo-store rows only, 6.2).
	ScopeID string
	// Dir is the folder of the dir: scope when printed outside its own
	// folder file (pinnedRule.Dir).
	Dir string
	// Origin is where the row came from (render_stamp.go).
	Origin string
}

// pinSkips counts rows that would have been pinned before 0.1.18 but are
// left out of the core, by reason (the render report's "moved out" rows).
type pinSkips struct {
	SupersededRules, SupersededHotfixes int
	ManualRules                         int
	// PathScoped counts glob rules moved to path files (6.7).
	PathScoped int
	// IDs are the skipped rows with their reason, in load order; the
	// render history's cut_summary keeps them for `why-context --cut`
	// (CONTEXT_BUDGET 6.11).
	IDs []reportEntry
}

// renderSources are the stores whose rows may reach the committed render:
// the repo store first, then every attached store marked shareable.
func (h *Host) renderSources(ctx context.Context) ([]store.Source, error) {
	shared, err := h.app.ShareableStores(ctx)
	if err != nil {
		return nil, err
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
func (h *Host) renderLiveRows(ctx context.Context, table row.Table) ([]renderRow, error) {
	if h.app == nil {
		return nil, errs.New(errs.Internal, "the core is not mounted")
	}
	srcs, err := h.renderSources(ctx)
	if err != nil {
		return nil, err
	}
	var out []renderRow
	for _, src := range srcs {
		rows, err := h.app.List(ctx, src, table)
		if err != nil {
			return nil, err
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
func (h *Host) loadPinnedRules(ctx context.Context, projectID string, skips *pinSkips) ([]pinnedRule, error) {
	rows, err := h.renderLiveRows(ctx, schema.TableRules)
	if err != nil {
		return nil, err
	}
	var out []pinnedRule
	mk := newRenderMetaKeys(h.app) // MQ13: metadata values on rendered rows
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
			skips.IDs = append(skips.IDs, reportEntry{ID: r.ID.String(), Reason: contextfit.ReasonSuperseded})
			continue
		}
		act := renderField(r.Row, renderFieldActivation)
		// 6.3: manual rules load only by lore show / lore context --topic.
		if act == schema.RuleActivationManual {
			skips.ManualRules++
			skips.IDs = append(skips.IDs, reportEntry{ID: r.ID.String(), Reason: contextfit.ReasonManual})
			continue
		}
		out = append(out, pinnedRule{
			ID: r.ID.String(), Severity: renderRuleSeverityMust,
			TrustScore: renderTrust(r.Row), Body: mk.withMetaLine(ctx, renderField(r.Row, renderFieldBody), r.Row, false),
			Summary: renderField(r.Row, schema.FieldSummary), Activation: act,
			Globs: renderField(r.Row, renderFieldGlobs), Updated: renderUpdated(r.Row),
			TagIDs: renderTagIDs(r.Row), ScopeID: repoScope(r, schema.FieldScope), OverridesID: repoScope(r, schema.FieldOverrides),
			Pin: renderPin(r.Row), PinnedAt: renderPinnedAt(r.Row), Origin: r.origin(),
		})
	}
	return out, nil
}

// renderPin is the row's context pin form ("" none or unknown).
func renderPin(r row.Row) contextfit.Form {
	switch f := contextfit.Form(renderField(r, opspin.FieldPin)); f {
	case contextfit.FormFull, contextfit.FormLine:
		return f
	}
	return ""
}

// renderPinnedAt is when the pin was set (zero when none).
func renderPinnedAt(r row.Row) time.Time {
	if dt, ok := r.Fields[opspin.FieldPinnedAt].(value.DateTime); ok {
		return dt.Time()
	}
	return time.Time{}
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
func (h *Host) loadPinnedHotfixes(ctx context.Context, projectID string, skips *pinSkips) ([]pinnedHotfix, error) {
	rows, err := h.renderLiveRows(ctx, schema.TableHotfixes)
	if err != nil {
		return nil, err
	}
	var out []pinnedHotfix
	mk := newRenderMetaKeys(h.app) // MQ13: metadata values on rendered rows
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
			skips.IDs = append(skips.IDs, reportEntry{ID: r.ID.String(), Reason: contextfit.ReasonSuperseded})
			continue
		}
		out = append(out, pinnedHotfix{
			ID: r.ID.String(), Severity: sev,
			Title: renderField(r.Row, renderFieldTitle), Body: mk.withMetaLine(ctx, renderField(r.Row, renderFieldBody), r.Row, false),
			Summary: renderField(r.Row, schema.FieldSummary), TagIDs: renderTagIDs(r.Row), ScopeID: repoScope(r, schema.FieldScope),
			Pin: renderPin(r.Row), Origin: r.origin(),
		})
	}
	return out, nil
}

// repoScope is a scope field of a repo-store row ("" for other stores:
// folder files hold the repo's own rows, CFG6).
func repoScope(r renderRow, field string) string {
	if r.source != store.SourceRepo {
		return ""
	}
	return renderField(r.Row, field)
}

// renderVarShareable is the vars field that lets a committed render fill
// the variable in (BATCH 1).
const renderVarShareable = "shareable"

// expandPinned fills the references of the pinned rows' text fields for
// the committed render, or refuses when an expanded value brings a secret
// in (13.2, D23).
func (h *Host) expandPinned(ctx context.Context, rules []pinnedRule, hotfixes []pinnedHotfix) ([]pinnedRule, []pinnedHotfix, error) {
	opt, err := h.renderExpandOptions(ctx)
	if err != nil {
		return nil, nil, err
	}
	fill := func(id, field string, s *string) error {
		if *s == "" || len(refs.References(*s)) == 0 {
			return nil
		}
		res, err := refpoint.Expand(opt, id+" "+field, *s)
		if err != nil {
			return err
		}
		*s = res.Text
		return nil
	}
	for i := range rules {
		r := &rules[i]
		for _, f := range []struct {
			name string
			s    *string
		}{{renderFieldBody, &r.Body}, {schema.FieldSummary, &r.Summary}} {
			if err := fill(r.ID, f.name, f.s); err != nil {
				return nil, nil, err
			}
		}
	}
	for i := range hotfixes {
		h := &hotfixes[i]
		for _, f := range []struct {
			name string
			s    *string
		}{{renderFieldTitle, &h.Title}, {renderFieldBody, &h.Body}, {schema.FieldSummary, &h.Summary}} {
			if err := fill(h.ID, f.name, f.s); err != nil {
				return nil, nil, err
			}
		}
	}
	return rules, hotfixes, nil
}

// renderExpandOptions builds the render point: Client when the repo store
// is client-class (13.1), the resolver and scanner from the App, and
// Locate placing a value's row against the render sources.
func (h *Host) renderExpandOptions(ctx context.Context) (refpoint.Options, error) {
	opt := refpoint.Options{Point: refpoint.Render}
	if h.app == nil {
		return opt, errs.New(errs.Internal, "the core is not mounted")
	}
	repoClass, err := h.app.StoreClass(ctx, store.SourceRepo)
	if err != nil {
		return opt, err
	}
	if repoClass == direction.Client {
		opt.Point = refpoint.Client
	}
	if opt.Resolver, err = h.app.RefResolver(ctx); err != nil { // wire/expandport.go
		return opt, err
	}
	if opt.Scanner, err = h.app.OutputScanner(ctx); err != nil {
		return opt, err
	}
	srcs, err := h.renderSources(ctx)
	if err != nil {
		return opt, err
	}
	reach := map[store.Source]bool{}
	for _, s := range srcs {
		// a client render shows only the client repo's own rows (13.1)
		reach[s] = opt.Point == refpoint.Render || s == store.SourceRepo
	}
	opt.Locate = func(id ids.AnyID) refpoint.Target {
		src, r, err := h.app.Get(ctx, engine.Ref{ID: id})
		if err != nil || r.Deleted {
			return refpoint.Target{} // unplaced: kept as written
		}
		class, err := h.app.StoreClass(ctx, src)
		b, _ := r.Fields[renderVarShareable].(value.Bool)
		return refpoint.Target{
			Personal:  err != nil || class == direction.Personal,
			Shareable: b.Bool(),
			Reaches:   reach[src],
		}
	}
	return opt, nil
}
