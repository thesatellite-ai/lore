package host

// clientguard.go — lore's leak guard in a client repo a store links by
// its remote (J-11). Such a repo holds no .lore/ and lore wires nothing
// into it (S-2, F-14: no merge driver, no .gitattributes, no refresh
// hooks), so it had no hook at all: an internal rule sentence pasted
// into a file was committed with no check, while `lore org link` said
// "the leak guards turn on" and `lore whoami` said "guards on". Now the
// link command (org link, link add: the set-up commands S-2 lets write
// to the clone) installs the one block that guards commits:
//
//	org link / link add ok ─▶ no .lore/ here, the repo resolves client?
//	   ─▶ commit-msg: lore's block (`lore check leaks --staged
//	      --commit-msg`; with no lore that can check, the block refuses
//	      while lore.clientrepo is true)
//	   ─▶ git config --local lore.clientrepo true
//	   ─▶ one line naming what was written
//
// A hooks folder every repo shares (a global core.hooksPath) is left
// alone: lore's blocks there run only where .lore/ is, and the line says
// so. A teammate's clone gets the block when they run the link there
// (`lore whoami` says the guards are off until then).

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire"
)

// clientGuardOps are the operations whose success installs the guard in
// a linked client repo with no .lore/.
var clientGuardOps = map[string]bool{opsorg.OpLink: true, opslink.OpAdd: true}

// installClientGuard writes lore's commit-msg block into the clone at
// workDir when it holds no .lore/ and resolves as a client repo. Best
// effort: a failure is one warning line, never a refused link.
func (h *liveHost) installClientGuard(ctx context.Context, opName string) {
	if !clientGuardOps[opName] || h.cfg.ClientRepo == nil || liveProjectRoot(h.cfg.WorkDir) != "" {
		return
	}
	top, err := h.git().Run(ctx, gitref.Invocation{Dir: h.cfg.WorkDir, Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil || strings.TrimSpace(string(top)) == "" || !h.cfg.ClientRepo(ctx) {
		return
	}
	dir := strings.TrimSpace(string(top))
	hooks, err := h.git().Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}})
	if err != nil {
		h.line("lore: could not install the leak guard in this clone: " + err.Error())
		return
	}
	hooksDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(hooks))))
	if hp, herr := h.git().Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"config", "--show-scope", "--get", "core.hooksPath"}}); herr == nil {
		scope, val, _ := strings.Cut(strings.TrimSpace(string(hp)), "\t")
		if scope == gitScopeGlobal || scope == gitScopeSystem {
			h.line("lore: commits in this client repo are NOT checked for internal notes: git runs hooks from " + val + " (core.hooksPath in your " + scope + " git config) for every repo; add `lore check leaks --staged --commit-msg \"$1\"` to its commit-msg hook")
			return
		}
		if p := filepath.FromSlash(strings.TrimSpace(val)); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			hooksDir = p
		}
	}
	before, _ := os.ReadFile(filepath.Join(hooksDir, hookCommitMsg))
	b := hookBlocks("")[hookCommitMsg]
	if err := ensureHookBlock(hooksDir, hookCommitMsg, b.marker, b.lines); err != nil {
		h.line("lore: could not install the leak guard in this clone: " + err.Error())
		return
	}
	if _, err := h.git().Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"config", "--local", wire.ClientRepoGitKey, "true"}}); err != nil {
		h.line("lore: could not mark this clone as a client repo: " + err.Error())
	}
	if after, _ := os.ReadFile(filepath.Join(hooksDir, hookCommitMsg)); string(after) != string(before) {
		h.line("lore: leak guard installed in this clone: " + filepath.Join(hooksDir, hookCommitMsg) + " (commits are checked for internal notes; teammates get it when they run the same link in their clone)")
	}
}
