package lookup_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/profilestore"
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
// of that name); pii_pattern.enable|disable are v0.1.17 spellings moved
// to `lore secrets rule on|off`: they are Local and refuse with
// E_INVALID_INPUT naming the replacement for every input, before anything
// is opened, so the id they are given is never looked up (G-META-5);
// actor.delete looks nothing up: it refuses every call, with or without
// an id, naming `lore actor deactivate` (BATCH2 #8, DELETION 4.1), and it
// runs on the roster's App, not the repo engine (infra.IsRosterOp), so a
// repo lookup would answer for the wrong store; E_NOT_FOUND for an
// unknown id would also let a script read "no such actor" as "an actor
// could be deleted" (G-ORGMIG-5);
// live.show reads one file of the live ref (refs/lore/live), not a store
// row: it needs the host's live session, which this harness does not
// wire, so it answers E_UNSUPPORTED (v0.1.17's "live rows are not
// available here") before any id is read, for every input; where live
// rows apply, any id the live folder lacks is E_NOT_FOUND, pinned over
// the same kinds of ids by wire/host TestLiveShowUnknownIDIsNotFound
// (G-HOST-1);
// none is a row-id lookup.
var notLookups = map[string]bool{
	"commit.show": true, "policy.show": true, "secrets.rule.edit": true,
	"resource.kind.show": true, "resource.kind.edit": true,
	"pii_pattern.enable": true, "pii_pattern.disable": true,
	"actor.delete": true,
	"live.show":    true,
}

// nameOrID are lookup ops whose "id" input also takes a NAME (or a
// unique prefix, or a secret reference), per their own input
// description: a value that is not id-shaped is looked up as that name,
// so a miss is E_NOT_FOUND, not a malformed id (B-ERRS-1 covers values
// that can only be ids). vars (name or var_), meta keys (name or mky_),
// resources and their kinds (name), tags (name), projects (name), actors
// (handle), machines (unique hex prefix of at least 8), time entries
// (unique prefix/suffix), secrets.show (a reference or a row id),
// external sources (a unique name or ext_).
var nameOrID = map[string]bool{
	"vars.show": true, "vars.rm": true,
	"meta.edit": true, "meta.delete": true, "meta.move": true,
	"resource.show": true, "resource.edit": true, "resource.delete": true, "resource.check": true, "resource.archive": true,
	"tag.edit": true, "project.show": true, "project.archive": true, "project.unarchive": true, "project.delete": true,
	"actor.show": true, "machine.show": true,
	"task.time.rm": true, "task.time.edit": true, "secrets.show": true,
	"external_source.enable": true, "external_source.disable": true,
}

// rosterLookups are lookup ops whose rows live only in the org roster
// store (team profiles, TEAM_PROFILES TS-1), mapped to the id prefix
// they take. This harness is a repo with no org, so a well-formed id of
// that prefix answers E_REFUSED (a no-org repo refuses profile writes)
// where a repo lookup answers E_NOT_FOUND; a malformed id or another
// prefix is still E_INVALID_ID before the roster is opened (B-ERRS-1).
// Over a real roster, an unknown or purged contact is E_NOT_FOUND and a
// removed (trashed) one E_IN_TRASH with the restore hint: pinned in
// ops/profile TestContactLookupTrashMatrix and wire
// TestProfileRosterContactInTrash.
var rosterLookups = map[string]ids.Prefix{
	profilestore.OpContactEdit:   ids.PrefixActorContact,
	profilestore.OpContactRemove: ids.PrefixActorContact,
}

// changeFields are the fields an edit is given so that its "nothing to
// change" check (an input error that holds for any id) does not answer
// before the lookup; the first one the op takes is used.
// actor.contact.edit has none of the others: its label is the change.
var changeFields = []string{"title", "body", "description", "text", "name", "to", "label"}

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

// TestLookupIDMatrix pins B-ERRS-1 (owner BATCH2 #11) over every
// registered lookup op: a MALFORMED id (junk text, a bad prefix case, a
// huge value, unicode, and an all-zero id of EVERY table prefix, which is
// no UUIDv7) answers E_INVALID_ID (exit 20), never E_NOT_FOUND; a
// WELL-FORMED unknown id answers E_NOT_FOUND for the op's own prefix (at
// least one prefix per op, every prefix for an op that takes any row)
// and E_INVALID_ID for a prefix the op does not take; no other code.
func TestLookupIDMatrix(t *testing.T) {
	h := opstest.New(t)
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	malformed := []string{"not-an-id", "rul_zz", "RUL_01a1a5aca7f57659be7320c5f4b70011", strings.Repeat("z", 3000), "ünïcode_id"}
	// batches have an id prefix but no table of their own (batch.show)
	wellFormed := []string{"bat_01a1a5aca7f57659be7320c5f4b7f00d"}
	malformed = append(malformed, "bat_"+strings.Repeat("0", 32))
	for _, def := range reg.Tables() {
		if def.Prefix == "" || def.Kind != (schema.TableDef{}).Kind {
			continue
		}
		p := string(def.Prefix)
		malformed = append(malformed, p+"_"+strings.Repeat("0", 32))
		wellFormed = append(wellFormed, p+"_01a1a5aca7f57659be7320c5f4b7f00d")
	}
	n := 0
	for _, e := range h.App.Registry().Entries() {
		if !isLookup(e) {
			continue
		}
		n++
		name := e.Spec().Name
		for _, id := range malformed {
			if nameOrID[name] {
				break
			}
			_, err := h.RunYes(name, inputFor(e, id))
			if !errs.Has(err, errs.InvalidID) {
				t.Errorf("%s malformed %.40q: %v, want E_INVALID_ID", name, id, err)
			}
		}
		found := 0
		for _, id := range wellFormed {
			_, err := h.RunYes(name, inputFor(e, id))
			own, roster := rosterLookups[name]
			switch {
			case roster && strings.HasPrefix(id, string(own)+"_"):
				if !errs.Has(err, errs.Refused) {
					t.Errorf("%s well-formed %.40q: %v, want E_REFUSED (an org-roster row in a no-org repo)", name, id, err)
					continue
				}
				found++
			case roster:
				if !errs.Has(err, errs.InvalidID) {
					t.Errorf("%s well-formed %.40q: %v, want E_INVALID_ID (not a %s_ id)", name, id, err, own)
				}
			case errs.Has(err, errs.NotFound):
				found++
			case errs.Has(err, errs.InvalidID):
			default:
				t.Errorf("%s well-formed %.40q: %v, want E_NOT_FOUND (own prefix) or E_INVALID_ID (another prefix)", name, id, err)
			}
		}
		if found == 0 {
			t.Errorf("%s: no well-formed unknown id answered E_NOT_FOUND (its own prefix must)", name)
		}
	}
	if n < 40 {
		t.Fatalf("only %d lookup ops found: the registry walk is broken", n)
	}
	t.Logf("%d lookup ops × %d malformed + %d well-formed ids", n, len(malformed), len(wellFormed))
}

// TestBadIDMalformedOfTheRightKind (devco:L-9): an id with the expected
// prefix and length whose hex tail lore never makes (all zeros) is named
// as a malformed id of that kind, never "not a msn_ id"; an id of
// another kind keeps the wrong-kind message.
func TestBadIDMalformedOfTheRightKind(t *testing.T) {
	t.Parallel()
	zero := "msn_00000000000000000000000000000000"
	_, cause := ids.ParseAny(zero)
	e := lookup.BadID(`task: --mission "`+zero+`" is not a msn_ id`, cause)
	if !strings.Contains(e.Message, "is a malformed msn_ id") || strings.Contains(e.Message, "is not a msn_ id") {
		t.Errorf("all-zero msn_: %q", e.Message)
	}
	e = lookup.BadID(`task: --mission "`+zero+`" is not an actor id (act_…)`, cause)
	if !strings.Contains(e.Message, "is not an actor id") {
		t.Errorf("another kind: %q", e.Message)
	}
}

// TestOnTableFromThePrefix (devco:J-8): --on-id alone names its table by
// its prefix (mem_ → memories); a given --on-table wins; an id whose prefix
// names no built-in table asks for --on-table.
func TestOnTableFromThePrefix(t *testing.T) {
	t.Parallel()
	tid, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	task := tid.String()
	if got, err := lookup.OnTable("comment add", "", task); err != nil || got != schema.TableMemories {
		t.Errorf("mem_: %q %v", got, err)
	}
	if got, err := lookup.OnTable("comment add", "rules", task); err != nil || got != "rules" {
		t.Errorf("given: %q %v", got, err)
	}
	if _, err := lookup.OnTable("comment add", "", "nope"); !errs.Has(err, errs.InvalidID) {
		t.Errorf("bad id: %v", err)
	}
}
