package main

// render_shim.go — `lore render` and `lore why-context` run on
// wire/renderhost (P3-CMDLORE2: the machinery moved to the root module so
// cmd/lore serves them too). This file builds the old binary's Host (its
// v0.1.17 project resolution, colours, LORE_NOW clock and error mapping)
// and keeps the names the old binary's other commands (context, doctor,
// init, sync, coord policy reads) still call, each forwarding to the
// moved code.

import (
	"context"
	"strings"
	"time"

	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
	"github.com/thesatellite-ai/lore/wire/renderhost"
)

// The row fields and file names the other commands read (renderhost's).
const (
	renderFieldBody         = renderhost.FieldBody
	renderFieldTitle        = renderhost.FieldTitle
	renderFieldSeverity     = renderhost.FieldSeverity
	renderFieldSuperseded   = renderhost.FieldSuperseded
	renderFieldActivation   = renderhost.FieldActivation
	renderFieldGlobs        = renderhost.FieldGlobs
	privateFieldName        = renderhost.FieldName
	privateFieldDescription = renderhost.FieldDescription
	privateFieldStatus      = renderhost.FieldStatus
	dirFileName             = renderhost.DirFileName
	claudeLocalFile         = renderhost.ClaudeLocalFile
	renderHeader            = renderhost.Header
)

// renderRow is a live row and the store it came from.
type renderRow = renderhost.Row

// rh is the render Host over the mounted core (coreApp may be nil before
// the mount; every Host method that needs it then fails as before).
func rh() *renderhost.Host {
	home, err := loreHome()
	if err != nil {
		home = ""
	}
	return renderhost.New(renderhost.Options{
		App:   coreApp,
		Home:  home,
		Decor: renderhost.Decor{Success: style.Success, Muted: style.Muted, Warn: style.Warn},
		Now:   func() time.Time { return cliClock().Now() },
		ProjectID: func(ctx context.Context) (_ string, err error) {
			// v0.1.17's resolution: it also builds lore.db for a fresh clone.
			rctx, client, err := resolveContext(&commonFlags{})
			if err != nil {
				return "", err
			}
			defer joinClose(&err, client, "the project DB")
			return resolveProjectID(ctx, rctx.ProjectID)
		},
		Profiles: func(ctx context.Context) ([]toolprofile.Profile, error) {
			cfg, err := loadTeamConfig(ctx)
			if err != nil {
				return nil, err
			}
			return cfg.Context.Profiles, nil
		},
		RepoIsClient: coreRepoIsClient,
		Redact:       redactIfQuiet,
		HostError:    asCoreError,
	})
}

// renderFlags are the flags of the post-sync re-render (sync_wire.go).
type renderFlags struct {
	outPath   string
	noPointer bool
	quiet     bool
	w         *strings.Builder
}

// out is where the render's lines go.
func (f *renderFlags) out() *strings.Builder {
	if f.w == nil {
		f.w = &strings.Builder{}
	}
	return f.w
}

// runRender is `lore render` with f.
func runRender(ctx context.Context, f *renderFlags) error {
	text, err := rh().Run(ctx, renderhost.Flags{OutPath: f.outPath, NoPointer: f.noPointer, Quiet: f.quiet})
	_, _ = f.out().WriteString(text) // a strings.Builder write never fails
	return err
}

func rerenderPrivateQuiet(ctx context.Context, root string) { rh().RerenderPrivateQuiet(ctx, root) }

func policySet(ctx context.Context) (policy.Set, bool) { return rh().PolicySet(ctx) }

func renderField(r row.Row, name string) string { return renderhost.Field(r, name) }

func renderTagIDs(r row.Row) []string { return renderhost.TagIDs(r) }

func countsText(c map[string]int) string { return renderhost.CountsText(c) }

func renderLiveRows(ctx context.Context, table row.Table) ([]renderRow, error) {
	return rh().LiveRows(ctx, table)
}

func renderSources(ctx context.Context) ([]store.Source, error) { return rh().Sources(ctx) }

func loadTopicIndex(ctx context.Context) ([]renderhost.TopicLine, error) { return rh().TopicIndex(ctx) }

func scopePathsOfRepo(ctx context.Context) map[string]string { return rh().ScopePathsOfRepo(ctx) }

func contextDoctor(ctx context.Context, root string) (warnings, notes []string) {
	return rh().ContextDoctor(ctx, root)
}

func loadToolRegistry(ctx context.Context) (*toolprofile.Registry, error) {
	return rh().ToolRegistry(ctx)
}

// targetSetting reads a repo's context.targets settings row.
type targetSetting = renderhost.TargetSetting

func coreContextTargets(ctx context.Context) ([]string, bool, error) { return rh().ContextTargets(ctx) }

func contextTargetsOf(ctx context.Context, app *wire.App) ([]string, bool, error) {
	return rh().ContextTargetsOf(ctx, app)
}

// contextTarget is renderhost.Target with the field names init and doctor
// read.
type contextTarget struct {
	profile toolprofile.Profile
	file    string
	known   bool
}

func (t contextTarget) host() renderhost.Target {
	return renderhost.NewTarget(t.profile, t.file, t.known)
}

// targetPick is renderhost.TargetPick with the field names init and doctor
// read.
type targetPick struct {
	targets []contextTarget
	sel     toolprofile.Selection
}

func resolveTargets(ctx context.Context, root string, values []string, setting targetSetting) (targetPick, error) {
	p, err := rh().ResolveTargets(ctx, root, values, setting)
	if err != nil {
		return targetPick{}, err
	}
	out := targetPick{sel: p.Selection()}
	for _, t := range p.Targets() {
		out.targets = append(out.targets, contextTarget{profile: t.Profile(), file: t.File(), known: t.Known()})
	}
	return out, nil
}

func stitchPointer(w *strings.Builder, root string, t contextTarget, outFile string) error {
	return rh().StitchPointer(w, root, t.host(), outFile)
}

func describeTargets(ts []contextTarget) string {
	hs := make([]renderhost.Target, 0, len(ts))
	for _, t := range ts {
		hs = append(hs, t.host())
	}
	return renderhost.DescribeTargets(hs)
}

func describeReason(r toolprofile.Reason) string { return renderhost.DescribeReason(r) }

// atomicWriteFile writes data to path through a synced temp file and a
// rename (move and org import).
var atomicWriteFile = renderhost.AtomicWriteFile

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return renderhost.GitOutput(ctx, dir, args...)
}

func gitTracks(ctx context.Context, root, path string) (bool, error) {
	return renderhost.GitTracks(ctx, root, path)
}

func oneLine(s string) string      { return renderhost.OneLine(s) }
func sha256Hex(data []byte) string { return renderhost.Sha256Hex(data) }
