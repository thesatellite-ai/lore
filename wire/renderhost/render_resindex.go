// render_resindex.go — the "available knowledge" resource index of a
// rendered context file (CONTEXT_BUDGET 6.13, RESOURCES 3.16, RES-OQ3b).
//
//	render sources (repo + shareable stores, or one internal store)
//	  ─▶ kinds (live) + resources (live, not archived), counted per kind
//	  ─▶ one line per kind: "- host: 12 — `lore resource list host`"
//	     a kind with render: summary adds one line per resource (its name)
//	  ─▶ a tail block of the core, kept only when it fits (FA-CTX-7)
//
// Resources are never rendered in full (RES-OQ3); only names and counts.

package renderhost

import (
	"context"
	"fmt"
	"sort"
	"strings"

	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

const (
	// resIndexHead is the block's heading.
	resIndexHead = "## Available knowledge\n\n"
	// kindRenderSummary is the kinds.render value that opts a kind into
	// one line per resource (RES 3.16).
	kindRenderSummary = "summary"
	resFieldKind      = "kind"
	resFieldName      = "name"
	resFieldRender    = "render"
	// metaKeyContextField is meta_keys.context (A-CTX-12).
	metaKeyContextField = "context"
)

// resKindCount is one kind's line in the index.
type resKindCount struct {
	name    string
	summary bool
	names   []string
}

// resourceIndexBlock is the index over sources ("" when no live resource).
func (h *Host) resourceIndexBlock(ctx context.Context, sources []store.Source) (string, error) {
	kinds := map[string]*resKindCount{}
	for _, src := range sources {
		rows, err := h.app.List(ctx, src, schema.TableKinds)
		if err != nil {
			return "", err
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			kinds[r.ID.String()] = &resKindCount{name: renderField(r, resFieldName), summary: renderField(r, resFieldRender) == kindRenderSummary}
		}
	}
	for _, src := range sources {
		rows, err := h.app.List(ctx, src, schema.TableResources)
		if err != nil {
			return "", err
		}
		for _, r := range rows {
			if r.Deleted || renderField(r, renderFieldArchivedAt) != "" {
				continue
			}
			// A resource whose kind is not visible here (another store's
			// kind not rendered) is left out rather than named by id.
			if k, ok := kinds[renderField(r, resFieldKind)]; ok {
				k.names = append(k.names, renderField(r, resFieldName))
			}
		}
	}
	var list []*resKindCount
	for _, k := range kinds {
		if len(k.names) > 0 && k.name != "" {
			list = append(list, k)
		}
	}
	metaLines, err := h.metaIndexLines(ctx, sources)
	if err != nil {
		return "", err
	}
	if len(list) == 0 && len(metaLines) == 0 {
		return "", nil
	}
	// Name order: render must give the same bytes for the same rows (6.6).
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	var b strings.Builder
	b.WriteString(resIndexHead)
	for _, k := range list {
		fmt.Fprintf(&b, "- %s: %d — `lore resource list --kind %s`\n", k.name, len(k.names), k.name)
		if k.summary {
			sort.Strings(k.names)
			for _, n := range k.names {
				fmt.Fprintf(&b, "  - %s\n", oneLine(n))
			}
		}
	}
	for _, l := range metaLines {
		b.WriteString(l)
	}
	return b.String(), nil
}

// metaIndexLines are the index lines of metadata keys marked context:
// index (CONTEXT_BUDGET 6.13, A-CTX-12): one line per key with how many
// live rows of sources carry it, per table, and the lookup command. Keys
// that are internal, retired or not in sources are left out; a key whose
// count is 0 shows no line. Name order (6.6: same bytes for the same rows).
func (h *Host) metaIndexLines(ctx context.Context, sources []store.Source) ([]string, error) {
	type metaIdx struct {
		name    string
		byTable map[string]int
	}
	keys := map[string]*metaIdx{}
	for _, src := range sources {
		rows, err := h.app.List(ctx, src, schema.TableMetaKeys)
		if err != nil {
			return nil, err
		}
		for _, k := range rows {
			if k.Deleted || renderField(k, metaKeyContextField) != opssettings.MetaContextIndex || rowBool(k, metaKeyInternalField) {
				continue
			}
			keys[k.ID.String()] = &metaIdx{name: renderField(k, metaKeyNameField), byTable: map[string]int{}}
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	for _, src := range sources {
		for _, t := range wire.CoreTables() {
			if t == schema.TableMetaKeys || t == schema.TableMetaValues {
				continue
			}
			rows, err := h.app.List(ctx, src, t)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				if r.Deleted || renderField(r, renderFieldArchivedAt) != "" {
					continue
				}
				for kid := range r.Meta {
					if k, ok := keys[kid.String()]; ok {
						k.byTable[string(t)]++
					}
				}
			}
		}
	}
	list := make([]*metaIdx, 0, len(keys))
	for _, k := range keys {
		if len(k.byTable) > 0 && k.name != "" {
			list = append(list, k)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	out := make([]string, 0, len(list))
	for _, k := range list {
		tables := make([]string, 0, len(k.byTable))
		total := 0
		for t, n := range k.byTable {
			tables = append(tables, fmt.Sprintf("%s %d", t, n))
			total += n
		}
		sort.Strings(tables)
		out = append(out, fmt.Sprintf("- metadata %s: %d (%s) — `lore <noun> list --meta %s`\n", k.name, total, strings.Join(tables, ", "), k.name))
	}
	return out, nil
}

// renderTails are the committed file's tail blocks: the pending
// reminders of projectID (D-148), the resource index, then the metadata
// keys block (META 3.14), over the render sources.
func (h *Host) renderTails(ctx context.Context, projectID string) ([]tailBlock, error) {
	rem, err := h.remindersBlock(ctx, projectID)
	if err != nil {
		return nil, err
	}
	srcs, err := h.renderSources(ctx)
	if err != nil {
		return nil, err
	}
	res, err := h.resourceIndexBlock(ctx, srcs)
	if err != nil {
		return nil, err
	}
	keys, err := h.app.MetaKeysBlock(ctx, srcs)
	if err != nil {
		return nil, err
	}
	return []tailBlock{{Name: "reminders", Text: rem}, {Name: "resource_index", Text: res}, {Name: "meta_keys", Text: keys}}, nil
}
