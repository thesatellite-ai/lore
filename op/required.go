package op

// required.go — "required after lookup" (OV29): an input field that the
// operation needs, but checks itself AFTER its id lookup, so a bad id
// answers E_NOT_FOUND first (the one lookup rule, ops/internal/lookup).
// Such a field is tagged `json:"x,omitempty" required:"after-lookup"`:
//
//	fieldOf ─► markAfterLookup: Required = true, AfterLookup = true
//	  └ docs / OpenAPI / --help show it required (f.Required)
//	checkRequired skips AfterLookup fields ─► the op refuses a missing
//	  value itself, after the lookup ("missing required field <name>")

import (
	"reflect"
	"strings"
)

// tagRequired is the struct tag key of the marker; requiredAfterLookup its
// only value.
const (
	tagRequired         = "required"
	requiredAfterLookup = "after-lookup"
)

// markAfterLookup reads the `required` tag of sf into f. Any other value
// is a programmer error and stops the binary at Define time, like every
// other bad tag.
func markAfterLookup(where string, sf reflect.StructField, f *FieldInfo) {
	v, ok := sf.Tag.Lookup(tagRequired)
	if !ok {
		return
	}
	if v != requiredAfterLookup {
		panic(tagError{where + `: required tag takes only "` + requiredAfterLookup + `"`})
	}
	if f.Type == TypeBool {
		panic(tagError{where + ": a bool field is never required (false is a value)"})
	}
	if !strings.Contains(sf.Tag.Get(tagJSON), ",omitempty") {
		panic(tagError{where + `: required:"after-lookup" goes with omitempty (the decoder must not refuse it before the lookup)`})
	}
	f.Required, f.AfterLookup = true, true
}
