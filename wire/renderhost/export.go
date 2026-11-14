package renderhost

// export.go — the render machinery the old binary's other commands reuse
// (context, doctor, init, sync, coord policy reads), exported while those
// commands still live in saas/cmd/cli. Each is the unexported function of
// the same name; cmd/lore needs only Render and LastRender.

import (
	"context"
	"io/fs"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opswhycontext "github.com/thesatellite-ai/lore/ops/whycontext"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// Row fields and file names the other commands read.
const (
	FieldBody        = renderFieldBody
	FieldTitle       = renderFieldTitle
	FieldSeverity    = renderFieldSeverity
	FieldSuperseded  = renderFieldSuperseded
	FieldActivation  = renderFieldActivation
	FieldGlobs       = renderFieldGlobs
	FieldName        = privateFieldName
	FieldDescription = privateFieldDescription
	FieldStatus      = privateFieldStatus
	DirFileName      = dirFileName
	ClaudeLocalFile  = claudeLocalFile
	Header           = renderHeader
)

// Row is a live row and the store it came from.
type Row = renderRow

// TopicLine is one line of the topic index (`lore context` with no task).
type TopicLine = topicLine

// Target is one context target (a tool profile's pointer file).
type Target = contextTarget

// TargetPick is the resolved context targets and why they were chosen.
type TargetPick = targetPick

// TargetSetting reads a repo's context.targets row.
type TargetSetting = targetSetting

// Flags are render's flags (the old binary's post-sync re-render sets
// them directly).
type Flags struct {
	OutPath   string
	NoPointer bool
	Quiet     bool
}

// Field is a text field of r ("" when absent).
func Field(r row.Row, name string) string { return renderField(r, name) }

// TagIDs are the tag ids of r.
func TagIDs(r row.Row) []string { return renderTagIDs(r) }

// CountsText prints a topic's counts.
func CountsText(c map[string]int) string { return countsText(c) }

// DescribeTargets prints targets on one line.
func DescribeTargets(ts []Target) string { return describeTargets(ts) }

// DescribeReason prints why the targets were chosen.
func DescribeReason(r toolprofile.Reason) string { return describeReason(r) }

// Render implements opsrender.Host.
func (h *Host) Render(ctx context.Context, in opsrender.RunIn) (_ opsrender.Answer, err error) {
	defer func() { err = h.hostError(err) }()
	var text strings.Builder
	if err = h.runRender(ctx, flagsOf(in, &text, false)); err != nil {
		return opsrender.Answer{}, err
	}
	a := opsrender.Answer{Text: text.String()}
	if in.Report || in.Trend {
		// P3-MISC-7: the operation cannot know whether --json was asked,
		// and a report writes nothing, so the JSON form is a second read.
		var data strings.Builder
		if err = h.runRender(ctx, flagsOf(in, &data, true)); err != nil {
			return opsrender.Answer{}, err
		}
		a.Data = []byte(strings.TrimSpace(data.String()))
	}
	return a, nil
}

// flagsOf maps the operation's input onto the command's flags, the lines
// going to w.
func flagsOf(in opsrender.RunIn, w *strings.Builder, asJSON bool) *renderFlags {
	return &renderFlags{outPath: in.OutPath(), targets: in.Targets(), addTargets: in.AddTargets(), noPointer: in.NoPointer, dryRun: in.DryRun,
		report: in.Report, scopes: in.Scopes, check: in.Check, asJSON: asJSON, trend: in.Trend, w: w}
}

// Run is `lore render` with f, its lines returned (the old binary's
// post-sync re-render of LORE.md).
func (h *Host) Run(ctx context.Context, f Flags) (string, error) {
	rf := &renderFlags{outPath: f.OutPath, noPointer: f.NoPointer, quiet: f.Quiet}
	err := h.runRender(ctx, rf)
	return rf.out().String(), err
}

// LastRender implements wire.RenderHistory: the newest render record of
// the checkout's project; none is E_NOT_FOUND naming `lore render`. Scoped
// to the project because the machine store holds every repo's records
// (v0.1.17 read its per-checkout lore.db unscoped).
func (h *Host) LastRender(ctx context.Context) (rec opswhycontext.Record, err error) {
	defer func() { err = h.hostError(err) }()
	projectID, err := h.projectID(ctx)
	if err != nil {
		return rec, err
	}
	rows, err := h.app.RenderHistory(ctx, projectID)
	if err != nil {
		return rec, errs.Wrap(errs.Internal, "render_history query", err)
	}
	if len(rows) == 0 {
		return rec, errs.New(errs.NotFound, "no render history yet").WithHint("run `lore render` first")
	}
	r := rows[len(rows)-1]
	return opswhycontext.Record{ID: r.ID, CreatedAt: r.CreatedAt, TargetPath: r.TargetPath, TotalBytes: r.TotalBytes,
		SHA256: r.SHA256, ScopeSummary: r.ScopeSummary, Text: r.Text}, nil
}

// RerenderPrivateQuiet refreshes the private per-store renders after a sync.
func (h *Host) RerenderPrivateQuiet(ctx context.Context, root string) {
	h.rerenderPrivateQuiet(ctx, root)
}

// RerenderStaleQuiet re-renders LORE.md after a sync when its stamp no
// longer matches its rows and a committed render may change
// (render_stamp.go).
func (h *Host) RerenderStaleQuiet(ctx context.Context, root string) {
	h.rerenderStaleQuiet(ctx, root)
}

// RenderedCopies implements opsrender.CopyChecker: the rendered files and
// pointer files of the repo at root that no longer match their source.
func (h *Host) RenderedCopies(ctx context.Context, root string) (_ []opsrender.CopyFinding, err error) {
	defer func() { err = h.hostError(err) }()
	return h.renderedCopies(ctx, root)
}

var _ opsrender.CopyChecker = (*Host)(nil)

// PolicySet is the effective policy of the repo (false when unreadable).
func (h *Host) PolicySet(ctx context.Context) (policy.Set, bool) { return h.policySet(ctx) }

// LiveRows lists the live, not archived rows of table across the render
// sources.
func (h *Host) LiveRows(ctx context.Context, table row.Table) ([]Row, error) {
	return h.renderLiveRows(ctx, table)
}

// Sources are the stores render reads.
func (h *Host) Sources(ctx context.Context) ([]store.Source, error) { return h.renderSources(ctx) }

// TopicIndex is the topic index.
func (h *Host) TopicIndex(ctx context.Context) ([]TopicLine, error) { return h.loadTopicIndex(ctx) }

// ScopePathsOfRepo maps scope ids to their paths.
func (h *Host) ScopePathsOfRepo(ctx context.Context) map[string]string {
	return h.scopePathsOfRepo(ctx)
}

// ContextDoctor is the context checks of `lore doctor` for root.
func (h *Host) ContextDoctor(ctx context.Context, root string) (warnings, notes []string) {
	return h.contextDoctor(ctx, root)
}

// ToolRegistry is the built-in tool profiles plus the user's own.
func (h *Host) ToolRegistry(ctx context.Context) (*toolprofile.Registry, error) {
	return h.loadToolRegistry(ctx)
}

// ContextTargets is the TargetSetting of the repo this Host's App is
// mounted at.
func (h *Host) ContextTargets(ctx context.Context) ([]string, bool, error) {
	return h.coreContextTargets(ctx)
}

// ContextTargetsOf reads the effective context.targets of app's repo.
func (h *Host) ContextTargetsOf(ctx context.Context, app *wire.App) ([]string, bool, error) {
	return h.contextTargetsOf(ctx, app)
}

// ResolveTargets picks the context targets of root.
func (h *Host) ResolveTargets(ctx context.Context, root string, values []string, setting TargetSetting) (TargetPick, error) {
	return h.resolveTargets(ctx, root, values, setting)
}

// StitchPointer writes the lore pointer block into t's file.
func (h *Host) StitchPointer(w *strings.Builder, root string, t Target, outFile string) error {
	return h.stitchPointer(w, root, t, outFile)
}

// Profile is t's tool profile.
func (t contextTarget) Profile() toolprofile.Profile { return t.profile }

// File is t's pointer file, relative to the repo root.
func (t contextTarget) File() string { return t.file }

// Known is true when t's profile is a registry profile.
func (t contextTarget) Known() bool { return t.known }

// NewTarget is a Target (the old binary keeps its own copy of the type).
func NewTarget(p toolprofile.Profile, file string, known bool) Target {
	return contextTarget{profile: p, file: file, known: known}
}

// Targets are the picked targets.
func (p targetPick) Targets() []Target { return p.targets }

// Selection is how they were picked.
func (p targetPick) Selection() toolprofile.Selection { return p.sel }

// AtomicWriteFile writes data to path through a synced temp file and a
// rename (the old binary's move and org import use it too).
func AtomicWriteFile(path string, data []byte, perm fs.FileMode) error {
	return atomicWriteFile(path, data, perm)
}

// GitOutput runs git -C dir args and returns its trimmed stdout.
func GitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return gitOutput(ctx, dir, args...)
}

// GitTracks reports whether path (relative to root) is in git's index.
func GitTracks(ctx context.Context, root, path string) (bool, error) {
	return gitTracks(ctx, root, path)
}

// OneLine collapses whitespace so a row is one bullet.
func OneLine(s string) string { return oneLine(s) }

// Sha256Hex is the hex sha256 of data.
func Sha256Hex(data []byte) string { return sha256Hex(data) }

// ScopeRenameHints are the `lore scope move` hints for folders git saw
// renamed (the old binary's sync report).
func (h *Host) ScopeRenameHints(ctx context.Context, root string) []string {
	return h.scopeRenameHints(ctx, root)
}
