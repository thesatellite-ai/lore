package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTeamVersionsNameHookOnlyAgent: a detected agent whose only save on
// this machine is its session row (`lore agent start`, a machine-local row:
// no actors row is written into any store, P3-LOCAL-1) was the one writer
// doctor showed as a raw act_ id. It is named "<person> via <tool>" for
// both key widths of the collision rule once a sessions row names the
// tool; an agent of a tool no session names, or of another person, keeps
// the id; the person is named by the identity when no store holds a row.
func TestTeamVersionsNameHookOnlyAgent(t *testing.T) {
	t.Parallel()
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	ctx := background()
	person, err := identity.ActorID(testEmail)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := identity.DetectedAgentIDs(person, "claude_code")
	if err != nil || len(agents) != 2 || agents[0] == agents[1] {
		t.Fatalf("agent ids %v %v", agents, err)
	}
	other, err := identity.ActorID("human:someone-else@example.com")
	if err != nil {
		t.Fatal(err)
	}
	strangers, err := identity.DetectedAgentIDs(other, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	otherTool, err := identity.DetectedAgentIDs(person, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.teamActorNamer(ctx)(agents[0].String()); got != agents[0].String() {
		t.Fatalf("no session yet: %q, want the id", got)
	}
	if _, err := a.FindOrCreateSession(ctx, "prj_a", SessionDetails{ActorID: agents[0].String(), Tool: "claude_code", ToolSessionID: "placeholder-session"}); err != nil {
		t.Fatal(err)
	}
	name := a.teamActorNamer(ctx)
	for in, want := range map[string]string{
		agents[0].String():    "alice via claude_code",
		agents[1].String():    "alice via claude_code",
		person.String():       "alice",
		strangers[0].String(): strangers[0].String(),
		otherTool[0].String(): otherTool[0].String(),
	} {
		if got := name(in); got != want {
			t.Errorf("name(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTeamMachineNamer: a machine shows as its label, else its host name;
// this machine is named from its own profile when no store holds its row;
// an id nothing names shows as mch_ + 8 hex.
func TestTeamMachineNamer(t *testing.T) {
	t.Parallel()
	text := func(s string) value.Value {
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		fields map[string]value.Value
		want   string
	}{
		{map[string]value.Value{schema.FieldMachineLabel: text("work laptop"), machineFieldHostname: text("host-a")}, "work laptop"},
		{map[string]value.Value{machineFieldHostname: text("host-a")}, "host-a"},
		{map[string]value.Value{}, ""},
	} {
		if got := machineDisplayName(row.Row{Fields: c.fields}); got != c.want {
			t.Errorf("machineDisplayName(%v) = %q, want %q", c.fields, got, c.want)
		}
	}
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	ctx := background()
	me, err := a.ThisMachine(ctx)
	if err != nil {
		t.Fatalf("this machine: %v", err)
	}
	want := machineDisplayName(row.Row{Fields: me.Fields})
	if want == "" {
		t.Fatal("this machine's profile has no host name")
	}
	name := a.teamMachineNamer(ctx)
	if got := name(me.ID.String()); got != want {
		t.Errorf("this machine = %q, want %q", got, want)
	}
	unknown := "mch_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got := name(unknown); got != "mch_01234567" {
		t.Errorf("unknown machine = %q, want mch_01234567", got)
	}
}

// TestTeamVersionsNameUnscopedIdentity (I-3): a row a setup step saved
// outside every setup's folders carries the identity this machine resolves
// there (no org, no setup), which no store holds an actors row for; doctor
// names it "<name> (<step>, outside any setup)" instead of a raw act_ id.
func TestTeamVersionsNameUnscopedIdentity(t *testing.T) {
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	ctx := background()
	t.Setenv(identity.EnvActor, "human:hostuser@box.example")
	id, err := identity.ActorID("human:hostuser@box.example")
	if err != nil {
		t.Fatal(err)
	}
	got := a.teamActorNamer(ctx)(id.String())
	if got == id.String() || !strings.Contains(got, "hostuser") || !strings.HasSuffix(got, unscopedNote+")") {
		t.Fatalf("name = %q, want the unscoped identity named", got)
	}
}
