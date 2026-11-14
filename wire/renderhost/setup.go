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
	if set, ok := s.h.policySet(ctx); ok {
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
	for _, p := range plans {
		if !p.Change {
			continue
		}
		if err := writeHookFile(root, filepath.Join(root, filepath.FromSlash(p.Hook.ConfigFile)), p.Hook, remove); err != nil {
			return err
		}
	}
	return nil
}

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
		// CONTEXT_BUDGET 6.9: the session hook, the session-end hook (store
		// batches waiting at the end go out), then the tool's context hooks.
		hooks := []toolprofile.SessionHook{h}
		if end, ok := toolprofile.SessionEndHookFor(prof.Name); ok {
			hooks = append(hooks, end)
		}
		for _, hk := range append(hooks, toolprofile.ContextHooksFor(prof.Name, promptSearch)...) {
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
