package settings

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Kind names and the REST surface (RESOURCES 6 step 10, G-META-9):
//
//	POST|GET /v1/resources/<kind>   the kind's own routes (surface/rest kindroutes.go)
//	POST     /v1/resources/<verb>   a fixed resource operation (lint, kinds, import, …)
//
// A kind named like a fixed verb would get the same path: the OpenAPI
// document overwrites one with the other and the server routes the
// request to the verb. The reserved names are derived from the registered
// operations, never a hand list, so a new resource verb reserves its name
// the day it is added.

// resourcesRoutePrefix is the collection path of resources ("/v1/resources/").
const resourcesRoutePrefix = op.RESTPrefix + "/resources/"

// reservedKindNames are the one-segment literal paths under
// /v1/resources/ of the settings operations bound to env, sorted.
func reservedKindNames(env Env) []string {
	var out []string
	for _, e := range Ops(env) {
		r, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		seg, isRes := strings.CutPrefix(r.Path, resourcesRoutePrefix)
		if !isRes || seg == "" || strings.ContainsAny(seg, "/{") || slices.Contains(out, seg) {
			continue
		}
		out = append(out, seg)
	}
	slices.Sort(out)
	return out
}

// checkKindRoute refuses a kind name its REST routes would share with a
// fixed resource operation.
func (o ops) checkKindRoute(name string) error {
	if reserved := reservedKindNames(o.env); slices.Contains(reserved, name) {
		return errs.New(errs.InvalidInput, "kind "+quote(name)+" is a resource command name: its REST route "+resourcesRoutePrefix+name+" would collide with lore resource "+name).
			WithHint("pick another name (for example " + name + "_item); reserved: " + strings.Join(reserved, ", "))
	}
	return nil
}
