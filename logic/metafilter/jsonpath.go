package metafilter

import (
	"net/netip"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
)

// JSON paths and ranges in list filters (METADATA 3.4 "Path grammar",
// 3.6 '<<'; OV14, P2-META-13):
//
//	key$.items[*].sku=AB-1 ─► parsePath("$.items[*].sku") ─► steps
//	  match: lookup(v) ─► walk(v, steps) ─► every leaf the path reaches
//	         ─► the term's compare over those leaves ([*] = any element)
//	egress_ip<<10.0.0.0/8 ─► netip prefix; an ip leaf inside it, or a
//	         cidr leaf whose range lies inside it, matches

// pathStep is one step of a json path: a field, a list position, or
// every element ([*]).
type pathStep struct {
	field string
	index int // -1: not a position
	all   bool
}

// parsePath reads "$.a.b[0][*]" and "$.\"ship-to\".city".
func parsePath(p string) ([]pathStep, error) {
	bad := func(why string) error {
		return errs.New(errs.MetaOp, "meta: json path "+strconv.Quote(p)+": "+why).
			WithHint("$.field, $[n], $[*], $.\"odd-name\"")
	}
	rest, ok := strings.CutPrefix(p, "$")
	if !ok {
		return nil, bad("starts with $")
	}
	var out []pathStep
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, ".\""):
			end := strings.Index(rest[2:], "\"")
			if end < 0 {
				return nil, bad("an unclosed quoted field")
			}
			out = append(out, pathStep{field: rest[2 : 2+end], index: -1})
			rest = rest[3+end:]
		case strings.HasPrefix(rest, "."):
			n := strings.IndexAny(rest[1:], ".[")
			if n < 0 {
				n = len(rest) - 1
			}
			f := rest[1 : 1+n]
			if f == "" {
				return nil, bad("an empty field")
			}
			if strings.ContainsAny(f, "]\"* ") {
				// a bare field holds no brackets, quotes, stars or spaces;
				// odd names are quoted ($."odd-name")
				return nil, bad("field " + strconv.Quote(f) + " needs quotes")
			}
			out = append(out, pathStep{field: f, index: -1})
			rest = rest[1+n:]
		case strings.HasPrefix(rest, "["):
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, bad("an unclosed [")
			}
			in := rest[1:end]
			if in == "*" {
				out = append(out, pathStep{all: true, index: -1})
			} else {
				i, err := strconv.Atoi(in)
				if err != nil || i < 0 {
					return nil, bad("[" + in + "] is not a position")
				}
				out = append(out, pathStep{index: i})
			}
			rest = rest[end+1:]
		default:
			return nil, bad("unexpected " + strconv.Quote(rest))
		}
	}
	return out, nil
}

// walk returns every value the steps reach in raw.
func walk(raw []byte, steps []pathStep) [][]byte {
	cur := [][]byte{raw}
	for _, s := range steps {
		var next [][]byte
		for _, v := range cur {
			switch {
			case s.field != "":
				if m, ok := member(v, s.field); ok {
					next = append(next, m)
				}
			case s.all:
				if els, err := canon.Elements(v); err == nil {
					next = append(next, els...)
				}
			case s.index >= 0:
				if els, err := canon.Elements(v); err == nil && s.index < len(els) {
					next = append(next, els[s.index])
				}
			}
		}
		cur = next
	}
	return cur
}

// inRange reports whether a leaf (an ip, or a cidr) lies inside bound.
func inRange(leaf string, bound netip.Prefix) bool {
	if a, err := netip.ParseAddr(leaf); err == nil {
		return bound.Contains(a)
	}
	if p, err := netip.ParsePrefix(leaf); err == nil {
		return p.Bits() >= bound.Bits() && bound.Contains(p.Addr())
	}
	return false
}
