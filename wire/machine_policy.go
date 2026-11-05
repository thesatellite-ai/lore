package wire

// machine_policy.go — this machine's stricter local policy values
// (CONFIG_GUIDE 4 step 1, 5.4, 7, CFG10; FQ11; inventory A-AGENT-8).
//
//	~/.lore/config.yaml  policies: {<key>: <value>, …}
//	  ─▶ machinePolicy: one policy.Entry per known key at LevelMachine
//	  ─▶ engine.WithMachinePolicy ─▶ policy.Resolve with the rows
//	     (stricter wins, OQ24: a local value can only tighten)
//
// DECISION FA-AGENT-6: the local values live under a top-level
// `policies:` map keyed by the policy key (e.g. meta.max_bytes: 4096), so
// they never collide with the other config.yaml keys. A key lore does not
// know is skipped with a notice (a typo on one machine must not stop every
// command); a known key with a value its shape refuses fails closed
// (E_CONFIG_INVALID from policy.Resolve).

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// machinePolicyDoc is the part of config.yaml this file reads.
type machinePolicyDoc struct {
	Policies map[string]yaml.Node `yaml:"policies"`
}

// machinePolicy reads the `policies:` map of <home>/config.yaml as
// entries at policy.LevelMachine, in key order; unknown keys are returned
// as notices. A missing file or map is no entry.
func machinePolicy(home string) ([]policy.Entry, []string, error) {
	b, err := os.ReadFile(filepath.Clean(configPath(home)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "wire: read "+ConfigFile, err)
	}
	var doc machinePolicyDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, nil, errs.Wrap(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" is not valid YAML", err)
	}
	keys := make([]string, 0, len(doc.Policies))
	for k := range doc.Policies {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out []policy.Entry
	var notes []string
	for _, k := range keys {
		if _, known := policy.Lookup(policy.Key(k)); !known {
			notes = append(notes, "~/.lore/"+ConfigFile+": policies."+k+" is not a policy key lore knows; ignored")
			continue
		}
		v, err := yamlValue(doc.Policies[k])
		if err != nil {
			return nil, nil, errs.Wrap(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" policies."+k+" is not a plain value", err)
		}
		out = append(out, policy.Entry{Level: policy.LevelMachine, Key: policy.Key(k), Value: v})
	}
	return out, notes, nil
}

// yamlValue turns one YAML value into the typed value a policies row holds
// (through JSON, as EntryFromRow reads a row's json value). Only plain
// values are accepted: a bool, a number, a string, or a list of strings.
func yamlValue(n yaml.Node) (value.Value, error) {
	var raw []byte
	var err error
	switch {
	case n.Kind == yaml.ScalarNode && n.Tag == "!!bool":
		var b bool
		if err = n.Decode(&b); err == nil {
			raw, err = json.Marshal(b)
		}
	case n.Kind == yaml.ScalarNode && n.Tag == "!!int":
		var i int64
		if err = n.Decode(&i); err == nil {
			raw, err = json.Marshal(i)
		}
	case n.Kind == yaml.ScalarNode && n.Tag == "!!float":
		var f float64
		if err = n.Decode(&f); err == nil {
			raw, err = json.Marshal(f)
		}
	case n.Kind == yaml.ScalarNode:
		var str string
		if err = n.Decode(&str); err == nil {
			raw, err = json.Marshal(str)
		}
	case n.Kind == yaml.SequenceNode:
		var list []string
		if err = n.Decode(&list); err == nil {
			raw, err = json.Marshal(list)
		}
	default:
		return nil, errors.New("a map is not a policy value")
	}
	if err != nil {
		return nil, err
	}
	return value.Infer(raw)
}

// MachinePolicy implements ops/settings.MachinePolicySource: this
// machine's local policy entries (notices are printed by open).
func (a *App) MachinePolicy(context.Context) ([]policy.Entry, error) {
	e, _, err := machinePolicy(a.opts.Home)
	return e, err
}
