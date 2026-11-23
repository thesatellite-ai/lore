package batch

// apply.go — `lore batch apply --file <jsonl>` (devco D-925, owner's
// 440-action import: every `lore memory add|edit|archive` and `lore meta
// set` was its own store write, so its own git commit (and push), and 440
// actions took hours).
//
// One line of the file is one write op with its input, the JSON body the
// op takes on REST (its flag names, `-` or `_`):
//
//	{"op": "memory add", "input": {"to": "<store>/<folder>", "body": "…"}}
//	{"op": "rule.edit",  "input": {"id": "rul_…", "body": "…"}}
//	{"op": "meta set",   "input": {"id": "mem_…", "pair": "team=core"}}
//
//	read + check every line   unknown op, an op that is not a row write,
//	                          a bad input: refused, nothing written
//	run every line staged     the op's own Run path (validation, secret
//	                          scan, policy, roles, client folders), with
//	                          its writes held in one stage
//	CommitEach                one backend write (one commit) per store,
//	                          all under one bat_ id: `lore batch undo`
//	--dry-run                 the same runs, then the stage is dropped
//
// A line that writes a row an earlier line of the file already wrote
// (two edits of one row) cannot join the stage (it cannot read its own
// rows back): what is staged is written first, then that line starts the
// next stage, so the file still ends as the lines say, with one more
// commit per such split.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// OpApply is `lore batch apply`.
const OpApply = "batch.apply"

// Limits and words of batch.apply.
const (
	// maxApplyBytes bounds the file read (a few thousand rows of notes).
	maxApplyBytes = 64 << 20
	// maxApplyErrors is how many bad lines one refusal lists.
	maxApplyErrors = 20
	commentMark    = "#"
)

// applyVerbs are the last name parts of the ops a line may run: the row
// writes that go through the engine's staged write path (add, edit,
// archive, metadata). Deletes, moves and restores write straight to the
// store (they may span stores) and setup/config ops write files, so they
// would break the one-commit-per-store promise: they are refused with the
// command to run instead.
var applyVerbs = []string{"add", "edit", "archive", "unarchive", "set", "unset", "attach", "detach", "invalidate"}

// applyNotNouns are nouns whose add/edit/set ops change setup, roster or
// config rather than notes: refused for the same reason.
var applyNotNouns = []string{"batch", "branch", "config", "identity", "member", "org", "pin", "policy", "project", "repo", "setup", "store", "team", "vars", "actor", "secrets", "external_source", "pii_pattern", "contact_type", "role"}

// applyNotOps are single ops of an allowed noun that register settings the
// later lines of the same file would read (a staged write is not visible to
// reads): `meta add` then `meta set` of that key in one file would fail.
var applyNotOps = []string{"meta.add", "meta.edit", "meta.value.add", "tag.add", "tag.edit"}

// ApplyIn is the input of batch.apply.
type ApplyIn struct {
	File   string `json:"file" arg:"0" desc:"a JSON-lines file: one write per line, {\"op\": \"memory add\", \"input\": {<the op's flags>}}; blank and # lines are skipped"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"run every line's checks and write nothing"`
}

// ApplyOut is the output of batch.apply.
type ApplyOut struct {
	// Batch is the bat_ id every written row carries ("" for --dry-run).
	Batch string `json:"batch"`
	// Lines is how many ops the file held (blank and # lines not counted).
	Lines int `json:"lines"`
	// Stores is each store written, in first-written order.
	Stores []string `json:"stores"`
	// Writes is how many store writes (git commits on a store clone) the
	// apply made: one per store, one more per line that wrote a row an
	// earlier line already wrote.
	Writes int  `json:"writes"`
	DryRun bool `json:"dry_run"`
}

// Text implements op.Texter.
func (o ApplyOut) Text() string {
	stores := strings.Join(o.Stores, ", ")
	if stores == "" {
		stores = "none"
	}
	if o.DryRun {
		return "checked " + strconv.Itoa(o.Lines) + " line(s): every one would be written (stores: " + stores +
			"); nothing written (--dry-run)\n"
	}
	return "applied " + strconv.Itoa(o.Lines) + " line(s) in " + strconv.Itoa(o.Writes) + " store write(s) (stores: " + stores +
		"); batch " + o.Batch + " (undo: lore batch undo --id " + o.Batch + ")\n"
}

// LineRunner is the Env upgrade batch.apply runs its lines through
// (*wire.App has it): the registry that names each line's op, and the
// surface's own Run path for one op, so a line gets exactly the checks the
// same command gets on the CLI.
type LineRunner interface {
	Registry() *op.Registry
	RunLine(ctx context.Context, e op.Entry, d op.Decoder) (op.Result, error)
}

// applyLine is one parsed line of the file.
type applyLine struct {
	n     int // 1-based line number in the file
	name  string
	entry op.Entry
	body  []byte
}

// rawLine is the JSON shape of one line.
type rawLine struct {
	Op    string          `json:"op"`
	Input json.RawMessage `json:"input"`
}

func applyEntry(o ops) op.Entry {
	return op.Define(op.Spec{Name: OpApply,
		Summary:    "Apply a file of row writes (adds, edits, archives, metadata) as one commit per store, every line checked like the single command",
		Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{MayAsk: true},
		Errors: []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.SecretDetected, errs.NeedsConfirm,
			errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid, errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable,
			errs.Unsupported, errs.Internal},
		Examples: []op.Example{{Title: "apply hundreds of writes as one commit per store", CLI: "lore batch apply --file notes.jsonl", Want: "applied"}}}, o.apply)
}

func (o ops) apply(ctx context.Context, in ApplyIn) (ApplyOut, error) {
	r, ok := upgrade.As[LineRunner](o.env)
	if !ok {
		return ApplyOut{}, errs.New(errs.Unsupported, "batch apply: this surface cannot run writes")
	}
	raw, err := readApplyFile(in.File)
	if err != nil {
		return ApplyOut{}, err
	}
	lines, err := parseApply(r.Registry(), raw)
	if err != nil {
		return ApplyOut{}, err
	}
	out := ApplyOut{Lines: len(lines), Stores: []string{}, DryRun: in.DryRun}
	if len(lines) == 0 {
		return out, errs.New(errs.InvalidInput, "batch apply: "+in.File+" holds no op lines").
			WithHint(`one write per line: {"op": "memory add", "input": {"to": "<store>/<folder>", "body": "…"}}`)
	}
	bctx, id, err := bulk.Begin(ctx, bulk.KindApply)
	if err != nil {
		return out, err
	}
	if !in.DryRun {
		out.Batch = id.String()
	}
	bctx = store.WithCommitTitle(bctx, "batch apply: "+strconv.Itoa(len(lines))+" ops from "+filepath.Base(in.File)+", batch "+id.String())
	a := applier{run: r, ctx: bctx, dry: in.DryRun, out: &out}
	a.open()
	for _, l := range lines {
		if err = a.line(l); err != nil {
			engine.Discard(a.stage)
			return out, a.failed(l, err)
		}
	}
	if in.DryRun {
		a.note(engine.StagedStores(a.stage))
		engine.Discard(a.stage)
		return out, nil
	}
	if err = a.flush(); err != nil {
		return out, a.failed(applyLine{}, err)
	}
	return out, nil
}

// applier runs the lines of one apply.
type applier struct {
	run   LineRunner
	ctx   context.Context
	stage context.Context
	dry   bool
	out   *ApplyOut
	// done is how many lines were run into stages already written.
	done, staged int
}

// open starts a fresh stage.
func (a *applier) open() { a.stage = engine.Staging(a.ctx) }

// line runs one line into the open stage. A line that writes a row the
// stage already holds lands the stage first and runs again in a new one.
func (a *applier) line(l applyLine) error {
	_, err := a.run.RunLine(a.stage, l.entry, op.JSONDecoder{Body: l.body})
	if errors.Is(err, store.ErrBatch) {
		if a.dry {
			a.note(engine.StagedStores(a.stage))
			engine.Discard(a.stage)
		} else if ferr := a.flush(); ferr != nil {
			return ferr
		}
		a.open()
		_, err = a.run.RunLine(a.stage, l.entry, op.JSONDecoder{Body: l.body})
	}
	if err != nil {
		return err
	}
	a.staged++
	return nil
}

// flush writes the open stage, one store write per store.
func (a *applier) flush() error {
	written, _, err := engine.CommitEach(a.stage)
	a.note(written)
	a.out.Writes += len(written)
	if err != nil {
		return err
	}
	a.done += a.staged
	a.staged = 0
	return nil
}

// note adds stores to the output's list.
func (a *applier) note(stores []store.Source) {
	for _, s := range stores {
		if !slices.Contains(a.out.Stores, string(s)) {
			a.out.Stores = append(a.out.Stores, string(s))
		}
	}
}

// failed is the error of a refused line (l zero: the final store write),
// naming what was written before it.
func (a *applier) failed(l applyLine, err error) error {
	what := "batch apply"
	if l.n > 0 {
		what += ": line " + strconv.Itoa(l.n) + " (" + l.name + ")"
	}
	if a.dry {
		return errs.Prefix(what+" (--dry-run, nothing written)", err)
	}
	if a.out.Writes == 0 {
		return errs.Prefix(what+"; nothing written", err)
	}
	b := a.out.Batch
	if l.n == 0 {
		return errs.Prefix(what+": a store write failed after "+strconv.Itoa(a.out.Writes)+" store write(s) landed ("+
			strings.Join(a.out.Stores, ", ")+") in batch "+b+" (undo: lore batch undo --id "+b+")", err)
	}
	return errs.Prefix(what+"; the "+strconv.Itoa(a.done)+" line(s) before it were written in batch "+b+
		" (undo: lore batch undo --id "+b+"; or fix the line, drop the written lines and apply again)", err)
}

// readApplyFile reads at most maxApplyBytes of path.
func readApplyFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errs.New(errs.InvalidInput, "batch apply: name the file to apply").WithHint("lore batch apply --file <file.jsonl>")
	}
	fh, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "batch apply: open "+path, err).WithHint("check the path: it must name a readable JSON-lines file")
	}
	defer func() { _ = fh.Close() }() // read only
	b, err := io.ReadAll(io.LimitReader(fh, maxApplyBytes+1))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "batch apply: read "+path, err)
	}
	if len(b) > maxApplyBytes {
		return nil, errs.New(errs.InvalidInput, "batch apply: "+path+" is over "+strconv.Itoa(maxApplyBytes)+" bytes").
			WithHint("split it into several files and apply each")
	}
	return b, nil
}

// parseApply checks every line before anything runs: the JSON shape, a
// known op that may run in a batch, and an input the op decodes. Every bad
// line (up to maxApplyErrors) is listed in one E_INVALID_INPUT.
func parseApply(reg *op.Registry, raw []byte) ([]applyLine, error) {
	var out []applyLine
	var bad []string
	for i, text := range bytes.Split(raw, []byte("\n")) {
		t := bytes.TrimSpace(text)
		if len(t) == 0 || bytes.HasPrefix(t, []byte(commentMark)) {
			continue
		}
		l, err := parseApplyLine(reg, i+1, t)
		if err != nil {
			bad = append(bad, "  line "+strconv.Itoa(i+1)+": "+err.Error())
			continue
		}
		out = append(out, l)
	}
	if len(bad) == 0 {
		return out, nil
	}
	n := len(bad)
	if n > maxApplyErrors {
		bad = append(bad[:maxApplyErrors], "  and "+strconv.Itoa(n-maxApplyErrors)+" more")
	}
	return nil, errs.New(errs.InvalidInput, "batch apply: "+strconv.Itoa(n)+" line(s) cannot run; nothing written:\n"+strings.Join(bad, "\n")).
		WithHint(`fix each line as it says, then apply again; a line is {"op": "memory add", "input": {"body": "…"}}`)
}

// parseApplyLine checks one line.
func parseApplyLine(reg *op.Registry, n int, t []byte) (applyLine, error) {
	var rl rawLine
	dec := json.NewDecoder(bytes.NewReader(t))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rl); err != nil {
		return applyLine{}, errors.New(`not a {"op": …, "input": {…}} object: ` + err.Error())
	}
	name := opName(rl.Op)
	if name == "" {
		return applyLine{}, errors.New(`no "op" (the command, e.g. "memory add")`)
	}
	e, ok := reg.Lookup(name)
	if !ok {
		return applyLine{}, errors.New("unknown op " + strconv.Quote(rl.Op) + " (write it as the command: \"memory add\", \"rule edit\", \"meta set\")")
	}
	if why := notApplicable(e.Spec()); why != "" {
		return applyLine{}, errors.New(strconv.Quote(rl.Op) + " cannot run in a batch apply: " + why)
	}
	body, err := inputBody(rl.Input)
	if err != nil {
		return applyLine{}, err
	}
	if _, err = e.Decode(op.JSONDecoder{Body: body}); err != nil {
		return applyLine{}, errors.New(strings.TrimSpace(errs.EnvelopeOf(err).Error.Message))
	}
	return applyLine{n: n, name: strings.ReplaceAll(name, ".", " "), entry: e, body: body}, nil
}

// opName is the registry name of a line's op: "memory add", "memory.add"
// and "lore memory add" all name memory.add.
func opName(s string) string {
	f := strings.Fields(strings.ReplaceAll(strings.TrimSpace(s), ".", " "))
	if len(f) > 0 && f[0] == "lore" {
		f = f[1:]
	}
	for i := range f {
		f[i] = strings.ReplaceAll(f[i], "-", "_")
	}
	return strings.Join(f, ".")
}

// notApplicable is why spec cannot run as a line ("" when it can).
func notApplicable(s op.Spec) string {
	parts := strings.Split(s.Name, ".")
	verb := parts[len(parts)-1]
	switch {
	case s.Access != op.AccessWrite || s.Local || s.Hidden:
		return "it is not a row write"
	case slices.Contains(applyNotNouns, parts[0]):
		return "it changes setup or the roster, not notes; run it on its own"
	case slices.Contains(applyNotOps, s.Name):
		return "it registers what later lines read; run `lore " + strings.ReplaceAll(s.Name, ".", " ") + "` before the apply"
	case !slices.Contains(applyVerbs, verb):
		return "only add, edit, archive, unarchive and metadata set/unset lines run in a batch; run `lore " +
			strings.ReplaceAll(s.Name, ".", " ") + "` on its own"
	}
	return ""
}

// inputBody is a line's input as the op's JSON body, with flag spellings
// (dry-run) turned into JSON names (dry_run). A line cannot ask a preview
// of its own: --dry-run of the apply covers the file.
func inputBody(in json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(in)) == 0 {
		return []byte("{}"), nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(in, &m); err != nil {
		return nil, errors.New(`"input" is not an object of the op's flags: ` + err.Error())
	}
	fixed := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		k = strings.ReplaceAll(strings.TrimPrefix(k, "--"), "-", "_")
		if k == op.InputDryRun {
			return nil, errors.New(`"dry_run" in a line: run the whole file with lore batch apply --dry-run instead`)
		}
		fixed[k] = v
	}
	b, err := json.Marshal(fixed)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "batch apply: encode a line", err)
	}
	return b, nil
}
