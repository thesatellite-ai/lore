// config_port.go — the config.yaml schema behind the core's
// `lore config set|check` operations (ops/config, P3-CFG-1): which keys
// lore reads and the validation of ~/.lore/config.yaml, from v0.1.17's
// lorecfg (LORE_TEAM_SPEC F19, CFG12). get/list/set themselves are core
// operations; this file only answers "is this file valid".
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"

	"github.com/thesatellite-ai/lore/model/errs"
	opsconfig "github.com/thesatellite-ai/lore/ops/config"
	"gopkg.in/yaml.v3"
)

// configSchemaPort is wire.ConfigSchema for this binary.
type configSchemaPort struct{}

// ValidateConfig reports unknown keys, a value of the wrong type and a
// broken rule of a candidate config.yaml (`lore config set`).
func (configSchemaPort) ValidateConfig(_ context.Context, raw []byte) (opsconfig.Verdict, error) {
	v := opsconfig.Verdict{Unknown: unknownKeyWarnings(raw)}
	var c lorecfg.Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		v.Decode = err
		return v, nil
	}
	v.Invalid = c.Validate()
	return v, nil
}

// CheckConfig validates ~/.lore/config.yaml and lists every problem at
// once (`lore config check`): an invalid file is E_CONFIG_INVALID naming
// each problem; a valid one lists its orgs and the warnings.
func (configSchemaPort) CheckConfig(ctx context.Context) (opsconfig.CheckOut, error) {
	dir, err := identity.Dir()
	if err != nil {
		return opsconfig.CheckOut{}, errs.Wrap(errs.Internal, "resolve HOME", err)
	}
	cfg, loc, err := lorecfg.LoadTeam(ctx, dir, lorecfg.Options{})
	if !loc.Exists && err == nil {
		return opsconfig.CheckOut{Path: loc.Path}, nil
	}
	var warnings []string
	if raw, rerr := os.ReadFile(loc.Path); rerr == nil {
		warnings = append(warnings, unknownKeyWarnings(raw)...)
	}
	if err != nil {
		// P3-CFG-3: the human and --json forms both carry every problem in
		// the error message (v0.1.17 printed them above the error line).
		msg := loc.Path + " is not valid: " + strings.Join(splitProblems(err), "; ")
		if len(warnings) > 0 {
			msg += "; warnings: " + strings.Join(warnings, "; ")
		}
		return opsconfig.CheckOut{}, errs.New(errs.ConfigInvalid, msg).
			WithHint("fix the problems listed; nothing else in lore reads an invalid config")
	}
	warnings = append(warnings, liveKindWarnings(cfg)...)
	return opsconfig.CheckOut{Path: loc.Path, Exists: true, Orgs: cfg.OrgNames(), Warnings: warnings}, nil
}

// splitProblems flattens the joined validation error into one line each.
func splitProblems(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "lorecfg: load") && strings.HasSuffix(line, "problem(s):") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		out = []string{err.Error()}
	}
	return out
}

// unknownKeyWarnings reports keys lore does not read (typos such as
// `org_foldr`), with their line. Lore tolerates them on load, as the TOML
// reader always did, so this is a warning, not an error.
func unknownKeyWarnings(raw []byte) []string {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var c lorecfg.Config
	err := dec.Decode(&c)
	if err == nil {
		return nil
	}
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return nil // syntax errors are reported by the load itself
	}
	var out []string
	for _, e := range te.Errors {
		if strings.Contains(e, "not found in type") {
			out = append(out, "unknown key, ignored: "+e)
		}
	}
	return out
}

// liveKindWarnings flags live.defaults keys that are not synced tables
// (a kind lore will never make live).
func liveKindWarnings(cfg *lorecfg.Config) []string {
	reg, _ := lsync.NewRegistry()
	known := map[string]bool{}
	for _, n := range reg.Names() {
		known[n] = true
	}
	check := func(where string, l *lorecfg.Live) []string {
		var out []string
		for k := range l.Defaults {
			if !known[k] {
				out = append(out, fmt.Sprintf("%s.defaults.%s: not a synced table (use plural table names such as tasks, memories)", where, k))
			}
		}
		sort.Strings(out)
		return out
	}
	out := check("live", &cfg.Live)
	for _, o := range cfg.Orgs {
		if o.Live != nil {
			out = append(out, check("orgs["+o.Name+"].live", o.Live)...)
		}
	}
	return out
}
