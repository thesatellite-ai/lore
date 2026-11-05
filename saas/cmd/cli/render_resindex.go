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
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
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
)

// resKindCount is one kind's line in the index.
type resKindCount struct {
	name    string
	summary bool
	names   []string
}

// resourceIndexBlock is the index over sources ("" when no live resource).
func resourceIndexBlock(ctx context.Context, sources []store.Source) (string, error) {
	kinds := map[string]*resKindCount{}
	for _, src := range sources {
		rows, err := coreApp.List(ctx, src, schema.TableKinds)
		if err != nil {
			return "", coreErr(err)
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			kinds[r.ID.String()] = &resKindCount{name: renderField(r, resFieldName), summary: renderField(r, resFieldRender) == kindRenderSummary}
		}
	}
	for _, src := range sources {
		rows, err := coreApp.List(ctx, src, schema.TableResources)
		if err != nil {
			return "", coreErr(err)
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
	if len(list) == 0 {
		return "", nil
	}
	// Name order: render must give the same bytes for the same rows (6.6).
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	var b strings.Builder
	b.WriteString(resIndexHead)
	for _, k := range list {
		fmt.Fprintf(&b, "- %s: %d — `lore resource list %s`\n", k.name, len(k.names), k.name)
		if k.summary {
			sort.Strings(k.names)
			for _, n := range k.names {
				fmt.Fprintf(&b, "  - %s\n", oneLine(n))
			}
		}
	}
	return b.String(), nil
}

// renderTails are the committed file's tail blocks: the resource index,
// then the metadata keys block (META 3.14), over the render sources.
func renderTails(ctx context.Context) ([]tailBlock, error) {
	srcs, err := renderSources(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resourceIndexBlock(ctx, srcs)
	if err != nil {
		return nil, err
	}
	keys, err := coreApp.MetaKeysBlock(ctx, srcs)
	if err != nil {
		return nil, coreErr(err)
	}
	return []tailBlock{{Name: "resource_index", Text: res}, {Name: "meta_keys", Text: keys}}, nil
}
