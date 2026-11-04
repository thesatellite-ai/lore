package infra

// Machine profile operations (AGENT_IDENTITY_DESIGN 6.8, MACHINE PROFILE,
// MACHINE LABEL, CFG11). Flow:
//
//	machine.list  → ListAll(machines) in every attached store → table
//	machine.show  → unique prefix (≥ 8 hex) of the full id → one profile
//	machine.label → Machines.ThisMachine (profile read now)
//	              → Machines.MachineTarget (never a code or client repo, Q-R3-9)
//	              → create the row (deterministic id) or patch it only
//	                where a field changed ("rewritten only when a field changes")

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Machine noun and operations.
const (
	NounMachine    = "machine"
	OpMachineList  = "machine.list"
	OpMachineShow  = "machine.show"
	OpMachineLabel = "machine.label"
	KindMachines   = "machine.list"
	KindMachine    = "machine.show"
	// minMachinePrefix is the shortest id prefix commands accept (PROV4).
	minMachinePrefix = len(ids.MachinePrefix) + ids.MachineShortHex
)

// ThisMachine is this machine's id and freshly read profile fields.
type ThisMachine struct {
	// RowID is the machines row id (deterministic from the full id).
	RowID ids.AnyID
	// ID is the full machine id.
	ID ids.MachineID
	// Fields are the profile columns (everything but the label).
	Fields map[string]value.Value
}

// Machines is the Env upgrade the label verb needs (wire.App).
type Machines interface {
	ThisMachine(ctx context.Context) (ThisMachine, error)
	// MachineTarget picks the store for this machine's profile: to when
	// named, else the first attached store that is neither the code repo
	// nor client-readable; E_REFUSED when there is none (Q-R3-9).
	MachineTarget(ctx context.Context, to string) (store.Source, error)
	CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (engine.Written, error)
}

// MachineListIn is the input of machine.list.
type MachineListIn struct {
	Store string `json:"store,omitempty" flag:"store" desc:"only machines of this store (default every attached store)"`
}

// MachineShowIn is the input of machine.show.
type MachineShowIn struct {
	ID string `json:"id" arg:"0" desc:"a machine id or a unique prefix of at least 8 hex characters (mch_0cd630f0)"`
}

// MachineLabelIn is the input of machine.label.
type MachineLabelIn struct {
	Label string `json:"label" arg:"0" desc:"the label; \"\" clears it (one line, no control characters)"`
	To    string `json:"to,omitempty" flag:"to" desc:"the store holding the profile (default the first own store)"`
}

// Machine is one profile in --json form.
type Machine struct {
	ID        string          `json:"id"`
	RowID     string          `json:"row_id"`
	IDSource  string          `json:"id_source,omitempty"`
	Label     string          `json:"label,omitempty"`
	Hostname  string          `json:"hostname,omitempty"`
	OS        json.RawMessage `json:"os,omitempty"`
	Kernel    json.RawMessage `json:"kernel,omitempty"`
	Libc      json.RawMessage `json:"libc,omitempty"`
	Arch      string          `json:"arch,omitempty"`
	CPU       json.RawMessage `json:"cpu,omitempty"`
	RAMMB     int64           `json:"ram_mb,omitempty"`
	OSUser    string          `json:"os_user,omitempty"`
	Runtime   json.RawMessage `json:"runtime,omitempty"`
	UpdatedAt string          `json:"updated_at,omitempty"`
	Store     store.SourceRef `json:"_source,omitempty,omitzero"`
}

// MachineListOut is the list envelope.
type MachineListOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count"`
	Data          []Machine `json:"data"`
}

// MachineShowOut is the show envelope.
type MachineShowOut struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Data          Machine `json:"data"`
}

// MachineLabelOut reports the label write.
type MachineLabelOut struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Store   string `json:"store"`
	Changed bool   `json:"changed"`
}

// Text is the one-line result.
func (o MachineLabelOut) Text() string {
	short := shortMachine(o.ID)
	switch {
	case !o.Changed:
		return work.Success + " machine " + short + " unchanged\n"
	case o.Label == "":
		return work.Success + " machine " + short + " label cleared\n"
	}
	return work.Success + " machine " + short + " labelled " + work.Quote(o.Label) + "\n"
}

// shortMachine is mch_ + 8 hex (the display form, PROV4).
func shortMachine(full string) string {
	if len(full) > minMachinePrefix {
		return full[:minMachinePrefix]
	}
	return full
}

// MachineOps returns the machine operations; machine.label only when env
// offers the Machines upgrade.
func MachineOps(env Env) []op.Entry {
	o := ops{env: env}
	out := []op.Entry{
		op.Define(op.Spec{Name: OpMachineList, Summary: "List the machine profiles of the attached stores", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore machine list --json", Want: `"data"`}}}, o.machineList),
		op.Define(op.Spec{Name: OpMachineShow, Summary: "Show one machine profile in full", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors}, o.machineShow),
	}
	if m, ok := upgrade.As[Machines](env); ok {
		out = append(out, op.Define(op.Spec{Name: OpMachineLabel, Summary: "Set (or clear with \"\") this machine's synced label and refresh its profile",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append([]errs.Code{errs.Refused}, writeErrors...)},
			func(ctx context.Context, in MachineLabelIn) (MachineLabelOut, error) {
				return machineLabel(ctx, env, m, in)
			}))
	}
	return out
}

// Text is the `lore machines` table (6.8).
func (o MachineListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no machines)\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tLABEL\tHOST\tOS\tKERNEL\tLIBC\tARCH\tCPU (CORES)\tRAM\tRUNTIME\tLAST WRITE")
	for _, m := range o.Data {
		cpu := nv(m.CPU, "model", "")
		if c := nv(m.CPU, "cores", ""); c != "" {
			cpu = strings.TrimSpace(cpu + " (" + c + ")")
		}
		ram := "-"
		if m.RAMMB > 0 {
			ram = fmt.Sprintf("%d GB", (m.RAMMB+512)/1024)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", shortMachine(m.ID), dash(m.Label), dash(m.Hostname),
			dash(nv(m.OS, "name", "version")), dash(nv(m.Kernel, "name", "version")), dash(nv(m.Libc, "name", "version")), dash(m.Arch),
			dash(cpu), ram, dash(runtimeText(m.Runtime)), dash(dateOnly(m.UpdatedAt)))
	}
	_ = tw.Flush()
	return b.String()
}

// Text is the full profile.
func (o MachineShowOut) Text() string {
	m := o.Data
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", m.ID)
	for _, kv := range [][2]string{{"label", m.Label}, {"id_source", m.IDSource}, {"host", m.Hostname}, {"os", nv(m.OS, "name", "version")},
		{"kernel", nv(m.Kernel, "name", "version")}, {"libc", nv(m.Libc, "name", "version")}, {"arch", m.Arch},
		{"cpu", nv(m.CPU, "model", "")}, {"cores", nv(m.CPU, "cores", "")}, {"ram_mb", fmt.Sprint(m.RAMMB)}, {"os_user", m.OSUser},
		{"runtime", runtimeText(m.Runtime)}, {"updated", m.UpdatedAt}} {
		fmt.Fprintf(&b, "  %-10s %s\n", kv[0]+":", dash(kv[1]))
	}
	return b.String()
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func dateOnly(s string) string {
	if len(s) >= len("2006-01-02") {
		return s[:len("2006-01-02")]
	}
	return s
}

// nv joins two keys of a small JSON object ("ubuntu 24.04").
func nv(raw json.RawMessage, a, b string) string {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{a, b} {
		if k == "" {
			continue
		}
		if v := scalar(m[k]); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

// scalar is a JSON string or number as text.
func scalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// runtimeText is "bare", "ci github_actions acme/x", "aws_lambda f r".
func runtimeText(raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return ""
	}
	parts := []string{scalar(m["type"])}
	for _, k := range []string{"provider", "repo", "function", "region", "service", "job", "pod", "namespace", "name", "distro", "container", "inside"} {
		if v := scalar(m[k]); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

// machineOf converts a machines row.
func machineOf(lab work.Labeler, s work.Sourced) Machine {
	r := s.Row
	raw := func(f string) json.RawMessage {
		if v, ok := r.Fields[f].(value.JSON); ok {
			return json.RawMessage(v.Raw())
		}
		if t := work.Field(r, f); strings.HasPrefix(t, "{") {
			return json.RawMessage(t)
		}
		return nil
	}
	return Machine{ID: work.Field(r, schema.FieldMachineID), RowID: r.ID.String(), IDSource: work.Field(r, "id_source"),
		Label: work.Field(r, schema.FieldMachineLabel), Hostname: work.Field(r, "hostname"), OS: raw("os"), Kernel: raw("kernel"),
		Libc: raw("libc"), Arch: work.Field(r, "arch"), CPU: raw("cpu"), RAMMB: work.Int(r, "ram_mb"), OSUser: work.Field(r, "os_user"),
		Runtime: raw("runtime"), UpdatedAt: timeOf(r, false), Store: sourceOf(lab, s.Source)}
}

// machineList lists every live profile once (same id in several stores:
// the first store's), oldest first.
func (o ops) machineList(ctx context.Context, in MachineListIn) (MachineListOut, error) {
	rows, err := work.ListAll(ctx, o.env, schema.TableMachines, in.Store)
	if err != nil {
		return MachineListOut{}, err
	}
	out := MachineListOut{SchemaVersion: SchemaVersion, Kind: KindMachines, Data: []Machine{}}
	seen := map[string]bool{}
	lab := work.LabelerOf(o.env)
	for _, s := range sortByCreated(rows) {
		if seen[s.Row.ID.String()] {
			continue
		}
		seen[s.Row.ID.String()] = true
		out.Data = append(out.Data, machineOf(lab, s))
	}
	out.Count = len(out.Data)
	return out, nil
}

// machineShow finds one profile by a unique prefix of its full id.
func (o ops) machineShow(ctx context.Context, in MachineShowIn) (MachineShowOut, error) {
	q := strings.TrimSpace(in.ID)
	if !strings.HasPrefix(q, ids.MachinePrefix) || len(q) < minMachinePrefix {
		return MachineShowOut{}, errs.New(errs.InvalidInput, "machine: "+work.Quote(q)+" is not a machine id: give mch_ and at least 8 hex characters").
			WithHint("run `lore machine list` to see the ids")
	}
	list, err := o.machineList(ctx, MachineListIn{})
	if err != nil {
		return MachineShowOut{}, err
	}
	var hits []Machine
	for _, m := range list.Data {
		if strings.HasPrefix(m.ID, q) {
			hits = append(hits, m)
		}
	}
	switch len(hits) {
	case 0:
		return MachineShowOut{}, errs.New(errs.NotFound, "machine "+work.Quote(q)+" not found").WithHint("run `lore machine list` to see the ids")
	case 1:
		return MachineShowOut{SchemaVersion: SchemaVersion, Kind: KindMachine, Data: hits[0]}, nil
	}
	return MachineShowOut{}, errs.New(errs.InvalidInput, "machine: "+work.Quote(q)+" matches "+fmt.Sprint(len(hits))+" machines; give more characters")
}

// validLabel is MACHINE LABEL: no length cap, one line, no control chars.
func validLabel(s string) error {
	for _, r := range s {
		if unicode.IsControl(r) {
			return errs.New(errs.InvalidInput, "machine: a label must be one line with no control characters")
		}
	}
	return nil
}

// machineLabel writes this machine's profile with the label into its
// target store, creating the row or patching only changed fields.
func machineLabel(ctx context.Context, env Env, m Machines, in MachineLabelIn) (MachineLabelOut, error) {
	label := strings.TrimSpace(in.Label)
	if err := validLabel(label); err != nil {
		return MachineLabelOut{}, err
	}
	me, err := m.ThisMachine(ctx)
	if err != nil {
		return MachineLabelOut{}, err
	}
	target, err := m.MachineTarget(ctx, in.To)
	if err != nil {
		return MachineLabelOut{}, err
	}
	want := map[string]value.Value{}
	for k, v := range me.Fields {
		want[k] = v
	}
	if label == "" {
		want[schema.FieldMachineLabel] = value.Null{}
	} else {
		t, terr := value.TextOf(label)
		if terr != nil {
			return MachineLabelOut{}, errs.Wrap(errs.InvalidInput, "machine: the label is not valid text", terr)
		}
		want[schema.FieldMachineLabel] = t
	}
	out := MachineLabelOut{ID: me.ID.String(), Label: label, Store: string(target)}
	ref := engine.Ref{Store: target, Table: schema.TableMachines, ID: me.RowID}
	_, cur, gerr := env.Get(ctx, ref)
	if errs.Has(gerr, errs.NotFound) {
		delete(want, schema.FieldMachineLabel)
		if label != "" {
			want[schema.FieldMachineLabel], _ = value.TextOf(label)
		}
		if _, err := m.CreateWithID(ctx, target, schema.TableMachines, me.RowID, want); err != nil {
			return MachineLabelOut{}, err
		}
		out.Changed = true
		return out, nil
	}
	if gerr != nil {
		return MachineLabelOut{}, gerr
	}
	patch := map[string]value.Value{}
	for k, v := range want {
		old, ok := cur.Fields[k]
		isNull := v == nil || v.Kind() == value.KindNull
		if (!ok || old == nil || old.Kind() == value.KindNull) && isNull {
			continue
		}
		if ok && old != nil && !isNull && old.String() == v.String() {
			continue
		}
		patch[k] = v
	}
	if len(patch) == 0 {
		return out, nil
	}
	if _, err := env.Update(ctx, ref, patch); err != nil {
		return MachineLabelOut{}, err
	}
	out.Changed = true
	return out, nil
}
