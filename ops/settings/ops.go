package settings

import "github.com/thesatellite-ai/lore/op"

// ops binds the run functions to one Env.
type ops struct{ env Env }

// Ops returns every settings operation bound to env, for op.NewRegistry.
// Order matters for the CLI: a leaf that is also a group (`secrets allow`
// and `secrets allow rm`) must be registered before its children.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	var out []op.Entry
	for _, group := range [][]op.Entry{o.kindOps(), o.resourceOps(), o.varOps(), o.policyOps(), o.metaOps(), o.secretOps()} {
		out = append(out, group...)
	}
	return out
}
