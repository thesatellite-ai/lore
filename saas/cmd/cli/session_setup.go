// session_setup.go — `lore setup --agent-hooks [--dry-run] [--remove]
// [--yes]` (AGENT_IDENTITY_DESIGN 6.4): install or remove the shipped
// session-hook snippets for the tools set up in this repo.
//
//	runAgentHooksSetup
//	  ├─ plan: every toolprofile built-in, in its order (TOOL NEUTRAL)
//	  │    ├─ confirmed hook + its config folder exists → add / remove / already
//	  │    ├─ confirmed hook, folder missing          → "not set up here"
//	  │    └─ no confirmed hook                       → nothing to install
//	  ├─ --dry-run → print only
//	  ├─ confirm (terminal y/N, else E_NEEDS_CONFIRM with --yes as next)
//	  └─ rewrite each settings file, keeping every other key as it was
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Setup --agent-hooks flags.
const (
	flagAgentHooks = "agent-hooks"
	flagHooksRm    = "remove"
	flagHooksDry   = "dry-run"
	flagHooksYes   = "yes"
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

// runAgentHooksSetup is the --agent-hooks mode of `lore setup`.
func runAgentHooksSetup(remove, dryRun, yes bool) error {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, "lore setup --agent-hooks runs inside a git work tree")
	}
	root := strings.TrimSpace(string(top))
	plans, err := planAgentHooks(root, remove)
	if err != nil {
		return err
	}
	changes := 0
	for _, p := range plans {
		fmt.Printf("%-13s %-28s %s\n", p.tool, p.file, p.note)
		if p.change {
			changes++
		}
	}
	switch {
	case changes == 0:
		fmt.Println("Nothing to change.")
		return nil
	case dryRun:
		fmt.Println("Nothing written (dry run).")
		return nil
	}
	if !yes {
		q := fmt.Sprintf("Write %d settings file(s)?", changes)
		if !stdinIsTTY() {
			next := []string{"lore", "setup", "--" + flagAgentHooks, "--" + flagHooksYes}
			if remove {
				next = append(next, "--"+flagHooksRm)
			}
			return errs.New(errs.NeedsConfirm, "lore setup --agent-hooks changes the files listed above and there is no terminal to ask on").
				WithQuestion(q).WithHint("rerun with --yes to write them, or --dry-run to only show them").WithNext(next[0], next[1:]...)
		}
		if !confirmYes(q + " [y/N] ") {
			fmt.Println("Nothing written.")
			return nil
		}
	}
	for _, p := range plans {
		if !p.change {
			continue
		}
		if err := writeHookFile(filepath.Join(root, filepath.FromSlash(p.hook.ConfigFile)), p.hook, remove); err != nil {
			return err
		}
	}
	fmt.Printf("%s %d settings file(s) written\n", style.Success("✓"), changes)
	return nil
}

// planAgentHooks lists what --agent-hooks would do per built-in profile.
func planAgentHooks(root string, remove bool) ([]hookPlan, error) {
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
		path := filepath.Join(root, filepath.FromSlash(h.ConfigFile))
		if _, err := os.Stat(filepath.Dir(path)); err != nil {
			out = append(out, hookPlan{tool: prof.Name, file: h.ConfigFile, note: "not set up here (" + filepath.ToSlash(filepath.Dir(h.ConfigFile)) + "/ missing)"})
			continue
		}
		has, err := hookInstalled(path, h)
		if err != nil {
			return nil, err
		}
		p := hookPlan{tool: prof.Name, file: h.ConfigFile, hook: h}
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
		out = append(out, p)
	}
	return out, nil
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
	b, err := os.ReadFile(path) // repo-relative settings file named by the hook table
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
		groups = append(groups, hookGroup{"hooks": entries})
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
