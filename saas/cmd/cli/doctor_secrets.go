package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/thesatellite-ai/lore/op"
	opsettings "github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
)

// envRemovedSyncAllow is the v0.1.17 escape hatch removed in 0.1.18 (D3):
// the export and the hooks always scan, so setting it does nothing.
const envRemovedSyncAllow = "LORE_SYNC_ALLOW_SECRETS"

// doctorSecretRules (OV4 leftovers, D2/D3/D9): users fully control the
// secret rules, so doctor lists every rule switched off at some level
// (a note each, with the command that turns it back on: what is NOT being
// scanned must be visible), and warns when the removed
// LORE_SYNC_ALLOW_SECRETS is still set, because a person relying on it
// believes secrets pass while every hook refuses them. envCheck is false
// under the doctor operation, which runs that warning in the core
// (wire.DoctorRemovedEnv), so it is printed once.
//
//	doctor → env check → secrets.rules (registry op) → state == off → note
func doctorSecretRules(ctx context.Context, r *doctorReport, envCheck bool) {
	if envCheck && os.Getenv(envRemovedSyncAllow) != "" {
		r.Warnings = append(r.Warnings, envRemovedSyncAllow+" is set but does nothing since 0.1.18 (the hooks always scan); unset it, and switch one rule off with `lore secrets rule off <id> --reason …` instead")
		degrade(r)
	}
	if coreApp == nil {
		return
	}
	e, ok := coreApp.Registry().Lookup(opsettings.OpSecretRules)
	if !ok {
		return
	}
	res, err := coreApp.Run(ctx, e, op.JSONDecoder{Body: []byte("{}")}, op.Caller{})
	if err != nil {
		r.Notes = append(r.Notes, "secret rules could not be read: "+err.Error())
		return
	}
	raw, err := res.JSON()
	if err != nil {
		return
	}
	var out opsettings.RulesOut
	if err := json.Unmarshal(raw, &out); err != nil {
		r.Notes = append(r.Notes, "secret rules could not be decoded: "+err.Error())
		return
	}
	for _, rule := range out.Data {
		if rule.State != schema.SecretRuleOff {
			continue
		}
		line := "secret rule " + rule.RuleID + " is off at " + rule.Level
		if rule.Reason != "" {
			line += " (" + rule.Reason + ")"
		}
		r.Notes = append(r.Notes, line+"; turn it back on: lore secrets rule on "+rule.RuleID+" --to "+rule.Store)
	}
}
