package wire

// doctor_kindroutes.go — the doctor warning for a resource kind named
// like a fixed resource command (G-META-9): its REST route
// /v1/resources/<name> is the command's, so the kind's own POST/GET routes
// never run and the OpenAPI path is overwritten. kind add and kind rename
// refuse such names since 14892e4c; a kind from an earlier build or a hand
// edit can still carry one, and resource lint reports it too.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// DoctorCheckKindRoute is the check name (the same word as resource lint's
// finding).
const DoctorCheckKindRoute DoctorCheck = "kind_route_collision"

// kindRoutePrefix is the resources collection path a kind's routes share.
const kindRoutePrefix = op.RESTPrefix + "/resources/"

// kindFieldName is the kinds table's name field.
const kindFieldName = "name"

// DoctorKindRoutes warns once per live kind, in any attached store, whose
// name is a one-segment literal route under /v1/resources/ of a registered
// operation (derived from the registry, never a hand list).
func (a *App) DoctorKindRoutes(ctx context.Context) ([]DoctorFinding, error) {
	var reserved []string
	for _, e := range a.Registry().Entries() {
		r, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		if seg, isRes := strings.CutPrefix(r.Path, kindRoutePrefix); isRes && seg != "" && !strings.ContainsAny(seg, "/{") {
			reserved = append(reserved, seg)
		}
	}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		rows, err := a.List(ctx, src, schema.TableKinds)
		if err != nil {
			return nil, err
		}
		type hit struct{ name, id string }
		var hits []hit
		for _, r := range rows {
			if v := r.Fields[kindFieldName]; v != nil && slices.Contains(reserved, v.String()) {
				hits = append(hits, hit{name: v.String(), id: r.ID.String()})
			}
		}
		// one stable line order per store: by name, then id
		slices.SortFunc(hits, func(x, y hit) int { return strings.Compare(x.name+" "+x.id, y.name+" "+y.id) })
		for _, h := range hits {
			n := h.name
			out = append(out, DoctorFinding{Check: DoctorCheckKindRoute, Level: DoctorWarning, Store: src,
				Message: "kind " + n + " (" + h.id + ") has the name of a resource command, so " + kindRoutePrefix + n + " runs lore resource " + n + " instead of the kind's routes",
				Fix:     "lore resource kind rename --id " + n + " --new-name <new_name>"})
		}
	}
	return out, nil
}
