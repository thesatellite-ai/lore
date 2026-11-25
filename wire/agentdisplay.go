package wire

// agentdisplay.go — the identity.agent_display mode of this command (issue
// #113, devco:D-900; the modes are in adapters/identity/agentdisplay.go).
//
// It is decided inside resolveIdentity, the one place every command gets
// its identity from, so the actor row and migrated_via can never
// disagree (no git commit names the agent at all, devco:D-983). Identity resolves before any store is
// opened (the cache path and the engine depend on it), so the mode is read
// the way branchmode.go reads its policy: straight from the files, no
// cache, no git, no fetch. The sources, resolved by policy.Resolve
// (stricter wins, a lower level only hides more):
//
//	repo     <top>/.lore/data/policies: a client repo's committed
//	         `lore policy set --key identity.agent_display --value person
//	         --to repo` binds everyone who writes there
//	machine  ~/.lore/config.yaml `policies: {identity.agent_display: …}`
//	env      LORE_AGENT_DISPLAY (counted at machine level)
//
// Org, store-folder and store-root rows (`--to <store>/org`, a linked
// client repo's folder) live in store clones that only the open attaches.
// Once the engine exists, applyAgentDisplayPolicy reads the key from the
// engine's full policy set (every level), renames the caller's identity
// when it hides more, and raises the App's floor, so every later
// resolveIdentity of this command (the row's actor copy, migrated_via)
// gives the same answer.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// agentDisplayFloor is the strictest mode the opened engine's policy gave
// in this App ("" until an open read it).
type agentDisplayFloor struct {
	mu   sync.Mutex
	mode identity.AgentDisplay
}

// get is the floor ("" when none, or f is nil).
func (f *agentDisplayFloor) get() identity.AgentDisplay {
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

// raise keeps the stricter of the floor and m.
func (f *agentDisplayFloor) raise(m identity.AgentDisplay) {
	if f == nil || m == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mode == "" {
		f.mode = m
		return
	}
	f.mode = identity.StricterAgentDisplay(f.mode, m)
}

// applyAgentDisplayPolicy is the open's second look at identity.agent_display
// (file comment): the engine's policy for the repo target, every level. A
// policy that does not read is the engine's refusal on the write itself.
//
// Only a detected agent reads it: for anyone else the mode changes nothing,
// and the read lists the policies table of every attached store, so doing
// it on every open would make every person's command (a read included)
// list each store at open, before the command's own reads and writes.
func (st *state) applyAgentDisplayPolicy(ctx context.Context, opts Options) {
	if st.eng == nil || !needsAgentDisplayPolicy(st.who) {
		return
	}
	set, err := st.eng.Policy(ctx, store.SourceRepo)
	if err != nil {
		return
	}
	t, ok := set.Text(policy.KeyIdentityAgentDisplay)
	if !ok {
		return
	}
	m, ok := identity.ParseAgentDisplay(t)
	if !ok || !m.HidesTool() {
		return
	}
	opts.agentDisplay.raise(m)
	st.who = identity.WithAgentDisplay(st.who, m)
}

// needsAgentDisplayPolicy reports whether the open must read the engine's
// identity.agent_display: only for a detected agent whose name still says
// more than the strictest mode (person) would, as only such a caller's
// written prose can change.
func needsAgentDisplayPolicy(who identity.Resolved) bool {
	return who.Step == identity.StepDetected && who.Owner != nil && who.Detection != nil && who.AgentDisplay != identity.AgentDisplayPerson
}

// agentDisplayMode is the mode for opts and the notes worth one line (an
// env value outside the set). A policy that does not read leaves the
// default here: the engine's own policy read refuses the command with the
// row named before anything is written.
func agentDisplayMode(opts Options, getenv func(string) string) (identity.AgentDisplay, []string) {
	var entries []policy.Entry
	var notes []string
	if top := loreTopFrom(optsProjectDir(opts)); top != "" {
		if rows, err := readRepoPolicyDir(top); err == nil {
			if got, err := policyEntriesOf(policy.LevelRepo, rows); err == nil {
				entries = append(entries, got...)
			}
		}
	}
	if opts.Home != "" {
		if machine, _, err := machinePolicy(opts.Home); err == nil {
			entries = append(entries, machine...)
		}
	}
	if v := strings.TrimSpace(getenv(identity.EnvAgentDisplay)); v != "" {
		m, ok := identity.ParseAgentDisplay(v)
		if t, terr := value.TextOf(string(m)); ok && terr == nil {
			entries = append(entries, policy.Entry{Level: policy.LevelMachine, Key: policy.KeyIdentityAgentDisplay, Value: t})
		} else {
			notes = append(notes, identity.EnvAgentDisplay+"="+v+" ignored: use person, agent or tool")
		}
	}
	floor := opts.agentDisplay.get()
	set, err := policy.Resolve(entries)
	if err != nil {
		return identity.StricterAgentDisplay(identity.AgentDisplayTool, floor), notes
	}
	m := identity.AgentDisplayTool
	if t, ok := set.Text(policy.KeyIdentityAgentDisplay); ok {
		if pm, ok := identity.ParseAgentDisplay(t); ok {
			m = pm
		}
	}
	if floor != "" {
		m = identity.StricterAgentDisplay(m, floor)
	}
	return m, notes
}

// loreTopFrom is the nearest folder at or above dir holding lore data
// (.lore/data), "" when none does: the repo whose committed policies bind
// the writer. The process's folder is used when dir is "".
func loreTopFrom(dir string) string {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		dir = wd
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, loreFolderName, rowfs.DataDir)); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
