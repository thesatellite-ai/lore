package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// Impact and graph (RESOURCES 3.21): one generic walk over every ref field
// of every kind in the view, no hand-written field list.
const (
	OpResGraph  = "resource.graph"
	OpResImpact = "resource.impact"
)

// Graph walk limits (3.21 rule 3: default 3, at most 10).
const (
	defaultGraphDepth = 3
	maxGraphDepth     = 10
)

// GraphIn is the input of resource.graph.
type GraphIn struct {
	Name       string   `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	Down       bool     `json:"down,omitempty" flag:"down" desc:"follow refs forward: what it depends on (the default)"`
	Up         bool     `json:"up,omitempty" flag:"up" desc:"follow refs backward: what depends on it"`
	Depth      int      `json:"depth,omitempty" flag:"depth" desc:"hops to follow (default 3, at most 10)"`
	Fields     []string `json:"fields,omitempty" flag:"fields" desc:"only these ref fields (repeatable or comma separated)"`
	NoArchived bool     `json:"no_archived,omitempty" flag:"no-archived" desc:"hide archived resources"`
}

// ImpactIn is the input of resource.impact (graph --up with links).
type ImpactIn struct {
	Name       string   `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	Depth      int      `json:"depth,omitempty" flag:"depth" desc:"hops to follow (default 3, at most 10)"`
	Fields     []string `json:"fields,omitempty" flag:"fields" desc:"only these ref fields (repeatable or comma separated)"`
	NoArchived bool     `json:"no_archived,omitempty" flag:"no-archived" desc:"hide archived resources"`
	AllLinks   bool     `json:"all_links,omitempty" flag:"all-links" desc:"also list done tasks and resolved incidents"`
}

// GraphVia is one edge that reached a node.
type GraphVia struct {
	Field string `json:"field"`
	To    string `json:"to"`
}

// GraphNode is one resource reached by the walk, at its smallest hop count.
type GraphNode struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Name     string     `json:"name"`
	Hops     int        `json:"hops"`
	Via      []GraphVia `json:"via"`
	Archived bool       `json:"archived,omitempty"`
}

// GraphLink is an open task or incident linked to a node (3.21 rule 7).
type GraphLink struct {
	Row      string `json:"row"`
	Table    string `json:"table"`
	Relation string `json:"relation"`
	Node     string `json:"node"`
}

// GraphOut is the output of resource.graph and resource.impact. Tree is
// the printed tree (graph) or list (impact); sensitive values never
// appear, nodes print kind and name only (rule 9).
type GraphOut struct {
	Root  string      `json:"root"`
	Depth int         `json:"depth"`
	Nodes []GraphNode `json:"nodes"`
	Links []GraphLink `json:"links,omitempty"`
	Tree  string      `json:"-"`
}

// Text implements op.Texter.
func (o GraphOut) Text() string { return o.Tree }

func (o ops) graphOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpResGraph, Summary: "Print the ref tree of a resource (--down: what it uses; --up: what uses it)", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors, Examples: []op.Example{{Title: "what a service depends on", CLI: "lore resource graph hub_api --down", Want: "runs_on"}}}, o.resGraph),
		op.Define(op.Spec{Name: OpResImpact, Summary: "What breaks if a resource goes down: every resource that depends on it, with open linked tasks and incidents", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.resImpact),
	}
}

// resNode is one resource of the edge index.
type resNode struct {
	l    located
	kind string
}

// resEdge is one ref value: src holds field pointing at dst.
type resEdge struct {
	src, dst, field string
}

// resIndex is every live resource and every ref edge of the view.
type resIndex struct {
	nodes    map[string]resNode
	out, in  map[string][]resEdge
	kindByID map[string]string
}

// buildIndex reads every resource and decodes its ref fields (rule 2:
// every ref field of every kind counts; fields narrows to some names).
func (o ops) buildIndex(ctx context.Context, fields []string) (resIndex, error) {
	idx := resIndex{nodes: map[string]resNode{}, out: map[string][]resEdge{}, in: map[string][]resEdge{}, kindByID: map[string]string{}}
	kinds, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableKinds)
	if err != nil {
		return idx, err
	}
	refField := map[string]string{} // fld_ id → name, ref fields only
	for _, k := range kinds {
		idx.kindByID[k.Row.ID.String()] = text(k.Row, kName)
		fs, ferr := o.fieldsOf(ctx, k)
		if ferr != nil {
			return idx, ferr
		}
		for _, f := range fs {
			n := text(f.Row, kName)
			if text(f.Row, fType) == string(value.KindRef) && (len(fields) == 0 || slices.Contains(fields, n)) {
				refField[f.Row.ID.String()] = n
			}
		}
	}
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return idx, err
	}
	for _, r := range rows {
		id := r.Row.ID.String()
		idx.nodes[id] = resNode{l: r, kind: idx.kindByID[text(r.Row, rKind)]}
		stored, derr := decodeValues(r)
		if derr != nil {
			return idx, derr
		}
		for fid, raw := range stored.vals {
			name, isRef := refField[fid]
			if !isRef {
				continue
			}
			v := fromJSONRaw(raw)
			if v == nil {
				continue
			}
			e := resEdge{src: id, dst: display(v), field: name}
			idx.out[id] = append(idx.out[id], e)
			idx.in[e.dst] = append(idx.in[e.dst], e)
		}
	}
	for _, m := range []map[string][]resEdge{idx.out, idx.in} {
		for k := range m {
			slices.SortFunc(m[k], func(a, b resEdge) int {
				return strings.Compare(a.field+idx.label(a.src)+idx.label(a.dst), b.field+idx.label(b.src)+idx.label(b.dst))
			})
		}
	}
	return idx, nil
}

// label is "<kind> <name>" of a node, or "? <id> (not in this view)".
func (idx resIndex) label(id string) string {
	n, ok := idx.nodes[id]
	if !ok {
		return "? " + id + " (not in this view)"
	}
	return n.kind + " " + text(n.l.Row, rName)
}

// walkOpts are the walk choices shared by graph and impact.
type walkOpts struct {
	up, noArchived bool
	depth          int
}

// walk does a breadth-first pass for the node list (smallest hops, every
// via) and a depth-first pass for the tree, cutting cycles with the
// current path and repeats with a seen set (rules 3-6).
func (idx resIndex) walk(root string, w walkOpts) ([]GraphNode, string) {
	edges := func(id string) []resEdge {
		if w.up {
			return idx.in[id]
		}
		return idx.out[id]
	}
	other := func(e resEdge) string {
		if w.up {
			return e.src
		}
		return e.dst
	}
	archived := func(id string) bool { n, ok := idx.nodes[id]; return ok && text(n.l.Row, rArchivedAt) != "" }
	hops := map[string]int{root: 0}
	byID := map[string]*GraphNode{}
	var order []string
	frontier := []string{root}
	for h := 1; h <= w.depth && len(frontier) > 0; h++ {
		var next []string
		for _, id := range frontier {
			if id != root && archived(id) {
				continue // rule 5: archived nodes are not expanded
			}
			for _, e := range edges(id) {
				to := other(e)
				if w.noArchived && archived(to) {
					continue
				}
				if _, seen := hops[to]; !seen {
					hops[to] = h
					n := &GraphNode{ID: to, Hops: h, Archived: archived(to)}
					if nd, ok := idx.nodes[to]; ok {
						n.Kind, n.Name = nd.kind, text(nd.l.Row, rName)
					}
					byID[to] = n
					order = append(order, to)
					if _, ok := idx.nodes[to]; ok {
						next = append(next, to)
					}
				}
				if n := byID[to]; n != nil && hops[to] == h {
					n.Via = append(n.Via, GraphVia{Field: e.field, To: id})
				}
			}
		}
		frontier = next
	}
	nodes := make([]GraphNode, 0, len(order))
	for _, id := range order {
		nodes = append(nodes, *byID[id])
	}
	var b strings.Builder
	b.WriteString(idx.label(root) + "\n")
	printed := map[string]bool{root: true}
	var tree func(id, prefix string, depth int, path map[string]bool)
	tree = func(id, prefix string, depth int, path map[string]bool) {
		es := edges(id)
		var keep []resEdge
		for _, e := range es {
			if !w.noArchived || !archived(other(e)) {
				keep = append(keep, e)
			}
		}
		for i, e := range keep {
			to := other(e)
			branch, indent := "├── ", "│   "
			if i == len(keep)-1 {
				branch, indent = "└── ", "    "
			}
			arrow := e.field + "  "
			if w.up {
				arrow = e.field + " ← "
			}
			line := prefix + branch + arrow + idx.label(to)
			_, known := idx.nodes[to]
			switch {
			case path[to]:
				b.WriteString(line + "   (cycle)\n")
				continue
			case printed[to]:
				b.WriteString(line + "   (seen above)\n")
				continue
			case archived(to):
				b.WriteString(prefix + branch + "(archived) " + arrow + idx.label(to) + "\n")
				printed[to] = true
				continue
			}
			b.WriteString(line + "\n")
			printed[to] = true
			if known && depth < w.depth {
				path[to] = true
				tree(to, prefix+indent, depth+1, path)
				delete(path, to)
			}
		}
	}
	tree(root, "", 1, map[string]bool{root: true})
	return nodes, b.String()
}

func graphDepth(d int) (int, error) {
	switch {
	case d == 0:
		return defaultGraphDepth, nil
	case d < 0 || d > maxGraphDepth:
		return 0, errs.New(errs.InvalidInput, fmt.Sprintf("--depth %d: give 1 to %d", d, maxGraphDepth))
	}
	return d, nil
}

// splitFields accepts --fields a,b and repeated --fields.
func splitFields(fs []string) []string {
	var out []string
	for _, f := range fs {
		out = append(out, splitList(f)...)
	}
	return out
}

// resGraph: resolve the root → build the edge index of the view → walk
// down (default) or up → print the tree (RESOURCES 3.21).
func (o ops) resGraph(ctx context.Context, in GraphIn) (GraphOut, error) {
	if in.Up && in.Down {
		return GraphOut{}, errs.New(errs.InvalidInput, "resource graph: give --down or --up, not both")
	}
	depth, err := graphDepth(in.Depth)
	if err != nil {
		return GraphOut{}, err
	}
	root, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return GraphOut{}, err
	}
	idx, err := o.buildIndex(ctx, splitFields(in.Fields))
	if err != nil {
		return GraphOut{}, err
	}
	nodes, tree := idx.walk(root.Row.ID.String(), walkOpts{up: in.Up, noArchived: in.NoArchived, depth: depth})
	return GraphOut{Root: root.Row.ID.String(), Depth: depth, Nodes: nodes, Tree: tree}, nil
}

// resImpact: graph --up as a list by hops, then the open tasks and
// unresolved incidents linked to the root or any node (rule 7).
func (o ops) resImpact(ctx context.Context, in ImpactIn) (GraphOut, error) {
	depth, err := graphDepth(in.Depth)
	if err != nil {
		return GraphOut{}, err
	}
	root, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return GraphOut{}, err
	}
	idx, err := o.buildIndex(ctx, splitFields(in.Fields))
	if err != nil {
		return GraphOut{}, err
	}
	rootID := root.Row.ID.String()
	nodes, _ := idx.walk(rootID, walkOpts{up: true, noArchived: in.NoArchived, depth: depth})
	links, err := o.openLinks(ctx, append([]string{rootID}, nodeIDs(nodes)...), in.AllLinks)
	if err != nil {
		return GraphOut{}, err
	}
	var b strings.Builder
	b.WriteString("what breaks if " + idx.label(rootID) + " goes down\n")
	for _, n := range nodes {
		vias := make([]string, 0, len(n.Via))
		for _, v := range n.Via {
			vias = append(vias, v.Field+" → "+text(idx.nodes[v.To].l.Row, rName))
		}
		mark := ""
		if n.Archived {
			mark = "  (archived)"
		}
		fmt.Fprintf(&b, "  %d  %-10s %-20s %s%s\n", n.Hops, n.Kind, n.Name, strings.Join(vias, ", "), mark)
	}
	if len(nodes) == 0 {
		b.WriteString("  nothing depends on it\n")
	}
	if len(links) > 0 {
		parts := make([]string, 0, len(links))
		for _, l := range links {
			parts = append(parts, fmt.Sprintf("%s %s (%s %s)", strings.TrimSuffix(l.Table, "s"), l.Row, l.Relation, text(idx.nodes[l.Node].l.Row, rName)))
		}
		b.WriteString("  open  " + strings.Join(parts, ", ") + "\n")
	}
	return GraphOut{Root: rootID, Depth: depth, Nodes: nodes, Links: links, Tree: b.String()}, nil
}

func nodeIDs(ns []GraphNode) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.ID)
	}
	return out
}

// openLinks are the knowledge_refs between a node and a task or incident;
// done or cancelled tasks and resolved incidents only with all.
func (o ops) openLinks(ctx context.Context, nodes []string, all bool) ([]GraphLink, error) {
	refs, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableKnowledgeRefs)
	if err != nil {
		return nil, err
	}
	open := map[string]bool{}
	for _, t := range []struct {
		table  row.Table
		isOpen func(located) bool
	}{
		{schema.TableTasks, func(l located) bool {
			s := text(l.Row, "status")
			return s != schema.TaskStatusDone && s != schema.TaskStatusCancelled
		}},
		{schema.TableIncidents, func(l located) bool { return text(l.Row, "resolved_at") == "" }},
	} {
		rows, lerr := liveRows(ctx, o.env, o.env.Stores(), t.table)
		if lerr != nil {
			return nil, lerr
		}
		for _, r := range rows {
			open[r.Row.ID.String()] = all || t.isOpen(r)
		}
	}
	var out []GraphLink
	for _, r := range refs {
		src, dst := text(r.Row, "src_id"), text(r.Row, "dst_id")
		for _, side := range []struct{ node, row, table string }{{dst, src, text(r.Row, "src_table")}, {src, dst, text(r.Row, "dst_table")}} {
			if slices.Contains(nodes, side.node) && open[side.row] {
				out = append(out, GraphLink{Row: side.row, Table: side.table, Relation: text(r.Row, "relation"), Node: side.node})
			}
		}
	}
	slices.SortFunc(out, func(a, b GraphLink) int { return strings.Compare(a.Row, b.Row) })
	return out, nil
}
