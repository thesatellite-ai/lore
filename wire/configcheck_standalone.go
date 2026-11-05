package wire

// configcheck_standalone.go — cmd/lore's config.yaml schema (wire.ConfigSchema,
// Options.Local.Config): the core-native form of the old binary's
// configSchemaPort (saas/cmd/cli/config_port.go), so `lore config check`
// and the validation step of `lore config set` work on cmd/lore instead of
// answering E_UNSUPPORTED.
//
//	config check ─► read <home>/config.yaml (absent: ✓, no orgs)
//	             ─► orgconf.Parse (syntax, types, every org/settings/profile rule)
//	             ─► store.lock_timeout (readLockTimeout) + policies: (CheckMachinePolicy)
//	             ─► problems: E_CONFIG_INVALID naming the file and every problem
//	             ─► valid: orgs + warnings (unknown keys, live kinds that are not tables)
//
// The rules themselves live in logic/orgconf and the wire readers of
// store.lock_timeout and policies:, the same code every other command uses
// to read the file, so check never disagrees with what a command refuses.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	opsconfig "github.com/thesatellite-ai/lore/ops/config"
	"github.com/thesatellite-ai/lore/schema"
)

// standaloneConfig is wire.ConfigSchema for a binary that carries no
// v0.1.17 lorecfg (cmd/lore); home is the ~/.lore folder.
type standaloneConfig struct{ home string }

// configKeys is every top-level key lore reads: orgconf.Config plus the
// two blocks the wire reads itself (policies:, store:). Decoding into it
// with KnownFields reports a typo such as `org_foldr` as an unknown key.
type configKeys struct {
	orgconf.Config `yaml:",inline"`
	Policies       map[string]yaml.Node `yaml:"policies,omitempty"`
	Store          struct {
		LockTimeout string `yaml:"lock_timeout,omitempty"`
	} `yaml:"store,omitempty"`
}

// unknownKeyNeedle marks yaml.v3's KnownFields error for a key that is
// not a field of the target struct.
const unknownKeyNeedle = "not found in type"

// ValidateConfig reports unknown keys, a value of the wrong type and a
// broken rule of a candidate config.yaml (`lore config set`).
func (standaloneConfig) ValidateConfig(_ context.Context, raw []byte) (opsconfig.Verdict, error) {
	v := opsconfig.Verdict{Unknown: configUnknownKeys(raw)}
	var c orgconf.Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		v.Decode = err
		return v, nil
	}
	v.Invalid = c.Validate()
	return v, nil
}

// CheckConfig validates <home>/config.yaml and lists every problem at once
// (`lore config check`), as v0.1.17 did.
func (s standaloneConfig) CheckConfig(_ context.Context) (opsconfig.CheckOut, error) {
	path := configPath(s.home)
	raw, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return opsconfig.CheckOut{Path: path}, nil
	case err != nil:
		return opsconfig.CheckOut{}, errs.Wrap(errs.Internal, "read "+path, err)
	}
	warnings := configUnknownKeys(raw)
	var problems []string
	cfg, perr := orgconf.Parse(raw)
	if perr != nil {
		problems = append(problems, splitConfigProblems(perr)...)
	}
	// The blocks the wire reads itself, whenever the file is YAML at all
	// (a syntax error is reported once, by the parse).
	var node yaml.Node
	if yaml.Unmarshal(raw, &node) == nil {
		if _, lerr := readLockTimeout(s.home); lerr != nil {
			problems = append(problems, errMessage(lerr))
		}
		notices, merr := CheckMachinePolicy(s.home)
		warnings = append(warnings, notices...)
		if merr != nil {
			problems = append(problems, errMessage(merr))
		}
	}
	if len(problems) > 0 || cfg == nil {
		msg := path + " is not valid: " + strings.Join(problems, "; ")
		if len(warnings) > 0 {
			msg += "; warnings: " + strings.Join(warnings, "; ")
		}
		return opsconfig.CheckOut{}, errs.New(errs.ConfigInvalid, msg).
			WithHint("fix the problems listed; nothing else in lore reads an invalid config")
	}
	warnings = append(warnings, configLiveKindWarnings(cfg)...)
	return opsconfig.CheckOut{Path: path, Exists: true, Orgs: cfg.OrgNames(), Warnings: warnings}, nil
}

// errMessage is a problem's text without its code (the check's own error
// carries E_CONFIG_INVALID once).
func errMessage(err error) string {
	var pe *errs.Error
	if errors.As(err, &pe) {
		return pe.Message
	}
	return err.Error()
}

// splitConfigProblems flattens orgconf's joined validation error into one
// problem per line.
func splitConfigProblems(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		out = []string{err.Error()}
	}
	return out
}

// configUnknownKeys reports keys lore does not read, with their line. Lore
// tolerates them on load, so they are warnings, not problems; a syntax
// error is left to the parse.
func configUnknownKeys(raw []byte) []string {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var c configKeys
	err := dec.Decode(&c)
	var te *yaml.TypeError
	if err == nil || !errors.As(err, &te) {
		return nil
	}
	var out []string
	for _, e := range te.Errors {
		if strings.Contains(e, unknownKeyNeedle) {
			out = append(out, "unknown key, ignored: "+e)
		}
	}
	return out
}

// configLiveKindWarnings flags live.defaults keys that are not stored
// tables (a kind lore will never make live).
func configLiveKindWarnings(cfg *orgconf.Config) []string {
	known := map[string]bool{}
	for _, t := range schema.Builtin() {
		if !t.MachineLocal {
			known[string(t.Name)] = true
		}
	}
	check := func(where string, l *orgconf.Live) []string {
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
	for i := range cfg.Orgs {
		if o := &cfg.Orgs[i]; o.Live != nil {
			out = append(out, check("orgs["+o.Name+"].live", o.Live)...)
		}
	}
	return out
}
