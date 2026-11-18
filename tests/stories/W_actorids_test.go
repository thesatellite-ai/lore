//go:build stories && !windows

package stories

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/model/ids"
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
// "actor id" field, a JSON value, a row path (actors/act_…), or the id in
// parentheses right after the name ("ada via claude_code (act_…)": doctor's
// actor_roster finding names the id readers will see beside the name).
var wActorIDOK = regexp.MustCompile(`(--[a-z][a-z-]*[ =]|\bid:\s*|\bactor id\s*|"\s*:\s*"|/|[^\s(,:]\s\()$`)

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
// `actor list --json` there) and the detected-agent ids of each of them in
// every tool lore detects: an agent's id derives from its person and tool,
// so lore can name it "<person> via <tool>" before (or without) its own
// actors row reaching this machine (devco D-652/D-661: doctor's Team
// versions printed such an id, and the matrices let it pass as an unknown
// writer). An id no store knows may be shown as the id (the documented
// fallback), so the rule flags only these. nil (the list was refused
// there) flags every id.
func wKnownActors(listJSON string) map[string]bool {
	found := wRawActor.FindAllString(listJSON, -1)
	if len(found) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, id := range found {
		known[id] = true
		for _, a := range wAgentsOf(id) {
			known[a] = true
		}
	}
	return known
}

// wAgentsOf is every detected-agent id of the actor id, in every tool
// agentdetect knows (both key widths); none when id is not an actor id.
func wAgentsOf(id string) []string {
	owner, err := ids.Parse[ids.Actor](id)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range agentdetect.Builtin() {
		agents, aerr := identity.DetectedAgentIDs(owner, d.Tool)
		if aerr != nil {
			continue
		}
		for _, a := range agents {
			out = append(out, a.String())
		}
	}
	return out
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
		{out: "- actor_roster: store acme holds rows saved by ada via claude_code (" + id + "), an actor its roster does not hold\n"},
	} {
		if got := wRawActorIDs(c.args, c.out, nil); (len(got) > 0) != c.bad {
			t.Errorf("wRawActorIDs(%q) = %q, want bad=%v", c.out, got, c.bad)
		}
	}
	// devco:D-661: a detected agent of a known person is known too (its id
	// derives from the person and the tool), so doctor's Team versions
	// printing it raw fails the rule, as D-652's did
	person, err := identity.ActorID("human:" + wAdmin.Email)
	if err != nil {
		t.Fatal(err)
	}
	agents := wAgentsOf(person.String())
	if len(agents) == 0 {
		t.Fatal("no detected-agent ids derive from a known actor id")
	}
	if got := wRawActorIDs([]string{"doctor"}, "Team versions (newest save per writer and machine):\n  "+agents[0]+" on laptop: lore dev (2026-11-18)\n", wKnownActors(`{"id": "`+person.String()+`"}`)); len(got) == 0 {
		t.Errorf("a known person's agent id in doctor output passed the rule")
	}
	// an id no roster here knows may stay the id (the fallback)
	other := "act_fedcba9876543210fedcba9876543210"
	if got := wRawActorIDs(nil, "  "+other+" on laptop: lore dev\n", map[string]bool{id: true}); len(got) > 0 {
		t.Errorf("an unknown writer's id is the documented fallback, got %q", got)
	}
}
