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
//	RefreshMachineProfile (wire, after an ordinary write; OV3 MACH-AUTO)
//	              → the same refresh without touching the label
//	machine.list LORE / LAST WRITE ← MachineVersions (team_versions, OQ7)

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
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

// MachineVersion is the newest lore one machine wrote with, from the
// `_w` stamps of the attached stores (the team_versions read, OQ7).
type MachineVersion struct {
	Version   string
	LastWrite string // RFC 3339 updated_at of that save
}

// MachineVersions is the optional Env upgrade for the LORE and LAST WRITE
// columns of `lore machines` (6.8): full machine id → newest version.
// Without it the columns fall back to "-" and the profile's updated_at.
type MachineVersions interface {
	MachineVersions(ctx context.Context) (map[string]MachineVersion, error)
}

// MachineListIn is the input of machine.list.
type MachineListIn struct {
	Store string `json:"store,omitempty" flag:"store" alias:"to" desc:"only machines of this store and its folders, <store>/<folder> (default every attached store)"`
	// Org narrows to the stores this repo attaches for an org: the store
	// named <org> and its folders <org>/<folder> (CLI_REFERENCE 3.8).
	Org string `json:"org,omitempty" flag:"org" desc:"only machines of this org's attached stores"`
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
	// --limit / --page: the shared paging flags of every list (op.PageFlags, T-4).
	op.PageFlags
}

// MachineShowIn is the input of machine.show.
type MachineShowIn struct {
	ID string `json:"id" arg:"0" desc:"a machine id or a unique prefix of at least 8 hex characters (mch_0cd630f0)"`
}

// MachineLabelIn is the input of machine.label.
type MachineLabelIn struct {
	// Label is required in the help and docs, but checked by the
	// operation (required:"after-lookup"), because "" is a value here: it
	// clears the label (`lore machine label ""`, `whoami --machine-label
	// ""`), and the decoder would refuse it as missing. A missing argument
	// reads as "" too (the operation cannot tell them apart); clearing a
	// label is harmless and undone by setting it again (G-ORGMIG-9).
	Label string `json:"label,omitempty" arg:"0" required:"after-lookup" desc:"the label; \"\" clears it (one line, no control characters)"`
	To    string `json:"to,omitempty" flag:"to" desc:"the store holding the profile (default the first own store)"`
}

// Machine is one profile in --json form.
type Machine struct {
	ID        string            `json:"id"`
	RowID     string            `json:"row_id"`
	IDSource  string            `json:"id_source,omitempty"`
	Label     string            `json:"label,omitempty"`
	Hostname  string            `json:"hostname,omitempty"`
	OS        NameVersion       `json:"os,omitzero"`
	Kernel    NameVersion       `json:"kernel,omitzero"`
	Libc      NameVersion       `json:"libc,omitzero"`
	Arch      string            `json:"arch,omitempty"`
	CPU       CPU               `json:"cpu,omitzero"`
	RAMMB     int64             `json:"ram_mb,omitempty"`
	OSUser    string            `json:"os_user,omitempty"`
	Install   string            `json:"install_method,omitempty"`
	Runtime   map[string]string `json:"runtime,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	// Lore and LastWrite come from the team_versions read (MachineVersions):
	// the newest lore version this machine saved with, and when.
	Lore      string          `json:"lore_version,omitempty"`
	LastWrite string          `json:"last_write,omitempty"`
	Store     store.SourceRef `json:"_source,omitempty,omitzero"`
	// CreatedAt / CreatedBy / UpdatedBy are the row envelope's audit fields
	// (INTEG-AUDIT-1, auditrec), last so the older keys keep their order.
	CreatedAt string `json:"created_at,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	// Stores are the stores holding a copy of this profile (list only; the
	// list shows each machine once, FB-ACTOR-STORES).
	Stores []string `json:"stores,omitempty"`
}

// NameVersion is a profile {name, version} object (os, kernel, libc).
type NameVersion struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// Text is "ubuntu 24.04".
func (n NameVersion) Text() string { return strings.TrimSpace(n.Name + " " + n.Version) }

// CPU is the profile cpu object; Cores stays a number in --json (OV3: the
// closed string map printed it as a string).
type CPU struct {
	Model string `json:"model,omitempty"`
	Cores int64  `json:"cores,omitempty"`
}

// MachineListOut is the list envelope.
type MachineListOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count,omitempty"`
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
		op.Define(op.Spec{Name: OpMachineShow, Summary: "Show one machine profile in full", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors, Examples: []op.Example{{Title: "one machine profile", CLI: "lore machine show --id mch_0a1b2c3d"}}}, o.machineShow),
	}
	if m, ok := upgrade.As[Machines](env); ok {
		out = append(out, op.Define(op.Spec{Name: OpMachineLabel, Summary: "Set (or clear with \"\") this machine's synced label and refresh its profile",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append([]errs.Code{errs.Refused}, writeErrors...), Examples: []op.Example{{Title: "name this machine", CLI: "lore machine label --label work-laptop"}}},
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
	all := make([][]string, len(o.Data))
	for i, m := range o.Data {
		all[i] = m.Stores
	}
	// FB-ACTOR-STORES: the STORES column and the count line only when the
	// list was merged from two or more stores
	many := merged(all)
	head := "ID\tLABEL\tHOST\tOS\tKERNEL\tLIBC\tARCH\tCPU (CORES)\tRAM\tRUNTIME\tLORE\tLAST WRITE"
	if many {
		head += "\tSTORES"
	}
	_, _ = fmt.Fprintln(tw, head)
	for _, m := range o.Data {
		cpu := m.CPU.Model
		if m.CPU.Cores > 0 {
			cpu = strings.TrimSpace(fmt.Sprintf("%s (%d)", cpu, m.CPU.Cores))
		}
		ram := "-"
		if m.RAMMB > 0 {
			ram = fmt.Sprintf("%d GB", (m.RAMMB+512)/1024)
		}
		last := m.LastWrite
		if last == "" {
			last = m.UpdatedAt
		}
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s", shortMachine(m.ID), dash(m.Label), dash(m.Hostname),
			dash(m.OS.Text()), dash(m.Kernel.Text()), dash(m.Libc.Text()), dash(m.Arch),
			dash(cpu), ram, dash(runtimeText(m.Runtime)), dash(m.Lore), dash(dateOnly(last)))
		if many {
			line += "\t" + dash(strings.Join(m.Stores, ", "))
		}
		_, _ = fmt.Fprintln(tw, line)
	}
	_ = tw.Flush()
	if many {
		b.WriteString(countLine(len(o.Data), all, "machine", "machines"))
	}
	return b.String()
}

// Text is the full profile.
// Text is the show text ending with the writers line (INTEG-AUDIT-2).
func (o MachineShowOut) Text() string {
	d := o.Data
	return o.mainText() + auditrec.Line(d.CreatedAt, d.CreatedBy, d.UpdatedAt, d.UpdatedBy)
}

func (o MachineShowOut) mainText() string {
	m := o.Data
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", m.ID)
	for _, kv := range [][2]string{{"label", m.Label}, {"id_source", m.IDSource}, {"host", m.Hostname}, {"os", m.OS.Text()},
		{"kernel", m.Kernel.Text()}, {"libc", m.Libc.Text()}, {"arch", m.Arch},
		{"cpu", m.CPU.Model}, {"cores", coresText(m.CPU.Cores)}, {"ram_mb", fmt.Sprint(m.RAMMB)}, {"os_user", m.OSUser},
		{"install", m.Install}, {"runtime", runtimeText(m.Runtime)}, {"lore", m.Lore}, {"last_write", m.LastWrite}, {"updated", m.UpdatedAt}} {
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

// coresText is the core count, "" when unknown.
func coresText(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// nameVersionOf reads an {name, version} profile object; a malformed one
// reads as empty (shown "-").
func nameVersionOf(raw json.RawMessage) NameVersion {
	var nv NameVersion
	if len(raw) > 0 && json.Unmarshal(raw, &nv) != nil {
		return NameVersion{}
	}
	return nv
}

// cpuOf reads the cpu profile object; cores is a JSON number (a string
// count written by an older build is parsed too).
func cpuOf(raw json.RawMessage) CPU {
	m := objMap(raw)
	c := CPU{Model: m["model"]}
	if n, err := strconv.ParseInt(m["cores"], 10, 64); err == nil {
		c.Cores = n
	}
	return c
}

// objMap reads a small JSON object of the profile (cpu, runtime) as
// name → text: the op registry describes outputs with closed
// types only, and every value of these objects is a string or a number.
// A value that is neither (a nested object) is left out.
func objMap(raw json.RawMessage) map[string]string {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if t := scalar(v); t != "" {
			out[k] = t
		}
	}
	return out
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
func runtimeText(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	parts := []string{m["type"]}
	for _, k := range []string{"provider", "repo", "function", "region", "service", "job", "pod", "namespace", "name", "distro", "container", "inside"} {
		if v := m[k]; v != "" {
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
		Label: work.Field(r, schema.FieldMachineLabel), Hostname: work.Field(r, "hostname"), OS: nameVersionOf(raw("os")), Kernel: nameVersionOf(raw("kernel")),
		Libc: nameVersionOf(raw("libc")), Arch: work.Field(r, "arch"), CPU: cpuOf(raw("cpu")), RAMMB: work.Int(r, "ram_mb"), OSUser: work.Field(r, "os_user"),
		Install: work.Field(r, "install_method"), Runtime: objMap(raw("runtime")), UpdatedAt: timeOf(r, false), Store: sourceOf(lab, s.Source), CreatedAt: timeOf(r, true), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy}
}

// machineList lists every live profile once (same id in several stores:
// the first store's), oldest first.
func (o ops) machineList(ctx context.Context, in MachineListIn) (MachineListOut, error) {
	rows, err := work.ListAll(ctx, o.env, schema.TableMachines, in.Store)
	if err != nil {
		return MachineListOut{}, err
	}
	if org := strings.TrimSpace(in.Org); org != "" {
		if rows, err = orgRows(o.env, rows, org); err != nil {
			return MachineListOut{}, err
		}
	}
	out := MachineListOut{SchemaVersion: SchemaVersion, Kind: KindMachines, Data: []Machine{}}
	seen := map[string]bool{}
	held := storesOf(rows)
	lab := work.LabelerOf(o.env)
	for _, s := range sortByCreated(rows) {
		if seen[s.Row.ID.String()] {
			continue
		}
		seen[s.Row.ID.String()] = true
		m := machineOf(lab, s)
		m.Stores = held[s.Row.ID.String()]
		out.Data = append(out.Data, m)
	}
	out.Count = len(out.Data)
	if mv, ok := upgrade.As[MachineVersions](o.env); ok && len(out.Data) > 0 {
		vers, verr := mv.MachineVersions(ctx)
		if verr != nil {
			return MachineListOut{}, verr
		}
		for i := range out.Data {
			if v, hit := vers[out.Data[i].ID]; hit {
				out.Data[i].Lore, out.Data[i].LastWrite = v.Version, v.LastWrite
			}
		}
	}
	return out, nil
}

// machineShow finds one profile by a unique prefix of its full id.
func (o ops) machineShow(ctx context.Context, in MachineShowIn) (MachineShowOut, error) {
	q := strings.TrimSpace(in.ID)
	if !strings.HasPrefix(q, ids.MachinePrefix) || len(q) < minMachinePrefix {
		// The one lookup rule (ops/internal/lookup): a value that cannot
		// name a machine answers E_NOT_FOUND like an unknown id does.
		return MachineShowOut{}, lookup.BadID("machine: "+work.Quote(q)+" is not a machine id: give mch_ and at least 8 hex characters", nil).
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
	return MachineShowOut{}, errs.New(errs.InvalidInput, "machine: "+work.Quote(q)+" matches "+fmt.Sprint(len(hits))+" machines; give more characters").
		WithHint("run `lore machine list` to see the full ids")
}

// validLabel is MACHINE LABEL: no length cap, one line, no control chars.
func validLabel(s string) error {
	for _, r := range s {
		if unicode.IsControl(r) {
			return errs.New(errs.InvalidInput, "machine: a label must be one line with no control characters").
				WithHint("give the label as one line of text, e.g. lore machine label --label work-laptop")
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
	var lv value.Value = value.Null{}
	if label != "" {
		t, terr := value.TextOf(label)
		if terr != nil {
			return MachineLabelOut{}, errs.Wrap(errs.InvalidInput, "machine: the label is not valid text", terr)
		}
		lv = t
	}
	out := MachineLabelOut{ID: me.ID.String(), Label: label, Store: string(target)}
	out.Changed, err = writeProfile(ctx, env, m, me, target, lv)
	return out, err
}

// RefreshMachineProfile writes this machine's profile into its default
// target store (MACH-TARGET) when a field changed or the row is missing,
// leaving the label as it is. wire calls it after an ordinary write
// (OV3 MACH-AUTO); E_REFUSED (no own store attached) means nothing to do
// and answers changed=false, nil.
func RefreshMachineProfile(ctx context.Context, env Env, m Machines) (bool, error) {
	me, err := m.ThisMachine(ctx)
	if err != nil {
		return false, err
	}
	target, err := m.MachineTarget(ctx, "")
	if errs.Has(err, errs.Refused) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return writeProfile(ctx, env, m, me, target, nil)
}

// writeProfile creates the machines row (with label when not nil and not
// null) or patches only the fields that changed; label nil leaves the
// label untouched, value.Null clears it.
func writeProfile(ctx context.Context, env Env, m Machines, me ThisMachine, target store.Source, label value.Value) (bool, error) {
	want := map[string]value.Value{}
	for k, v := range me.Fields {
		want[k] = v
	}
	if label != nil {
		want[schema.FieldMachineLabel] = label
	}
	ref := engine.Ref{Store: target, Table: schema.TableMachines, ID: me.RowID}
	_, cur, gerr := env.Get(ctx, ref)
	if errs.Has(gerr, errs.NotFound) {
		if label == nil || label.Kind() == value.KindNull {
			delete(want, schema.FieldMachineLabel)
		}
		if _, err := m.CreateWithID(ctx, target, schema.TableMachines, me.RowID, want); err != nil {
			return false, err
		}
		return true, nil
	}
	if gerr != nil {
		return false, gerr
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
		return false, nil
	}
	if _, err := env.Update(ctx, ref, patch); err != nil {
		return false, err
	}
	return true, nil
}

// orgSep joins an org name and a linked folder in a store name
// (wire renameOrgClashes: "<org>/<folder>").
const orgSep = "/"

// orgRows keeps the rows of org's attached stores (<org> and
// <org>/<folder>); an org with no store attached here is E_NOT_FOUND.
func orgRows(env work.Env, rows []work.Sourced, org string) ([]work.Sourced, error) {
	ofOrg := func(src store.Source) bool {
		return string(src) == org || strings.HasPrefix(string(src), org+orgSep)
	}
	if !slices.ContainsFunc(env.Stores(), ofOrg) {
		return nil, errs.New(errs.NotFound, "machines: no store of org "+strconv.Quote(org)+" is attached in this repo").
			WithHint("lore store list shows the attached stores")
	}
	return slices.DeleteFunc(rows, func(s work.Sourced) bool { return !ofOrg(s.Source) }), nil
}
