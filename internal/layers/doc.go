// Package layers holds the architecture guard tests of the root module: the
// import-layer rule (a package may import only its own layer or lower ones,
// never the old saas/dbent/lace modules or ent) and the no-`any` rule for
// hand-written code. It has no non-test code; nothing may import it.
package layers
