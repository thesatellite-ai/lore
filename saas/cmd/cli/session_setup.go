// session_setup.go — `lore setup --agent-hooks [--dry-run] [--remove]
// [--yes]` (AGENT_IDENTITY_DESIGN 6.4): install or remove the shipped
// session-hook snippets for the tools set up in this repo.
//
//	setup.run (ops/setup) ─► setupHost.PlanAgentHooks
//	  ├─ plan: every toolprofile built-in, in its order (TOOL NEUTRAL)
//	  │    ├─ confirmed hook + its config folder exists → add / remove / already
//	  │    ├─ confirmed hook, folder missing          → "not set up here"
//	  │    └─ no confirmed hook                       → nothing to install
//	  ├─ (the operation prints, stops on --dry-run, asks: ops/setup)
//	  └─ setupHost.WriteAgentHooks: rewrite each settings file, keeping every other key as it was
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"

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

// setupHost is the machinery behind the setup.run operation
// (ops/setup, wire.ContextHosts.Setup; PASS3 ONE-PATH, P3-MISC-4): the
// lore.db schema update and the tools' hook files. The operation prints,
// asks and decides; these methods only plan and write.
type setupHost struct{}

// PlanAgentHooks is the --agent-hooks plan of this git work tree. CQ5: the
// per-prompt search hook only when the policy (or --prompt-search for
// this machine) turns it on.
func (setupHost) PlanAgentHooks(ctx context.Context, remove, promptSearch bool) (_ []opssetup.HookPlan, err error) {
	defer asHostError(&err)
	root, err := agentHooksRoot()
	if err != nil {
		return nil, err
	}
	if set, ok := policySet(ctx); ok {
		if e, found := set.Get(policy.KeyContextPromptSearch); found {
			if b, isBool := e.Value.(value.Bool); isBool && b.Bool() {
				promptSearch = true
			}
		}
	}
	plans, err := planAgentHooks(root, remove, promptSearch)
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
func (setupHost) WriteAgentHooks(_ context.Context, plans []opssetup.HookPlan, remove bool) (err error) {
	defer asHostError(&err)
	root, err := agentHooksRoot()
	if err != nil {
		return err
	}
	for _, p := range plans {
		if !p.Change {
			continue
		}
		if err := writeHookFile(filepath.Join(root, filepath.FromSlash(p.Hook.ConfigFile)), p.Hook, remove); err != nil {
			return err
		}
	}
	return nil
}

// agentHooksRoot is the top of the git work tree --agent-hooks writes in.
func agentHooksRoot() (string, error) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errcodes.New(errcodes.InvalidInput, "lore setup --agent-hooks runs inside a git work tree")
	}
	return strings.TrimSpace(string(top)), nil
}

// planAgentHooks lists what --agent-hooks would do per built-in profile.
func planAgentHooks(root string, remove, promptSearch bool) ([]hookPlan, error) {
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
		// CONTEXT_BUDGET 6.9: the session hook, then the tool's context hooks.
		for _, hk := range append([]toolprofile.SessionHook{h}, toolprofile.ContextHooksFor(prof.Name, promptSearch)...) {
			p, err := planOneHook(root, prof.Name, hk, remove)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// planOneHook plans one hook of one tool.
func planOneHook(root, tool string, h toolprofile.SessionHook, remove bool) (hookPlan, error) {
	path := filepath.Join(root, filepath.FromSlash(h.ConfigFile))
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return hookPlan{tool: tool, file: h.ConfigFile, note: "not set up here (" + filepath.ToSlash(filepath.Dir(h.ConfigFile)) + "/ missing)"}, nil
	}
	has, err := hookInstalled(path, h)
	if err != nil {
		return hookPlan{}, err
	}
	p := hookPlan{tool: tool, file: h.ConfigFile, hook: h}
	switch {
	case remove && has:
		p.note, p.change = "remove "+h.Event+" hook: "+h.Command(), true
	case remove:
		p.note = "no lore hook installed"
	case has:
		p.note = "already installed"
	default:
		p.note, p.change = "add "+h.Event+" hook: "+h.Command(), true
	}
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
		return nil, errcodes.New(errcodes.Internal, "read "+path).WithCause(err)
	}
	doc := settingsDoc{}
	if len(bytes.TrimSpace(b)) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, errcodes.New(errcodes.InvalidInput, path+" is not a JSON object; fix it, then rerun").WithCause(err)
	}
	return doc, nil
}

// eventGroups decodes doc.hooks[event].
func eventGroups(doc settingsDoc, event string) (map[string]json.RawMessage, []hookGroup, error) {
	hooks := map[string]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, nil, errcodes.New(errcodes.InvalidInput, "the settings file's \"hooks\" is not an object").WithCause(err)
		}
	}
	var groups []hookGroup
	if raw, ok := hooks[event]; ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, nil, errcodes.New(errcodes.InvalidInput, "the settings file's hooks."+event+" is not a list").WithCause(err)
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

// writeHookFile adds (or removes) lore's hook in path.
func writeHookFile(path string, h toolprofile.SessionHook, remove bool) error {
	doc, err := readSettings(path)
	if err != nil {
		return err
	}
	hooks, groups, err := eventGroups(doc, h.Event)
	if err != nil {
		return err
	}
	if remove {
		var kept []hookGroup
		for _, g := range groups {
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
			if len(rest) != len(es) {
				b, merr := json.Marshal(rest)
				if merr != nil {
					return errcodes.New(errcodes.Internal, "encode hooks").WithCause(merr)
				}
				g["hooks"] = b
			}
			kept = append(kept, g)
		}
		groups = kept
	} else {
		typ, _ := json.Marshal(hookCommandType)
		cmd, _ := json.Marshal(h.Command())
		entries, merr := json.Marshal([]hookEntry{{"type": typ, "command": cmd}})
		if merr != nil {
			return errcodes.New(errcodes.Internal, "encode the hook").WithCause(merr)
		}
		g := hookGroup{"hooks": entries}
		if h.Matcher != "" {
			m, _ := json.Marshal(h.Matcher)
			g["matcher"] = m
		}
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		delete(hooks, h.Event)
	} else {
		b, merr := json.Marshal(groups)
		if merr != nil {
			return errcodes.New(errcodes.Internal, "encode hooks").WithCause(merr)
		}
		hooks[h.Event] = b
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	} else {
		b, merr := json.Marshal(hooks)
		if merr != nil {
			return errcodes.New(errcodes.Internal, "encode hooks").WithCause(merr)
		}
		doc["hooks"] = b
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode "+path).WithCause(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), hookSettingsMode); err != nil {
		return errcodes.New(errcodes.Internal, "write "+path).WithCause(err)
	}
	return nil
}
