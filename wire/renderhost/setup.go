// setup.go (was saas/cmd/cli session_setup.go) — `lore setup --agent-hooks [--dry-run] [--remove]
// [--yes]` (AGENT_IDENTITY_DESIGN 6.4): install or remove the shipped
// session-hook snippets for the tools set up in this repo.
//
//	setup.run (ops/setup) ─► setupHost.PlanAgentHooks
//	  ├─ plan: every toolprofile built-in, in its order (TOOL NEUTRAL)
//	  │    ├─ confirmed hook + its config folder exists → add / remove / already
//	  │    ├─ confirmed hook, folder missing          → "not set up here"
//	  │    └─ no confirmed hook                       → nothing to install
//	  ├─ (the operation prints, stops on --dry-run, asks: ops/setup)
//	  └─ SetupHost.WriteAgentHooks: rewrite each settings file, keeping every other key as it was

package renderhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/value"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// hookSettingsMode is the mode of a settings file lore creates.
const hookSettingsMode = 0o644

// hookCommandType is the hook entry type of a command hook.
const hookCommandType = "command"

// hookPlan is one line of the plan.
type hookPlan struct {
	tool, file, note string
	hook             toolprofile.SessionHook
	change           bool // the file is rewritten
}

// SetupHost is the agent-hooks half of `lore setup` (opssetup.Host); the
// schema half is the binary's (UpdateSchema), because the old binary's is
// a lore.db migration and cmd/lore has no lore.db (ops/setup,
// wire.ContextHosts.Setup; P3-MISC-4). The operation prints, asks and
// decides; these methods only plan and write.
type SetupHost struct {
	h *Host
	// Schema is UpdateSchema (nil: nothing to update, no lines).
	Schema func(ctx context.Context, quiet bool) ([]string, error)
}

// Setup is the SetupHost over h with schema as its schema half.
func (h *Host) Setup(schema func(ctx context.Context, quiet bool) ([]string, error)) *SetupHost {
	return &SetupHost{h: h, Schema: schema}
}

// UpdateSchema implements opssetup.Host.
func (s *SetupHost) UpdateSchema(ctx context.Context, quiet bool) ([]string, error) {
	if s.Schema == nil {
		return nil, nil
	}
	// One fetch per store for the whole update: a store's first open
	// fetches every ref, and the update's later sync and live-ref fetch
	// of that store reuse it (git.FetchScope).
	return s.Schema(git.WithFetchScope(ctx), quiet)
}

// PlanAgentHooks is the --agent-hooks plan of this git work tree. CQ5: the
// per-prompt search hook only when the policy (or --prompt-search for
// this machine) turns it on.
func (s *SetupHost) PlanAgentHooks(ctx context.Context, remove, promptSearch bool) (_ []opssetup.HookPlan, err error) {
	defer func() { err = s.h.hostError(err) }()
	root, err := agentHooksRoot(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.refuseClientRepo(ctx, remove); err != nil {
		return nil, err
	}
	if set, ok := s.h.policySet(ctx); ok {
		if e, found := set.Get(policy.KeyContextPromptSearch); found {
			if b, isBool := e.Value.(value.Bool); isBool && b.Bool() {
				promptSearch = true
			}
		}
	}
	plans, err := planAgentHooks(ctx, root, remove, promptSearch, s.sharedHooks(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]opssetup.HookPlan, 0, len(plans))
	for _, p := range plans {
		out = append(out, opssetup.HookPlan{Tool: p.tool, File: p.file, Note: p.note, Change: p.change, Hook: p.hook})
	}
	return out, nil
}

// WriteAgentHooks rewrites the settings file of every changed plan,
// keeping every other key as it was.
func (s *SetupHost) WriteAgentHooks(ctx context.Context, plans []opssetup.HookPlan, remove bool) (err error) {
	defer func() { err = s.h.hostError(err) }()
	root, err := agentHooksRoot(ctx)
	if err != nil {
		return err
	}
	// fmt1: the settings files are tracked; a project on the 0.1.17
	// format gets no tracked file written before lore migrate run (the
	// plan above still shows what would change)
	if wire.ProjectFormatOld(root) && slices.ContainsFunc(plans, func(p opssetup.HookPlan) bool { return p.Change }) {
		return wire.FormatOldErr(root)
	}
	return writeHookPlans(ctx, root, plans, remove, s.sharedHooks(ctx))
}

// writeHookPlans writes every changed plan into its settings file under
// root, moving lore's entry out of the tool's other file (D-732).
func writeHookPlans(ctx context.Context, root string, plans []opssetup.HookPlan, remove, shared bool) error {
	for _, p := range plans {
		if !p.Change {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(p.Hook.ConfigFile))
		if has, herr := hookInstalled(path, p.Hook); herr != nil {
			return herr
		} else if has == remove {
			if err := writeHookFile(root, path, p.Hook, remove); err != nil {
				return err
			}
			// D-743: taking lore's last entry out leaves no bare {} behind
			if remove {
				if err := removeIfEmpty(path); err != nil {
					return err
				}
			}
		}
		if !remove && p.Hook.ConfigFile == toolprofile.ClaudeLocalSettings {
			if err := excludePersonal(ctx, root, p.Hook.ConfigFile); err != nil {
				return err
			}
		}
		// D-732: the same entry in the tool's other file (an older lore's
		// .claude/settings.json, or the mode switched) moves: it leaves
		// that file, the person's own entries there stay as they were.
		if err := dropFromOther(ctx, root, p.Hook, remove, shared); err != nil {
			return err
		}
	}
	return nil
}

// sharedHooks reports whether the team opted in to committed agent hooks
// (context.hooks.agent_hooks shared, D-732); personal otherwise.
func (s *SetupHost) sharedHooks(ctx context.Context) bool {
	set, ok := s.h.policySet(ctx)
	if !ok {
		return false
	}
	mode, _ := set.Text(policy.KeyContextAgentHooks)
	return mode == policy.AgentHooksShared
}

// dropFromOther removes lore's entry for h from the settings file h is not
// placed in for the mode, when it is there (D-732). A file left holding
// nothing ({}) is removed, tracked or not: it held only lore's hooks, so
// lore made it (D-743: a bare {} was left in a committed settings.json).
// On a move (not remove) a file git tracks that holds more than lore's
// hooks is the team's and is left untouched (otherKept); --remove asks
// for lore's entries out of every file, so it takes them out there too.
func dropFromOther(ctx context.Context, root string, h toolprofile.SessionHook, remove, shared bool) error {
	other := h.OtherFile(shared)
	if other == "" {
		return nil
	}
	path := filepath.Join(root, filepath.FromSlash(other))
	has, err := hookInstalled(path, h)
	if err != nil || !has {
		return err
	}
	if !remove {
		if kept, kerr := otherKept(ctx, root, path); kerr != nil || kept {
			return kerr
		}
	}
	moved := h
	moved.ConfigFile = other
	if err := writeHookFile(root, path, moved, true); err != nil {
		return err
	}
	return removeIfEmpty(path)
}

// otherKept reports whether the other settings file at path stays as it
// is when lore's hooks move out of it (D-743): git tracks it and it holds
// more than lore's hooks (the team's permissions, their own hooks), so
// rewriting it would change, and reformat, a committed file of theirs.
// A tracked file holding only lore's hooks is lore's and is not kept.
func otherKept(ctx context.Context, root, path string) (bool, error) {
	doc, err := readSettings(path)
	if err != nil {
		return false, err
	}
	if onlyLoreHooks(doc) {
		return false, nil
	}
	return gitTracks(ctx, root, path)
}

// keptOtherNote is the plan's note on a hook whose older entry stays in a
// kept other file (otherKept).
const keptOtherNote = "lore's entry in %s stays: git tracks that file and it holds other settings"

// onlyLoreHooks reports whether doc holds nothing but lore's hook entries
// (every command one of the hook table's, for any tool and with the prompt
// hook on): the shape of a settings file lore created.
func onlyLoreHooks(doc settingsDoc) bool {
	for k := range doc {
		if k != "hooks" {
			return false
		}
	}
	raw, ok := doc["hooks"]
	if !ok {
		return true
	}
	var hooks map[string]json.RawMessage
	if json.Unmarshal(raw, &hooks) != nil {
		return false
	}
	lore := loreHookCommands()
	for _, evRaw := range hooks {
		var groups []hookGroup
		if json.Unmarshal(evRaw, &groups) != nil {
			return false
		}
		for _, g := range groups {
			for k := range g {
				if k != "hooks" && k != "matcher" {
					return false
				}
			}
			var es []hookEntry
			if gr, has := g["hooks"]; has && json.Unmarshal(gr, &es) != nil {
				return false
			}
			for _, e := range es {
				var cmd string
				if cr, has := e["command"]; !has || json.Unmarshal(cr, &cmd) != nil || !lore[strings.TrimSpace(cmd)] {
					return false
				}
			}
		}
	}
	return true
}

// loreHookCommands is every command line lore's hook table writes.
func loreHookCommands() map[string]bool {
	out := map[string]bool{}
	all := append(append(toolprofile.SessionHooks(), toolprofile.SessionEndHooks()...), toolprofile.ContextHooks(true)...)
	for _, h := range all {
		out[h.Command()] = true
	}
	return out
}

// excludePersonal keeps the personal settings file rel (Claude Code's
// settings.local.json) out of `git status` (D-732): Claude Code adds it
// to git's ignore list only when Claude Code creates it, so a file lore
// creates first would show as untracked. A file git already ignores or
// tracks is left as it is; the line goes in the clone's info/exclude,
// never in a tracked .gitignore.
func excludePersonal(ctx context.Context, root, rel string) error {
	if tracked, err := gitTracks(ctx, root, rel); err != nil || tracked {
		return err
	}
	if _, err := runGit(ctx, false, "-C", root, "check-ignore", "-q", "--", rel); err == nil {
		return nil
	}
	return excludeInGitAs(ctx, root, rel, "# lore: personal agent hooks (Claude Code's local settings, never committed)")
}

// removeIfEmpty removes the settings file at path when lore just took its
// last entry out and it holds an empty JSON object (D-743): lore made it,
// and a bare {} is neither a setting nor anything a person wrote. A
// tracked one goes too; its deletion is the change to commit. A settings
// file that is a link is left (the link is the person's).
func removeIfEmpty(path string) error {
	doc, err := readSettings(path)
	if err != nil || len(doc) > 0 {
		return err
	}
	if fi, lerr := os.Lstat(path); lerr != nil || fi.Mode()&os.ModeSymlink != 0 {
		return nil // gone already, or a link: nothing of lore's to remove
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return wire.WriteError("remove", path, err, renderWriteHint)
	}
	return nil
}

// refuseClientRepo refuses to add hooks in a client repo (devco D-494):
// every hook goes in a settings file inside the checkout (.claude/,
// .codex/, .gemini/), and a client's repo holds nothing of lore's, not
// even a git-excluded file (the private render writes no import file
// there either). Taking lore's hooks out (remove) stays allowed.
func (s *SetupHost) refuseClientRepo(ctx context.Context, remove bool) error {
	if remove || s.h.app == nil {
		return nil
	}
	client, err := s.h.app.ClientRepo(ctx)
	if err != nil || !client {
		return nil // not known as a client repo: the usual plan decides
	}
	e := errs.New(errs.InvalidInput, "lore setup --agent-hooks writes nothing in a client repo: every agent hook goes in a settings file inside the checkout, and a client's repo never holds lore files")
	if why := s.h.app.UnlistedOwnHere(ctx); why != "" {
		// devco D-525: client only by default (the org's own repo never
		// listed under own:): say so and name the way out first
		return e.WithHint(why + "; until then " + clientHooksHint)
	}
	return e.WithHint(clientHooksHint)
}

// clientHooksHint is how an agent gets lore's notes in a client repo.
const clientHooksHint = "an agent reads them with `lore context --for-file <path>` or `lore context --for-prompt <text>`; " +
	"for the always-on rules at each session start, put `" + clientSessionStartCmd + "` as a SessionStart hook in your user-level tool settings (outside the repo, such as ~/.claude/settings.json)"

// clientSessionStartCmd is the SessionStart context hook command for
// Claude Code (toolprofile.ContextHooks).
const clientSessionStartCmd = "lore context --hook " + toolprofile.NameClaudeCode + " --hook-stdin --session-start"

// agentHooksRoot is the top of the git work tree --agent-hooks writes in.
func agentHooksRoot(ctx context.Context) (string, error) {
	top, err := runGit(ctx, false, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errs.New(errs.InvalidInput, "lore setup --agent-hooks runs inside a git work tree").
			WithHint("cd into the repo whose hooks to install, then run it again")
	}
	return strings.TrimSpace(string(top)), nil
}

// planAgentHooks lists what --agent-hooks would do per built-in profile.
func planAgentHooks(ctx context.Context, root string, remove, promptSearch, shared bool) ([]hookPlan, error) {
	var out []hookPlan
	for _, prof := range toolprofile.Builtins() {
		h, ok := toolprofile.SessionHookFor(prof.Name)
		if !ok {
			if prof.Name == toolprofile.NameGeneric {
				continue
			}
			out = append(out, hookPlan{tool: prof.Name, file: "no confirmed session hook", note: "nothing to install (model stays \"not exposed\")"})
			continue
		}
		// CONTEXT_BUDGET 6.9: the session hook, the session-end hook (store
		// batches waiting at the end go out), then the tool's context hooks.
		hooks := []toolprofile.SessionHook{h}
		if end, ok := toolprofile.SessionEndHookFor(prof.Name); ok {
			hooks = append(hooks, end)
		}
		for _, hk := range append(hooks, toolprofile.ContextHooksFor(prof.Name, promptSearch)...) {
			p, err := planOneHook(ctx, root, prof.Name, hk.In(shared), remove, shared)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// planOneHook plans one hook of one tool.
func planOneHook(ctx context.Context, root, tool string, h toolprofile.SessionHook, remove, shared bool) (hookPlan, error) {
	path := filepath.Join(root, filepath.FromSlash(h.ConfigFile))
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return hookPlan{tool: tool, file: h.ConfigFile, note: "not set up here (" + filepath.ToSlash(filepath.Dir(h.ConfigFile)) + "/ missing)"}, nil
	}
	has, err := hookInstalled(path, h)
	if err != nil {
		return hookPlan{}, err
	}
	inOther, keptNote := false, ""
	if other := h.OtherFile(shared); other != "" {
		otherPath := filepath.Join(root, filepath.FromSlash(other))
		if inOther, err = hookInstalled(otherPath, h); err != nil {
			return hookPlan{}, err
		}
		// D-743: a move leaves a kept other file as it is, so its entry is
		// no change to plan (every later run would plan it again)
		if inOther && !remove {
			kept, kerr := otherKept(ctx, root, otherPath)
			if kerr != nil {
				return hookPlan{}, kerr
			}
			if kept {
				inOther, keptNote = false, " ("+fmt.Sprintf(keptOtherNote, other)+")"
			}
		}
	}
	p := hookPlan{tool: tool, file: h.ConfigFile, hook: h}
	switch {
	case remove && inOther:
		p.note, p.change = "remove "+h.Event+" hook: "+h.Command()+" (also from "+h.OtherFile(shared)+")", true
	case !remove && inOther:
		// D-732: an entry an older lore wrote to the committed file
		p.note, p.change = "move "+h.Event+" hook from "+h.OtherFile(shared)+": "+h.Command(), true
	case remove && has:
		p.note, p.change = "remove "+h.Event+" hook: "+h.Command(), true
	case remove:
		p.note = "no lore hook installed"
	case has:
		p.note = "already installed"
	default:
		p.note, p.change = "add "+h.Event+" hook: "+h.Command(), true
	}
	p.note += keptNote
	return p, nil
}

// settingsDoc is a tool settings file kept key by key, so every key lore
// does not own is written back byte for byte.
type settingsDoc map[string]json.RawMessage

// hookGroup is one entry of an event's list ({"matcher": …, "hooks": [...]}).
type hookGroup map[string]json.RawMessage

// hookEntry is one hook of a group ({"type": "command", "command": …}).
type hookEntry map[string]json.RawMessage

// readSettings reads path ("" content = an empty document).
func readSettings(path string) (settingsDoc, error) {
	b, err := os.ReadFile(filepath.Clean(path)) // repo-relative settings file named by the hook table
	if os.IsNotExist(err) {
		return settingsDoc{}, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read "+path, err)
	}
	doc := settingsDoc{}
	if len(bytes.TrimSpace(b)) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, errs.Wrap(errs.InvalidInput, path+" is not a JSON object; fix it, then rerun", err)
	}
	return doc, nil
}

// eventGroups decodes doc.hooks[event].
func eventGroups(doc settingsDoc, event string) (map[string]json.RawMessage, []hookGroup, error) {
	hooks := map[string]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, nil, errs.Wrap(errs.InvalidInput, "the settings file's \"hooks\" is not an object", err)
		}
	}
	var groups []hookGroup
	if raw, ok := hooks[event]; ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, nil, errs.Wrap(errs.InvalidInput, "the settings file's hooks."+event+" is not a list", err)
		}
	}
	return hooks, groups, nil
}

// isLoreHook reports whether e runs h's command.
func isLoreHook(e hookEntry, h toolprofile.SessionHook) bool {
	var cmd string
	if raw, ok := e["command"]; !ok || json.Unmarshal(raw, &cmd) != nil {
		return false
	}
	return strings.TrimSpace(cmd) == h.Command()
}

// groupEntries decodes g.hooks (nil when absent or not a list).
func groupEntries(g hookGroup) []hookEntry {
	var es []hookEntry
	if raw, ok := g["hooks"]; ok && json.Unmarshal(raw, &es) == nil {
		return es
	}
	return nil
}

// hookInstalled reports whether path already runs h's command on h.Event.
func hookInstalled(path string, h toolprofile.SessionHook) (bool, error) {
	doc, err := readSettings(path)
	if err != nil {
		return false, err
	}
	_, groups, err := eventGroups(doc, h.Event)
	if err != nil {
		return false, err
	}
	for _, g := range groups {
		for _, e := range groupEntries(g) {
			if isLoreHook(e, h) {
				return true, nil
			}
		}
	}
	return false, nil
}

// writeHookFile adds (or removes) lore's hook in path. Every object it
// rewrites keeps the user's key order (keysInOrder / marshalInOrder; a
// Go map would sort them), and a group it does not change keeps its bytes.
func writeHookFile(root, path string, h toolprofile.SessionHook, remove bool) error {
	// A settings file that is a link (or chain) is rewritten at its end, so
	// the link stays; one in the repo leading outside it is refused by name.
	path, err := wire.WriteThroughLink(root, path)
	if err != nil {
		return err
	}
	doc, err := readSettings(path)
	if err != nil {
		return err
	}
	orig, err := os.ReadFile(filepath.Clean(path)) // the same settings file readSettings read
	if err != nil && !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, "read "+path, err)
	}
	hooks, groups, err := eventGroups(doc, h.Event)
	if err != nil {
		return err
	}
	origHooks := doc["hooks"]
	var origGroups []json.RawMessage
	if raw, ok := hooks[h.Event]; ok {
		_ = json.Unmarshal(raw, &origGroups) // eventGroups decoded the same list
	}
	rawGroups := make([]json.RawMessage, len(groups))
	copy(rawGroups, origGroups)
	if remove {
		var kept []hookGroup
		var keptRaw []json.RawMessage
		for i, g := range groups {
			es := groupEntries(g)
			var rest []hookEntry
			for _, e := range es {
				if !isLoreHook(e, h) {
					rest = append(rest, e)
				}
			}
			if len(es) > 0 && len(rest) == 0 {
				continue // the group held only lore's hook
			}
			raw := rawGroups[i]
			if len(rest) != len(es) {
				b, merr := json.Marshal(rest)
				if merr != nil {
					return errs.Wrap(errs.Internal, "encode hooks", merr)
				}
				g["hooks"] = b
				if raw, merr = marshalInOrder(rawGroups[i], g); merr != nil {
					return errs.Wrap(errs.Internal, "encode hooks", merr)
				}
			}
			kept, keptRaw = append(kept, g), append(keptRaw, raw)
		}
		groups, rawGroups = kept, keptRaw
	} else {
		typ, _ := json.Marshal(hookCommandType)
		cmd, _ := json.Marshal(h.Command())
		entry := hookEntry{"type": typ, "command": cmd}
		if h.Timeout > 0 {
			entry["timeout"], _ = json.Marshal(h.Timeout)
		}
		entries, merr := json.Marshal([]hookEntry{entry})
		if merr != nil {
			return errs.Wrap(errs.Internal, "encode the hook", merr)
		}
		g := hookGroup{"hooks": entries}
		if h.Matcher != "" {
			m, _ := json.Marshal(h.Matcher)
			g["matcher"] = m
		}
		raw, merr := marshalInOrder(nil, g)
		if merr != nil {
			return errs.Wrap(errs.Internal, "encode the hook", merr)
		}
		groups, rawGroups = append(groups, g), append(rawGroups, raw)
	}
	if len(groups) == 0 {
		delete(hooks, h.Event)
	} else {
		b, merr := json.Marshal(rawGroups)
		if merr != nil {
			return errs.Wrap(errs.Internal, "encode hooks", merr)
		}
		hooks[h.Event] = b
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	} else {
		b, merr := marshalInOrder(origHooks, hooks)
		if merr != nil {
			return errs.Wrap(errs.Internal, "encode hooks", merr)
		}
		doc["hooks"] = b
	}
	compact, err := marshalInOrder(orig, doc)
	if err != nil {
		return errs.Wrap(errs.Internal, "encode "+path, err)
	}
	var b bytes.Buffer
	if err := json.Indent(&b, compact, "", "  "); err != nil {
		return errs.Wrap(errs.Internal, "encode "+path, err)
	}
	// A dangling link ends in a folder that may not exist yet.
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return wire.WriteError("create the folder of", path, err, renderWriteHint)
	}
	if err := os.WriteFile(path, append(b.Bytes(), '\n'), hookSettingsMode); err != nil {
		return wire.WriteError("write", path, err, renderWriteHint)
	}
	return nil
}

// keysInOrder lists the top-level keys of the JSON object orig in the
// order they are written ("" or not an object: none).
func keysInOrder(orig []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(orig))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		k, ok := tok.(string)
		if !ok {
			return keys
		}
		keys = append(keys, k)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// marshalInOrder writes m as a compact JSON object: the keys orig had, in
// orig's order, then the new keys sorted (a settings file is the user's;
// lore never reorders it).
func marshalInOrder(orig []byte, m map[string]json.RawMessage) ([]byte, error) {
	order := keysInOrder(orig)
	seen := make(map[string]bool, len(order))
	var rest []string
	for k := range m {
		rest = append(rest, k)
	}
	slices.Sort(rest)
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	put := func(k string) error {
		v, ok := m[k]
		if !ok || seen[k] {
			return nil
		}
		seen[k] = true
		kb, err := json.Marshal(k)
		if err != nil {
			return err
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.Write(kb)
		b.WriteByte(':')
		return json.Compact(&b, v)
	}
	for _, k := range append(order, rest...) {
		if err := put(k); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// inUntouchedRepo reports whether the command runs in a git repo that
// holds no lore data (wire.UntouchedRepo of the working folder): the one
// place CoreSchema has nothing to rebuild and nothing to say.
func inUntouchedRepo(ctx context.Context) bool {
	dir, err := os.Getwd()
	return err == nil && wire.UntouchedRepo(ctx, dir) != ""
}

// Lines of the core's schema half.
const (
	setupLineIndex = "→ building the search index from the row files..."
	setupLineDone  = "setup complete"
)

// CoreSchema is the schema half of `lore setup` for a binary with no
// lore.db (cmd/lore): the repo cache is built at run time from the table
// definitions, so there is no migration; the search index of every
// attached store is rebuilt from the row files (App.RebuildSearch). quiet
// (--schema-only after `lore upgrade`) reports nothing.
func (h *Host) CoreSchema(ctx context.Context, quiet bool) ([]string, error) {
	if _, err := h.app.RebuildSearch(ctx); err != nil {
		// D-20: started in a git repo with no lore data, the open refuses
		// (setup.run never sets a repo up, host.setsUpRepo): there is no
		// index of this repo to rebuild, and nothing is made there. Outside
		// any git repo the refusal stands: it names where to run it.
		if !wire.NoCreate(ctx) || !errs.Has(err, errs.NotAStore) || !inUntouchedRepo(ctx) {
			return nil, err
		}
	}
	if quiet {
		return nil, nil
	}
	return []string{h.decor.muted(setupLineIndex), h.decor.check() + " " + setupLineDone}, nil
}
