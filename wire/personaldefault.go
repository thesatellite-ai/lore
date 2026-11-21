package wire

// personaldefault.go — `lore ask` and `lore search` outside a code repo
// read the personal store (devco:D-828, bug hunt H-86). Both are reads:
// from the home folder, with no --store and no setup covering the folder,
// they refused E_NOT_A_STORE with a hint naming --store me, so an agent
// spent a call learning to add it and then asked again. Now they read the
// personal store there, as with --store me, and say so in one line.
//
//	ask/search, outside any work tree, no --store/--to/--project
//	  ─▶ a setup's org store covers the folder ─▶ reads it (as before)
//	  ─▶ none, a personal store is set up       ─▶ reads `--store me`
//	                                               + note line
//	  ─▶ none, no personal store                ─▶ E_NOT_A_STORE (as before)
//
// Inside a repo (or a store clone) nothing changes: the repo's open says
// what is read there.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	opsask "github.com/thesatellite-ai/lore/ops/ask"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/store"
)

// PersonalDefaultNote is the line a read that defaulted to the personal
// store prints after its answer, so the person (or agent) knows what was
// read and that `--store <org>` reads an org's stores instead.
const PersonalDefaultNote = "note: read your personal store (not in a repo); --store <org> reads an org's stores"

// personalDefaultOps are the reads that default to the personal store
// outside a repo: the recall reads an agent runs first from any folder.
// Lists and sql keep their refusal (its hint names --store me): their
// answer depends on which store is read, and none is the obvious one.
var personalDefaultOps = map[string]bool{
	opsask.OpRun:    true,
	opssearch.OpRun: true,
}

// personalDefaultRead reports whether e, run outside a work tree with no
// store named (outsideTo's useDefault), reads the personal store instead
// of refusing: a personalDefaultOps read, no --setup named (the person
// chose where to read), no setup's org roster covering the folder (that
// store is the default, runOnDefaultStore), and a personal store set up.
func (a *App) personalDefaultRead(ctx context.Context, e op.Entry) bool {
	if !personalDefaultOps[e.Spec().Name] || strings.TrimSpace(op.SetupOf(ctx)) != "" {
		return false
	}
	if r, err := a.repoRoster(ctx); !noOrgDefault(r, err) {
		return false
	}
	return a.hasPersonalStore(ctx)
}

// noOrgDefault reports whether a folder outside any repo has no default
// store: no org's setup covers it (repoRoster gave the repo's own roster,
// or failed). runOnDefaultStore refuses there and personalDefaultRead
// reads the personal store there: one rule for both.
func noOrgDefault(r rosterAt, err error) bool {
	return err != nil || r.src == store.SourceRepo
}

// personalDefaultStore is the `to` a personalDefaultRead reads: the whole
// personal store, as `--store me` names it.
const personalDefaultStore = attach.ReservedMe

// personalDefaultKey holds the mark App.Run reads after the run: whether
// runStoreOnly read the personal store as a personalDefaultRead.
type personalDefaultKey struct{}

// withPersonalDefaultMark returns ctx carrying a fresh, unset mark.
// Why a mark and not the note itself: App.Run's empty-answer hooks
// (emptysearch.go) read the answer's last line, so the note is added after
// them, by App.Run, once the run said it read the personal store.
func withPersonalDefaultMark(ctx context.Context) context.Context {
	return context.WithValue(ctx, personalDefaultKey{}, new(bool))
}

// personalDefaultMarked reports whether the run of ctx read the personal
// store as a personalDefaultRead (its mark is set).
func personalDefaultMarked(ctx context.Context) bool {
	mark, ok := ctx.Value(personalDefaultKey{}).(*bool)
	return ok && *mark
}

// markPersonalDefault sets the mark of ctx (withPersonalDefaultMark), if any.
func markPersonalDefault(ctx context.Context) {
	if mark, ok := ctx.Value(personalDefaultKey{}).(*bool); ok {
		*mark = true
	}
}

// notePersonalDefault appends PersonalDefaultNote to res's text (the JSON
// form is unchanged: each row already carries its place, `me…`).
func notePersonalDefault(res op.Result) op.Result {
	return res.WithTextEdit(func(s string) string {
		return strings.TrimRight(s, "\n") + "\n" + PersonalDefaultNote + "\n"
	})
}
