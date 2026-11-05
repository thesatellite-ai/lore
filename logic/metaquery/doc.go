// Package metaquery is the search query parser of METADATA 3.17 (step 2):
// lore splits a search into metadata conditions and free text before
// FTS5 sees it. Pure (layer L1): the caller says which names are keys the
// place can see.
//
//	Parse(q, isKey)
//	  └ split on spaces (quotes kept together)
//	      ├ name?            isKey(name)      → Has
//	      ├ name:v / name=v  isKey(top name)  → Cond (":" and "=" are one; "deploy.region:x" = key deploy, path region)
//	      ├ name>v >= < <= != ~=  isKey       → Cmp (Op set; OV39-CMP)
//	      └ anything else                     → Text
//	FTS(terms) → the FTS5 text: a word holding - . or : becomes one phrase,
//	             a trailing * moves outside the quotes ("HUB-41"* finds HUB-412)
//
// Not built (0.1.18): scope prefixes
// (repo:<name>:key:v), json paths ($.), "did you mean" hints.
package metaquery
