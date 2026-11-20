package wire

// trackedwiring.go — which clone of a project leaves lore's first render
// and wiring for its commit (stories A-1, N-7).
//
// lore migrate run changes no tracked file but .lore/data (M-6), and no
// plain write changes a tracked file outside .lore/ either (devco:D-753):
// the .gitignore lines and the .gitattributes block a project on the
// current format needs go to every clone's own .git/info/exclude and
// .git/info/attributes until an install step (lore init, lore doctor
// --fix, lore fix) writes the tracked files, in any clone. Before D-753
// the migrator's next write added them to the tracked files (and every
// teammate's write did in a project no migration recorded), which made a
// plain `lore mission add` edit .gitignore right after the migration had
// said only `lore doctor --fix` does, and two clones adding the same
// lines uncommitted made the git pull of either commit abort.
//
// The clone marks below still name the clone whose commit brings the
// project's first render (RenderOwnerHere).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// MigratedHereGitKey is the clone-local git config key lore migrate run
// sets in the clone it ran in. It is never committed or pushed, so it
// names exactly one clone: the one whose next commit is expected to carry
// the project's first render in the new format.
const MigratedHereGitKey = "lore.migratedhere"

// migratedHereOn is the key's value in the migrator's clone.
const migratedHereOn = "true"

// markMigratedHere records in the git config of the clone holding top that
// lore migrate run raised the project's format here. Best effort: without
// the mark this clone's renders stay local like a teammate's until the
// render is committed (RenderOwnerHere).
func markMigratedHere(ctx context.Context, top string) {
	_, _ = gitRun{dir: top, args: []string{"config", "--local", MigratedHereGitKey, migratedHereOn}}.output(ctx) // best effort, see above
}

// InitedHereGitKey is the clone-local git config key `lore init` sets in
// the clone where it created the project's _meta.json (N-7). Like
// MigratedHereGitKey it is never committed, so it names the one clone
// whose commits are expected to bring the project's first render.
const InitedHereGitKey = "lore.initedhere"

// markInitedHere records in the git config of the clone holding top that
// lore init made the project here. Best effort: without the mark this
// clone's renders stay local like a teammate's until the render is
// committed (RenderOwnerHere), and the render line says how to commit it.
func markInitedHere(ctx context.Context, top string) {
	_, _ = gitRun{dir: top, args: []string{"config", "--local", InitedHereGitKey, migratedHereOn}}.output(ctx) // best effort, see above
}

// RenderOwnerHere reports whether a render in the clone of the project at
// root may leave a new, untracked .lore/LORE.md (and LORE.full.md) in
// git status for the next `git add -A` (N-7, the render side of A-1):
//
//	_meta.json not committed yet (the project is being made here) ─▶ yes
//	this clone ran lore init or lore migrate run (lore.initedhere,
//	   lore.migratedhere) ─▶ yes: its commit carries the first render
//	any other clone ─▶ no: the render files go to the clone's own
//	   .git/info/exclude, so the git pull of the owner's commit that adds
//	   them overwrites them instead of aborting ("untracked working tree
//	   files would be overwritten by merge")
//
// Not a git checkout (nothing to pull) answers yes.
func RenderOwnerHere(ctx context.Context, root string) bool {
	if _, err := (gitRun{dir: root, args: []string{"rev-parse", "--git-dir"}}).output(ctx); err != nil {
		return true
	}
	if _, err := (gitRun{dir: root, args: []string{"ls-files", "--error-unmatch", "--", rowfs.RepoMetaPath}}).output(ctx); err != nil {
		return true
	}
	for _, key := range []string{InitedHereGitKey, MigratedHereGitKey} {
		out, err := gitRun{dir: root, args: []string{"config", "--local", "--get", key}}.output(ctx)
		if err == nil && strings.TrimSpace(string(out)) == migratedHereOn {
			return true
		}
	}
	return false
}
