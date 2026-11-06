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
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
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
	if err := yamlerr.Decode(b, &doc, yamlerr.Lenient); err != nil {
		return nil, nil, configYAMLErr(err)
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
		e := policy.Entry{Level: policy.LevelMachine, Key: policy.Key(k), Value: v}
		// A value its key's shape refuses fails closed here, naming the
		// config key (policy.Resolve would name an empty row id).
		if _, rerr := policy.Resolve([]policy.Entry{e}); rerr != nil {
			return nil, nil, errs.New(errs.ConfigInvalid, "~/.lore/"+ConfigFile+": policies."+k+": "+policyProblem(rerr))
		}
		out = append(out, e)
	}
	return out, notes, nil
}

// policyRowPrefix starts policy.Resolve's messages ("policy row <id> …");
// a machine entry has no row id, so the prefix is dropped.
const policyRowPrefix = "policy row "

// policyProblem is the problem text of a policy.Resolve error.
func policyProblem(err error) string {
	msg := err.Error()
	var pe *errs.Error
	if errors.As(err, &pe) {
		msg = pe.Message
	}
	msg = strings.TrimSpace(strings.TrimPrefix(msg, policyRowPrefix))
	// The shape check's own "[E_CONFIG_INVALID] " is folded into the text.
	if strings.HasPrefix(msg, codeOpen) {
		if i := strings.Index(msg, codeClose); i > 0 {
			msg = msg[i+len(codeClose):]
		}
	}
	return msg
}

// codeOpen and codeClose bracket an errs code in an error text.
const (
	codeOpen  = "["
	codeClose = "] "
)

// CheckMachinePolicy validates the `policies:` map of <home>/config.yaml
// for `lore config check` (P3-CFG-6): the notices name the keys lore does
// not know (skipped, FA-AGENT-6); the error is E_CONFIG_INVALID for a
// value that is not plain or that its key's shape refuses.
func CheckMachinePolicy(home string) (notices []string, err error) {
	_, notices, err = machinePolicy(home)
	return notices, err
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
