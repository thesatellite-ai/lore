package wire

// personalall.go — every personal store of the person (owner item 9,
// reopened). A personal store is the person's, whatever setup covers the
// folder lore runs in, so `lore store list` and the setup-wide `lore sync`
// show and sync each one. Before, both took only the ACTIVE setup's `me`
// (effectivePersonalURL), and on a machine whose solo setup names its own
// store (setups[].personal_store: <url>, what the setup page writes; the
// top-level personal.store is never written from a setup) while a work
// setup covers the folder or is the default, `me` resolved to nothing (the
// work setup inherits an unset personal.store, or says personal_store:
// none) and the person's own store was left out of both.
//
//	ListEntries ─▶ ListStores (stores.yaml, the active setup's `me`)
//	            ─▶ OrgStoreEntries (the org's stores)
//	            ─▶ OtherPersonalEntries: personalStoreURLs
//	                 personal.store (top level)
//	                 each enabled setup's own personal_store url, config order
//	               minus the stores already listed (by store key), each
//	               named by its short name (git@host:you/notes-me.git ─▶
//	               notes-me), by its url when that name is taken
//
// Only store list and the setup-wide sync read it: `--to me` stays the
// active setup's store, and a store named here is not a --to target.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

var _ opsstores.PersonalLister = (*App)(nil)

// personalStoreURLs is every personal store url on this machine, once
// each (by store key), in order: personal.store, then each enabled
// setup's own personal_store url in config order. A broken config gives
// what could be read (doctor reports the config).
func personalStoreURLs(home string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		k, ok := storeKey(raw)
		if !ok {
			k = raw
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, raw)
		}
	}
	if mc, err := readMachineConfig(home); err == nil {
		add(mc.personalURL())
	}
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		return out
	}
	for _, s := range cfg.Setups {
		if s.Disabled {
			continue
		}
		switch p := strings.TrimSpace(s.PersonalStore); p {
		case "", onboard.PersonalStoreInherit, onboard.PersonalStoreNone:
		case onboard.PersonalStoreLocal:
			add(fileuri.URL(personalFolderDir(home), ""))
		default:
			add(p)
		}
	}
	return out
}

// OtherPersonalEntries implements opsstores.PersonalLister (see the file
// comment).
func (a *App) OtherPersonalEntries(_ context.Context, got []opsstores.Entry) ([]opsstores.Entry, error) {
	listed := map[string]bool{}
	taken := map[string]bool{}
	for _, e := range got {
		taken[e.Name] = true
		if k, ok := storeKey(e.URL); ok {
			listed[k] = true
		} else {
			listed[e.URL] = true
		}
	}
	var out []opsstores.Entry
	local := fileuri.URL(personalFolderDir(a.opts.Home), "")
	for _, raw := range personalStoreURLs(a.opts.Home) {
		k, ok := storeKey(raw)
		if !ok {
			k = raw
		}
		if listed[k] {
			continue
		}
		listed[k] = true
		name := personalEntryName(raw, raw == local, taken)
		if name == "" || taken[name] || (name != attach.ReservedMe && slices.Contains(attach.Reserved(), name)) {
			name = raw
		}
		taken[name] = true
		out = append(out, opsstores.Entry{Name: name, URL: raw, Personal: true, Source: opsstores.SourcePersonal})
	}
	return out, nil
}

// personalEntryName is the name store list and the setup-wide sync give
// the personal store at raw (OtherPersonalEntries): the local personal
// folder is the person's `me` store, named so while no listed store holds
// the name (devco D-279: outside any setup it was listed by its folder's
// name `_me`, which is no store name, so `lore sync` there FAILED with "a
// store needs a snake_case name"); any other store is named by its short
// name without the leading '_', '-' or '.' no store name may start with.
func personalEntryName(raw string, local bool, taken map[string]bool) string {
	if local && !taken[attach.ReservedMe] {
		return attach.ReservedMe
	}
	return strings.TrimLeft(strings.ToLower(orgconf.RepoName(raw)), "_-.")
}

// OrgSetupNames implements opsstores.SetupHinter: every enabled setup
// that names an org (its stores are what `store list --setup <name>`
// lists), in config order (devco D-554).
func (a *App) OrgSetupNames(_ context.Context) []string {
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range cfg.Setups {
		if !s.Disabled && strings.TrimSpace(s.Org) != "" {
			out = append(out, s.Name)
		}
	}
	return out
}

var _ opsstores.SetupHinter = (*App)(nil)
