//go:build stories && !windows

package stories

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// wRuleActorID is devco:D-620's rule for the W and WW matrices: a human
// (non-JSON) output names people, never by their raw act_… id. Every text
// renderer resolves an actor id to its display name and falls back to the
// id only when no store knows it; the matrices' admin is on the roster, so
// an act_ id in their text is a renderer that skipped the lookup (owner use
// pass 2 weak 9: trash list printed "by act_…").
const wRuleActorID = "f-actor-id"

// wRawActor is an actor id as lore mints it.
var wRawActor = regexp.MustCompile(`act_[0-9a-f]{8,}`)

// wActorIDOK are the places an actor id is the point of the text: a
// command hint's named flag (--actor act_…, --id act_…), an "id:" or
// "actor id" field, a JSON value, a row path (actors/act_…).
var wActorIDOK = regexp.MustCompile(`(--[a-z][a-z-]*[ =]|\bid:\s*|\bactor id\s*|"\s*:\s*"|/)$`)

// wActorIDOps are the commands whose subject is the actor itself: the id
// is printed as the row's own id beside the name (the roster, an actor's
// card, whoami's identity line, a raw row dump), never in place of a name.
var wActorIDOps = map[string]string{
	"actor list":     "roster: id beside the name",
	"actor show":     "the actor's card: its id heads it",
	"member list":    "roster: id beside the name",
	"member show":    "a member's card: its id beside the name",
	"whoami":         "the identity line: name, key and id",
	"whoami show":    "the identity line: name, key and id",
	"sync peek":      "a raw row dump of a ref: table and row id",
	"actor rename":   "names the actor it renamed by id and name",
	"actor register": "names the actor it registered by id and name",
	"actor refresh":  "names the actor it stamped by id and source",
	"migrate run":    "lists the rows it moved by table and row id",
}

// wKnownActors is the act_ ids a roster at the place names (the output of
// `actor list --json` there): an id no store knows may be shown as the id
// (the documented fallback), so the rule flags only these. nil (the list
// was refused there) flags every id.
func wKnownActors(listJSON string) map[string]bool {
	found := wRawActor.FindAllString(listJSON, -1)
	if len(found) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, id := range found {
		known[id] = true
	}
	return known
}

// wTableHeader is a list's header line: upper-case column names.
var wTableHeader = regexp.MustCompile(`^\s*[A-Z][A-Z_]+(\s{2,}[A-Z][A-Z_ ]*)+\s*$`)

// wRawActorIDs is each act_ id of a known actor (known; nil = every id)
// in a human output that is not in an explicit id place (wActorIDOK, or a
// list's ID column), one line each. A --json run, an output that is a
// JSON document, or an op about the actor itself (wActorIDOps) has none.
func wRawActorIDs(args []string, out string, known map[string]bool) []string {
	if len(args) >= 2 && wActorIDOps[args[0]+" "+args[1]] != "" || len(args) == 1 && wActorIDOps[args[0]] != "" {
		return nil
	}
	if slices.Contains(args, "--json") || strings.HasPrefix(strings.TrimSpace(out), "{") || strings.HasPrefix(strings.TrimSpace(out), "[") {
		return nil
	}
	var bad []string
	idCol := -1 // the ID column's offset in the last header seen
	for _, line := range strings.Split(out, "\n") {
		if wTableHeader.MatchString(line) {
			idCol = -1
			for _, f := range []string{" ID ", " ID\n"} {
				if i := strings.Index(" "+line+"\n", f); i >= 0 {
					idCol = i
				}
			}
			continue
		}
		for _, m := range wRawActor.FindAllStringIndex(line, -1) {
			if wActorIDOK.MatchString(line[:m[0]]) || m[0] == idCol || known != nil && !known[line[m[0]:m[1]]] {
				continue
			}
			bad = append(bad, strings.TrimSpace(line))
			break
		}
	}
	return bad
}

// TestStoryWRawActorIDs is the rule's own check: the renderers' shapes it
// passes and the one it catches (devco:D-620).
func TestStoryWRawActorIDs(t *testing.T) {
	id := "act_0123456789abcdef0123456789abcdef"
	for _, c := range []struct {
		out  string
		args []string
		bad  bool
	}{
		{out: "trsh_1  memory  2026-11-18  by " + id + "\n", bad: true},
		{out: "tsk_1 claimed by " + id + ", lease expired 3 min ago\n", bad: true},
		{out: "trsh_1  memory  2026-11-18  by Ada Admin\n"},
		{out: "hint: lore actor show --id " + id + "\n"},
		{out: "  id:       " + id + "\n"},
		{out: "ID                                    NAME\n" + id + "  Ada Admin\n"},
		{out: "{\"by\": \"" + id + "\"}\n"},
		{out: "2026-01-05T09:00:00Z  actors.write  actors/" + id + "  by ada\n"},
		{out: id + "  ada@example.test  Ada Admin\n", args: []string{"member", "list"}},
		{out: "by " + id + "\n", args: []string{"trash", "list", "--json"}},
		{out: "tsk_1 was already done by " + id + "\n", args: []string{"task", "done"}, bad: true},
	} {
		if got := wRawActorIDs(c.args, c.out, nil); (len(got) > 0) != c.bad {
			t.Errorf("wRawActorIDs(%q) = %q, want bad=%v", c.out, got, c.bad)
		}
	}
	// an id no roster here knows may stay the id (the fallback)
	other := "act_fedcba9876543210fedcba9876543210"
	if got := wRawActorIDs(nil, "  "+other+" on laptop: lore dev\n", map[string]bool{id: true}); len(got) > 0 {
		t.Errorf("an unknown writer's id is the documented fallback, got %q", got)
	}
}
