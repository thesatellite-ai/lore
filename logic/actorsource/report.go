package actorsource

import "strings"

// IsIdentityOrigin reports whether an origin_store names an identity
// (setup:<name>, org:<org>, identity:<step>: each has a colon) rather than
// a roster store (a sto_ id, or a store name for a store with no id).
func IsIdentityOrigin(origin string) bool { return strings.Contains(origin, ":") }

// Report is one actor's source and every copy this machine can read.
type Report struct {
	// ID is the actor id (act_…).
	ID     string
	Source Source
	Copies []Copy
}

// Shown is the value a merged read shows (ShownOf).
func (r Report) Shown() Shown { return ShownOf(r.Source, r.Copies) }

// Counts tallies the copies' states (CountsOf).
func (r Report) Counts() Counts { return CountsOf(r.Source, r.Copies) }

// Stale lists the copies a refresh rewrites now (NeedsRefresh), in order.
func (r Report) Stale() []Copy {
	var out []Copy
	for _, c := range r.Copies {
		if NeedsRefresh(r.Source, c) {
			out = append(out, c)
		}
	}
	return out
}

// Settle completes a source the host could not find from the copies'
// stamps: when no source is known but a copy names a roster store (an
// origin_store that is no identity origin, IsIdentityOrigin), the actor
// has a roster this
// machine cannot read, so its source is known and unreachable, named by
// that origin. Any other source is returned as it is.
func Settle(src Source, copies []Copy) Source {
	if src.Known {
		return src
	}
	for _, c := range copies {
		if c.OriginStore != "" && !IsIdentityOrigin(c.OriginStore) {
			return Source{Known: true, Origin: c.OriginStore, Where: c.OriginStore}
		}
	}
	return src
}
