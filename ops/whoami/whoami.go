// Package whoami defines `lore whoami` (layer L5, CLI_REFERENCE 3.8,
// LORE_TEAM_SPEC F10, F18, AGENT_IDENTITY 2, 9): the resolved org and what
// decided it, your identity, the tool and machine lines, the repo's role,
// the org's stores and the personal folders that load here; `--explain`
// adds every org rule with hit or miss, and `--machine-label` sets this
// machine's synced label instead.
//
//	whoami.show ─▶ --machine-label given ─▶ Host.SetMachineLabel ─▶ the label line
//	            └▶ otherwise             ─▶ Host.Whoami ─▶ the lines / the v0.1.17 JSON envelope
//
// PASS3 ONE-PATH: an operation (CLI, `lore api`, docs from one
// definition), overriding CLI_REFERENCE 3.8's "old-binary command". The
// org resolution, the identity chain and the redaction rules are the
// host's (P3-ORG-G-3). It is Local: it reads the repo itself and must work
// where opening the stores fails (it is how a person finds out why).
package whoami

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation name (`lore whoami`, a bare-noun shortcut of show).
const (
	Noun   = "whoami"
	OpShow = "whoami.show"
	// ShowVerb is the generated verb the bare `lore whoami` runs.
	ShowVerb = "show"
)

// restNoun keeps the REST collection "whoami".
const restNoun = "whoami"

// Kinds of the JSON envelope (v0.1.17 printJSON kind).
const (
	KindWhoami       = "whoami"
	KindMachineLabel = "machine.label"
	// SchemaVersion is the envelope's schema_version (R25).
	SchemaVersion = 1
)

// Label is the --machine-label value. Set tells "given as empty" (clear
// the label) from "not given": the surfaces decode a flag or JSON member
// only when it is present, through UnmarshalText.
type Label struct {
	Set   bool
	Value string
}

// UnmarshalText implements encoding.TextUnmarshaler: present means set.
func (l *Label) UnmarshalText(b []byte) error {
	l.Set, l.Value = true, string(b)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (l Label) MarshalText() ([]byte, error) { return []byte(l.Value), nil }

// ShowIn is the input of whoami.show.
type ShowIn struct {
	Explain      bool  `json:"explain,omitempty" flag:"explain" desc:"print every org rule with hit or miss and why"`
	Share        bool  `json:"share,omitempty" flag:"share" desc:"on a terminal, also print store URLs and internal folders (hidden without a TTY or with CI set)"`
	MachineLabel Label `json:"machine_label,omitempty,omitzero" flag:"machine-label" desc:"set this machine's label, synced with its profile to our own stores (\"\" clears)"`
}

// Identity is the identity block.
type Identity struct {
	StableKey string `json:"stable_key"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	ActorID   string `json:"actor_id,omitempty"`
	Source    string `json:"source"`
	Session   string `json:"session,omitempty"`
}

// Store is one of the org's stores (v0.1.17 JSON keys: lorecfg.Store had
// yaml tags only).
type Store struct {
	URL       string `json:"URL"`
	Branch    string `json:"Branch"`
	OrgFolder string `json:"OrgFolder"`
}

// Rule is one org rule's trace entry (v0.1.17 orgresolve.RuleTrace keys).
type Rule struct {
	Step    int    `json:"Step"`
	Outcome string `json:"Outcome"`
	Reason  string `json:"Reason"`
}

// Agent is what agent detection saw (R11); the tool session id is
// "<redacted>" unless stdout is a terminal and CI is unset (AID 9).
type Agent struct {
	Tool          string            `json:"tool,omitempty"`
	ToolSession   string            `json:"tool_session_id,omitempty"`
	Version       string            `json:"tool_version,omitempty"`
	Model         string            `json:"model,omitempty"`
	Signal        string            `json:"signal,omitempty"`
	Outer         []string          `json:"outer_tools,omitempty"`
	CI            string            `json:"ci,omitempty"`
	DeclaredHuman bool              `json:"declared_human,omitempty"`
	Flags         []string          `json:"flags,omitempty"`
	Sources       map[string]string `json:"sources,omitempty"`
}

// Data is the whoami envelope's data (v0.1.17 whoamiJSON).
type Data struct {
	Org       string   `json:"org"`
	MatchedBy string   `json:"matched_by"`
	Rule      int      `json:"rule"`
	RepoRole  string   `json:"repo_role"`
	Identity  Identity `json:"identity"`
	ProjectID string   `json:"project_id,omitempty"`
	Remotes   []string `json:"remotes"`
	Stores    []Store  `json:"stores"`
	// Personal is the personal store `me` as one place (G-DECIDE-7,
	// STORE TARGETS: no per-company or everywhere/ split): its url, or the
	// v0.1.17 local folder; "" when none is set.
	Personal   string   `json:"personal_store"`
	LinkStatus string   `json:"link_status"`
	Trace      []Rule   `json:"trace"`
	Warnings   []string `json:"warnings"`
	Error      string   `json:"error,omitempty"`
	Machine    string   `json:"machine,omitempty"`
	Agent      Agent    `json:"agent,omitzero"`
	// IdentityContact is the identity-contact line (TEAM_PROFILES 6.3):
	// "<contact> is an identity contact of <name> (act_…)" when the
	// caller's key resolves to another actor through a contact; "" else.
	IdentityContact string `json:"identity_contact,omitempty"`
	// Setup is the active setup (ONBOARDING_DESIGN 3.3); snake_case keys
	// unlike the v0.1.17 ones above (10.11). Absent when no setup exists
	// on this machine.
	Setup Setup `json:"setup,omitzero"`
}

// Setup is the whoami setup block (ONBOARDING 3.3, 10.11).
type Setup struct {
	// Name is the active setup ("" = none).
	Name string `json:"name"`
	// Why is the 3.3 step that chose it (flag, env, repo, folder, org,
	// default, none).
	Why string `json:"why"`
	// Detail is the glob, env value, repo id or org that matched.
	Detail string `json:"detail,omitempty"`
	// Kind and Org are the setup's (solo has no org).
	Kind string `json:"kind,omitempty"`
	Org  string `json:"org,omitempty"`
	// Steps are every 3.3 step with its outcome (--explain only).
	Steps []SetupStep `json:"steps,omitempty"`
	// Error is the refusal (E_SETUP_AMBIGUOUS, E_SETUP_NOT_FOUND) text.
	Error string `json:"error,omitempty"`
}

// SetupStep is one 3.3 step in `whoami --explain`.
type SetupStep struct {
	Step    string `json:"step"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

// MachineLabel is the --machine-label result (ops/infra machine.label).
type MachineLabel struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Store   string `json:"store"`
	Changed bool   `json:"changed"`
}

// Out is the output of whoami.show: the v0.1.17 JSON envelope. P3-ORG-G-4:
// with --machine-label the envelope's kind is machine.label and the
// result is under label (v0.1.17 printed machine.label's bare JSON).
type Out struct {
	SchemaVersion int          `json:"schema_version"`
	Kind          string       `json:"kind"`
	Count         int          `json:"count,omitempty"`
	Data          Data         `json:"data,omitzero"`
	Label         MachineLabel `json:"label,omitzero"`
	// Lines is the human output, built by the host.
	Lines []string `json:"-"`
	// Fail is the error whoami fails with AFTER its output is printed (an
	// org refusal or an identity error: whoami is the diagnostic for
	// both, v0.1.17); nil = success. Data.Error carries its text.
	Fail error `json:"-"`
}

// Failure implements op.Failer.
func (o Out) Failure() error { return o.Fail }

// Text implements op.Texter.
func (o Out) Text() string {
	if len(o.Lines) == 0 {
		return ""
	}
	return strings.Join(o.Lines, "\n") + "\n"
}

// Host is the machinery behind whoami, supplied by the binary.
type Host interface {
	// Whoami resolves and describes this folder; a resolution or identity
	// error is returned as the error (exit status kept, P3-ORG-G-3).
	Whoami(ctx context.Context, in ShowIn) (Out, error)
	// SetMachineLabel sets ("" clears) this machine's synced label.
	SetMachineLabel(ctx context.Context, label string) (MachineLabel, []string, error)
}

// Env is what whoami needs; *wire.App implements it.
type Env interface {
	// WhoamiHost is the binary's Host; ok is false on a binary without one.
	WhoamiHost(ctx context.Context) (h Host, ok bool)
}

// Ops returns the whoami operation bound to env.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show the resolved org, what decided it, your identity and what loads here",
			Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{errs.InvalidInput, errs.NotAStore, errs.ConfigInvalid, errs.OrgAmbiguous, errs.OrgDisagrees,
				errs.IdentityFailed, errs.StoreReadOnly, errs.Refused, errs.Unsupported, errs.Internal},
			Examples: []op.Example{
				{Title: "", CLI: "lore whoami", Want: "identity"},
				{Title: "every org rule with hit or miss", CLI: "lore whoami --explain", Want: "identity"},
				{Title: "as if the org were agency-stag", CLI: "LORE_ORG=agency-stag lore whoami", Want: "identity"},
				{Title: "", CLI: "lore whoami --json", Want: "identity"},
				{Title: `synced label of this machine ("" clears)`, CLI: `lore whoami --machine-label "tomas desktop"`, Want: "machine"},
			},
		}, func(ctx context.Context, in ShowIn) (Out, error) {
			h, ok := env.WhoamiHost(ctx)
			if !ok || h == nil {
				return Out{}, errs.New(errs.Unsupported, "lore whoami needs the org resolution of the release binary, which this binary does not have yet").
					WithHint("run it with the release binary")
			}
			return Show(ctx, h, in)
		}),
	}
}

// Show is whoami.show over h.
func Show(ctx context.Context, h Host, in ShowIn) (Out, error) {
	if in.MachineLabel.Set {
		if in.Explain || in.Share {
			return Out{}, errs.New(errs.InvalidInput, "--machine-label sets the label; run --explain or --share on their own")
		}
		l, lines, err := h.SetMachineLabel(ctx, in.MachineLabel.Value)
		if err != nil {
			return Out{}, err
		}
		return Out{SchemaVersion: SchemaVersion, Kind: KindMachineLabel, Count: 1, Label: l, Lines: lines}, nil
	}
	out, err := h.Whoami(ctx, in)
	if err != nil {
		return Out{}, err
	}
	out.SchemaVersion, out.Kind = SchemaVersion, KindWhoami
	return out, nil
}
