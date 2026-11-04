package lookup_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// lookupVerbs are the operation verbs that address an EXISTING row by an
// "id" input (show, edit, archive, delete, …). Ops not ending in one of
// these (add, list, search, apply, …) are not lookups.
var lookupVerbs = []string{
	"show", "edit", "archive", "unarchive", "delete", "remove", "done", "start", "cancel",
	"pause", "resume", "invalidate", "ack", "enable", "disable", "move", "check", "rm",
}

// notLookups are ops whose "id" input is not a row id: commit.show takes a
// git sha;
// policy.show's id is a policy key, resource kinds are addressed by name
// with their own E_KIND_UNKNOWN, and secrets.rule.edit takes a scanner
// rule id (a name such as aws-access-key, overriding the built-in rule
// of that name); none is a row-id lookup.
var notLookups = map[string]bool{
	"commit.show": true, "policy.show": true, "secrets.rule.edit": true,
	"resource.kind.show": true, "resource.kind.edit": true,
}

// changeFields are the fields an edit is given so that its "nothing to
// change" check (an input error that holds for any id) does not answer
// before the lookup; the first one the op takes is used.
var changeFields = []string{"title", "body", "description", "text", "name", "to"}

// inputFor is the JSON input of a lookup op for id.
func inputFor(e op.Entry, id string) string {
	in := `{"id":` + opstest.Quote(id)
	for _, want := range changeFields {
		for _, f := range e.InputFields() {
			if f.Name == want {
				val := "x"
				if want == "to" {
					val = "repo"
				}
				return in + `,"` + want + `":"` + val + `"}`
			}
		}
	}
	return in + `}`
}

// isLookup reports whether e is an id-addressed lookup op.
func isLookup(e op.Entry) bool {
	name := e.Spec().Name
	if notLookups[name] {
		return false
	}
	verb := name[strings.LastIndex(name, ".")+1:]
	ok := false
	for _, v := range lookupVerbs {
		ok = ok || verb == v
	}
	if !ok {
		return false
	}
	for _, f := range e.InputFields() {
		if f.Name == "id" {
			return true
		}
	}
	return false
}

// TestLookupNotFoundMatrix is the coordinator's parity decision as a
// matrix over every registered op × every kind of id that names no row:
// malformed text, a huge value, and for EVERY table prefix an all-zero id
// and a well-formed unknown id (so each op sees its own prefix and every
// wrong one). Each answers E_NOT_FOUND (exit 27), as v0.1.17 did; never
// E_INVALID_ID, which is kept for ids given to a NEW row.
func TestLookupNotFoundMatrix(t *testing.T) {
	h := opstest.New(t)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{"not-an-id", "rul_zz", "RUL_01a1a5aca7f57659be7320c5f4b70011", strings.Repeat("z", 3000), "ünïcode_id"}
	for _, def := range reg.Tables() {
		if def.Prefix == "" || def.Kind != (schema.TableDef{}).Kind {
			continue
		}
		p := string(def.Prefix)
		bad = append(bad, p+"_"+strings.Repeat("0", 32), p+"_01a1a5aca7f57659be7320c5f4b7f00d")
	}
	n := 0
	for _, e := range h.App.Registry().Entries() {
		if !isLookup(e) {
			continue
		}
		n++
		name := e.Spec().Name
		for _, id := range bad {
			in := inputFor(e, id)
			_, err := h.RunYes(name, in)
			if err == nil {
				t.Errorf("%s %.40q: no error", name, id)
				continue
			}
			if !errs.Has(err, errs.NotFound) {
				t.Errorf("%s %.40q: %v, want E_NOT_FOUND", name, id, err)
			}
		}
	}
	if n < 40 {
		t.Fatalf("only %d lookup ops found: the registry walk is broken", n)
	}
	t.Logf("%d lookup ops × %d ids", n, len(bad))
}
