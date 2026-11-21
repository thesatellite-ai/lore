package host

// clientguard.go — lore's leak guard in a client repo a store links by
// its remote (J-11). Such a repo holds no .lore/ and lore wires nothing
// else into it (S-2, F-14: no merge driver, no .gitattributes, no refresh
// hooks). The link command (org link, link add: the set-up commands S-2
// lets write to the clone) and `lore doctor --fix` / `lore fix` there
// install the two blocks that guard what leaves the clone, through one
// installer (wireClientGuard):
//
//	org link / link add ok, doctor --fix, fix ─▶ no .lore/ here, client repo?
//	   ─▶ hooks folder of the clone (.git/hooks, or a local core.hooksPath
//	      outside the work tree): untracked, the repo's files stay
//	      byte-identical
//	      ─▶ commit-msg: lore's block (`lore check leaks --staged
//	         --commit-msg`; with no lore that can check, the block refuses
//	         while lore.clientrepo is true)
//	      ─▶ pre-push: lore's block (`lore sync hook pre-push`: the added
//	         lines of the pushed commits, so a --no-verify commit is still
//	         checked before it leaves, D-730)
//	   ─▶ git config --local lore.clientrepo true
//	   ─▶ one line naming what was written
//
// Never written (a line names the lines to add instead, and `lore doctor`
// warns until they are there):
//   - a hooks folder every repo shares (a global or system core.hooksPath):
//     lore's blocks there run only where .lore/ is;
//   - a hooks folder inside the work tree (core.hooksPath=.githooks, a
//     tracked folder): a change there is a change to the client's files,
//     which lore never makes, `lore fix --tracked-hooks` included (D-730).
//
// A teammate's clone gets the blocks when they run the link there
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
// a linked client repo with no .lore/ (doctor --fix and fix install it
// through doctorClientGuard).
var clientGuardOps = map[string]bool{opsorg.OpLink: true, opslink.OpAdd: true}

// clientGuardHookNames are the hooks the client guard needs, in install
// order: commit-msg checks each commit, pre-push the commits a push sends
// (a --no-verify commit skipped the first).
var clientGuardHookNames = []string{hookCommitMsg, hookPrePush}

// clientRepoMarkOn is lore.clientrepo's value in a marked clone (the
// value wire/clientmark.go writes and the commit-msg block reads).
const clientRepoMarkOn = "true"

// clientGuardHandLines are the lines a person adds by hand to a hook
// lore may not write (a shared or a tracked hooks folder), per hook. Each
// runs as printed inside that hook (git passes commit-msg the message
// file as $1, pre-push the remote as "$@" and the refs on stdin).
var clientGuardHandLines = map[string]string{
	hookCommitMsg: `lore check leaks --staged --commit-msg "$1" || exit $?`,
	hookPrePush:   `lore sync hook pre-push "$@" || exit $?`,
}

// clientGuardHandText names each hook with its hand line, for a warning.
func clientGuardHandText(hooks []string) string {
	parts := make([]string, 0, len(hooks))
	for _, n := range hooks {
		parts = append(parts, "`"+clientGuardHandLines[n]+"` to its "+n+" hook")
	}
	return strings.Join(parts, " and ")
}

// clientGuardBlock is lore's block for hook in a client repo's hooks
// folder: the commit-msg block of a lore project (hookBlocks), and the
// ride-along pre-push block, whose `lore sync hook pre-push` runs the
// leak check in a clone with no .lore/ too.
func clientGuardBlock(hook string) (marker string, lines []string) {
	if hook == hookPrePush {
		return rideHookMarker, rideHookLines[hookPrePush]
	}
	b := hookBlocks("")[hookCommitMsg]
	return b.marker, b.lines
}

// clientGuardPlace is where a client repo's guard goes: the clone's top
// folder and the hooks folder git runs. shared is set for a global or
// system core.hooksPath (scope names it), tracked for a hooks folder
// inside the work tree; lore writes into neither.
type clientGuardPlace struct {
	top, hooks string
	scope      string
	shared     bool
	tracked    bool
}

// clientGuardPlaceOf resolves the guard's place for the clone at
// h.cfg.WorkDir: ok is false when that is no client repo with no .lore/
// (a lore project wires its own hooks, gitwire.go).
func (h *liveHost) clientGuardPlaceOf(ctx context.Context) (clientGuardPlace, bool, error) {
	if h.cfg.ClientRepo == nil || liveProjectRoot(h.cfg.WorkDir) != "" {
		return clientGuardPlace{}, false, nil
	}
	top, err := h.git().Run(ctx, gitref.Invocation{Dir: h.cfg.WorkDir, Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil || strings.TrimSpace(string(top)) == "" || !h.cfg.ClientRepo(ctx) {
		return clientGuardPlace{}, false, nil
	}
	p := clientGuardPlace{top: strings.TrimSpace(string(top))}
	hooks, err := h.git().Run(ctx, gitref.Invocation{Dir: p.top, Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}})
	if err != nil {
		return p, true, err
	}
	p.hooks = filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(hooks))))
	if hp, herr := h.git().Run(ctx, gitref.Invocation{Dir: p.top, Args: []string{"config", "--show-scope", "--get", "core.hooksPath"}}); herr == nil {
		scope, val, _ := strings.Cut(strings.TrimSpace(string(hp)), "\t")
		if dir := filepath.FromSlash(strings.TrimSpace(val)); dir != "" {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(p.top, dir)
			}
			p.hooks = filepath.Clean(dir)
		}
		p.scope = scope
		p.shared = scope == gitScopeGlobal || scope == gitScopeSystem
	}
	p.tracked = !p.shared && hooksDirInWorkTree(p.top, p.hooks)
	return p, true, nil
}

// missing are the guard hooks in p's hooks folder without lore's block.
func (p clientGuardPlace) missing() []string {
	var out []string
	for _, n := range clientGuardHookNames {
		marker, _ := clientGuardBlock(n)
		b, err := os.ReadFile(filepath.Clean(filepath.Join(p.hooks, n)))
		if err != nil || !strings.Contains(string(b), marker) {
			out = append(out, n)
		}
	}
	return out
}

// notWritten is the line for a hooks folder lore may not write (shared or
// tracked), naming the hooks left unguarded and the lines to add; "" when
// lore may write there.
func (p clientGuardPlace) notWritten(hooks []string) string {
	what := "commits and pushes in this client repo are NOT checked for internal notes: "
	switch {
	case p.shared:
		return what + "git runs hooks from " + p.hooks + " (core.hooksPath in your " + p.scope + " git config) for every repo; add " + clientGuardHandText(hooks)
	case p.tracked:
		return what + "git runs hooks from " + p.hooks + ", a folder committed with the repo, and lore never changes a client repo's files; add " + clientGuardHandText(hooks) + " (or keep the hooks on this machine only: copy them into .git/hooks, run `git config --local core.hooksPath .git/hooks`, then `lore doctor --fix`)"
	}
	return ""
}

// installClientGuard installs the guard after a link command succeeded
// in a client repo with no .lore/. Best effort: a failure is one warning
// line, never a refused link.
func (h *liveHost) installClientGuard(ctx context.Context, opName string) {
	if !clientGuardOps[opName] {
		return
	}
	p, ok, err := h.clientGuardPlaceOf(ctx)
	if !ok {
		return
	}
	if err != nil {
		h.line("lore: could not install the leak guard in this clone: " + err.Error())
		return
	}
	for _, msg := range h.wireClientGuard(ctx, p) {
		h.line("lore: " + msg)
	}
}

// wireClientGuard writes the guard's missing blocks into p's hooks folder
// and marks the clone a client repo; the lines say what was written or
// why not. It writes nothing into a shared or tracked hooks folder.
func (h *liveHost) wireClientGuard(ctx context.Context, p clientGuardPlace) []string {
	missing := p.missing()
	if len(missing) == 0 {
		return nil
	}
	if msg := p.notWritten(missing); msg != "" {
		return []string{msg}
	}
	var out, wrote []string
	for _, n := range missing {
		marker, lines := clientGuardBlock(n)
		if err := hookRefused(p.top, p.hooks, n); err != nil {
			out = append(out, "could not install the leak guard in this clone: "+err.Error()) // D-502
			continue
		}
		if err := ensureHookBlock(p.hooks, n, marker, withHookPin(hookPinFor(ctx, p.top, p.hooks, false), lines)); err != nil {
			out = append(out, "could not install the leak guard in this clone: "+err.Error())
			continue
		}
		wrote = append(wrote, filepath.Join(p.hooks, n))
	}
	if _, err := h.git().Run(ctx, gitref.Invocation{Dir: p.top, Args: []string{"config", "--local", wire.ClientRepoGitKey, clientRepoMarkOn}}); err != nil {
		out = append(out, "could not mark this clone as a client repo: "+err.Error())
	}
	if len(wrote) > 0 {
		out = append(out, "leak guard installed in this clone: "+strings.Join(wrote, ", ")+" (commits and pushes are checked for internal notes; teammates get it when they run the same link in their clone)")
	}
	return out
}

// doctorClientGuard is doctor's check of the guard in a client repo with
// no .lore/ (D-730): a block missing from a clone the guard was on in is
// a warning naming the step that installs it (`lore doctor --fix`), or,
// in a hooks folder lore may not write, the lines to add by hand; --fix
// installs what it may. A teammate's clone the link never ran in is a
// note, not a warning (devco D-775): joining a team is not a degraded
// machine, and `lore whoami` there already says the guards are off.
func (h *liveHost) doctorClientGuard(ctx context.Context, fix bool, warn, note func(string)) {
	p, ok, err := h.clientGuardPlaceOf(ctx)
	if !ok || err != nil {
		return
	}
	missing := p.missing()
	if len(missing) == 0 {
		note("client repo leak guard installed in " + p.hooks + " (" + strings.Join(clientGuardHookNames, ", ") + ")")
		return
	}
	if msg := p.notWritten(missing); msg != "" {
		warn(msg)
		return
	}
	if !fix {
		if !h.clientGuardWasOn(ctx, p, missing) {
			note("client repo leak guard off in this clone (no lore block in " + strings.Join(missing, ", ") + " in " + p.hooks + "): commits and pushes here are not checked for internal notes until the link runs here or `lore doctor --fix` installs it (in .git/hooks only: the repo's files stay unchanged)")
			return
		}
		warn("client repo leak guard missing in " + strings.Join(missing, ", ") + " (" + p.hooks + "): commits or pushes here are not checked for internal notes; run `lore doctor --fix` to install it (in .git/hooks only: the repo's files stay unchanged)")
		return
	}
	for _, msg := range h.wireClientGuard(ctx, p) {
		warn(msg)
	}
}

// clientGuardWasOn reports whether the guard was ever installed in p's
// clone: the clone holds one of lore's guard blocks, or the clone-local
// lore.clientrepo mark only wireClientGuard sets in a clone with no
// .lore/ (wire/clientmark.go skips such a clone). A partial guard (a clone
// linked before the pre-push block existed, or a block someone removed)
// is a doctor warning; a clone the link never ran in is not (D-775).
func (h *liveHost) clientGuardWasOn(ctx context.Context, p clientGuardPlace, missing []string) bool {
	if len(missing) < len(clientGuardHookNames) {
		return true
	}
	out, err := h.git().Run(ctx, gitref.Invocation{Dir: p.top, Args: []string{"config", "--local", "--get", wire.ClientRepoGitKey}})
	return err == nil && strings.TrimSpace(string(out)) == clientRepoMarkOn
}
