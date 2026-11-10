package wire

// setupprobe_sotstores.go — the setup page and `lore doctor --all` side of
// SOURCE_OF_TRUTH rows 7 and 9: the checks store.address and org.source,
// whose Run it runs `lore store set-url <sto_id> <url>` and `lore org
// refresh <org>`. Both read this machine only (config.yaml, the address
// record, the stores' local copies); never the network.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// Probe details of the two checks.
const (
	detailOneAddress   = "every store of this setup has one address here"
	detailNoOrgEntry   = "the setup names no org entry"
	detailNoOrgCopy    = "the org store has no local copy yet (its first fetch makes one)"
	detailOrgSourceNow = "the entry copies the store's current lore-org.yaml"
)

// sourceOfTruthStores runs store.address or org.source for r's setup.
func (sp setupProber) sourceOfTruthStores(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	if r.Check.ID == onboard.CheckOrgSource {
		return sp.orgSourceProbe(ctx, r)
	}
	return sp.storeAddressProbe(r)
}

// setupStoreURLs are the store urls setup s owns: its personal store (its
// own, or the shared personal.store it inherits) and its org's stores.
func (sp setupProber) setupStoreURLs(s onboard.SetupEntry) []string {
	home := sp.a.opts.Home
	var out []string
	switch p := strings.TrimSpace(s.PersonalStore); p {
	case onboard.PersonalStoreNone:
	case "", onboard.PersonalStoreInherit:
		if mc, err := readMachineConfig(home); err == nil && mc.personalURL() != "" {
			out = append(out, mc.personalURL())
		}
	default:
		out = append(out, p)
	}
	if s.Org != "" {
		if cfg, _, err := orgcfg.LoadConfig(home); err == nil {
			if o := cfg.FindOrg(s.Org); o != nil {
				for _, sc := range o.Stores {
					out = append(out, sc.URL)
				}
			}
		}
	}
	return out
}

// storeAddressProbe fails when a store of the setup is reached at two
// addresses on this machine; its vars fill the Run it.
func (sp setupProber) storeAddressProbe(r opssetup.ProbeRequest) opssetup.ProbeResult {
	mine := sp.setupStoreURLs(r.Setup)
	for _, split := range storeaddr.Splits(addressEntries(sp.a.opts.Home, "")) {
		for _, e := range split.Entries {
			if !namesOld(mine, e.URL) {
				continue
			}
			return result(onboard.StatusFail, split.Line()+" ("+placesText(split.Entries)+")",
				map[string]string{onboard.VarStoreID: split.StoreID, onboard.VarURL: split.Primary})
		}
	}
	return result(onboard.StatusOK, detailOneAddress, nil)
}

// orgSourceProbe fails when the setup's org entry is older than, or
// (unstamped) differs from, the store's lore-org.yaml as last fetched.
func (sp setupProber) orgSourceProbe(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	if r.Setup.Org == "" {
		return skip(detailNoOrgEntry)
	}
	cfg, _, err := orgcfg.LoadConfig(sp.a.opts.Home)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	o := cfg.FindOrg(r.Setup.Org)
	if o == nil {
		return skip(detailNoOrgEntry)
	}
	sg, ok := orgSuggestionLocal(ctx, sp.a.opts.Home, cfg, o)
	if !ok {
		return skip(detailNoOrgCopy)
	}
	vars := map[string]string{onboard.VarOrg: o.Name}
	switch orgconf.StateOf(o, sg.StoreID, sg.Version) {
	case orgconf.SourceCurrent:
		return result(onboard.StatusOK, detailOrgSourceNow, nil)
	case orgconf.SourceStale:
		return result(onboard.StatusFail, orgFileShown(sg)+" is at version "+short(sg.Version)+", the entry copied "+short(o.Source.Version), vars)
	case orgconf.SourceUnstamped, orgconf.SourceOtherStore:
	}
	if orgconf.SharedOf(o).Equal(sg.Shared()) {
		return result(onboard.StatusOK, detailOrgSourceNow, nil)
	}
	return result(onboard.StatusFail, "the entry differs from "+orgFileShown(sg)+" and records no source version", vars)
}
