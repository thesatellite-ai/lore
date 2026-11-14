//go:build setupe2e

// Package setupe2e is the setup page's end-to-end suite: a fake machine
// (HOME, ~/.ssh, ~/.gitconfig, ~/.lore, fake gh/ssh/git, a fake GitHub
// API) built per Shape, the REAL lore binary serving `lore setup web` in
// it, and a matrix over the shapes: every valid shape through the page's
// HTTP API (matrix_test.go).
//
// Contract: the world JSON and shape fields below describe the suite's world; see docs/DEVELOPMENT.md (setup e2e suite).
package setupe2e

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Shape is one fake machine. Every field but push is required in the
// JSON; each is one dimension of the matrix, its values listed in the
// dims below. push may be left out (or "") and then means now, so the
// shapes written before it existed (web/e2e scripts, docs) still parse.
type Shape struct {
	// GHAccounts is how many logins gh holds: 0, 1 or 2.
	GHAccounts int `json:"gh_accounts"`
	// GHActive is which login gh reports active: "first" | "second"
	// ("second" only with 2 accounts).
	GHActive string `json:"gh_active"`
	// SSH is ~/.ssh's shape: none (no ~/.ssh) | default_only (only
	// ~/.ssh/id_ed25519, no config) | several (one alias per key in
	// ~/.ssh/config) | missing_key (one alias whose IdentityFile is gone).
	SSH string `json:"ssh"`
	// Keys is whether the private keys have their .pub beside them:
	// with_pub | without_pub.
	Keys string `json:"keys"`
	// IncludeIf is how many identities ~/.gitconfig adds in includeIf
	// blocks: 0, 1 or 2.
	IncludeIf int `json:"include_if"`
	// LoreHome is ~/.lore's state: empty | v0117 | setups | removed |
	// disabled | trashed | invalid_yaml.
	LoreHome string `json:"lore_home"`
	// Start is the folder `lore setup web` starts in: lore_repo | git_repo | plain.
	Start string `json:"start"`
	// Push is the push mode the person picks on the Stores step: now |
	// batch | manual. The walk picks it only when the store chosen there is
	// a git repo (a local folder or none has no remote; lore refuses a
	// mode other than manual for it), and the judge then expects config.yaml
	// personal.push to say it (nothing for manual, the default: C5). "" is
	// manual (PushMode).
	Push string `json:"push"`
}

// PushMode is the shape's push value, manual (the default) when it names
// none.
func (s Shape) PushMode() string {
	if s.Push == "" {
		return PushManual
	}
	return s.Push
}

// Dimension values, in matrix order. Each list is the closed set its
// Shape field accepts; Validate refuses anything else.
var (
	GHAccountsValues = []int{0, 1, 2}
	GHActiveValues   = []string{ActiveFirst, ActiveSecond}
	SSHValues        = []string{SSHNone, SSHDefaultOnly, SSHSeveral, SSHMissingKey}
	KeysValues       = []string{KeysWithPub, KeysWithoutPub}
	IncludeIfValues  = []int{0, 1, 2}
	LoreHomeValues   = []string{HomeEmpty, HomeV0117, HomeSetups, HomeRemoved, HomeDisabled, HomeTrashed, HomeInvalidYAML}
	StartValues      = []string{StartLoreRepo, StartGitRepo, StartPlain}
	PushValues       = []string{PushNow, PushBatch, PushManual}
)

// Dimension value names.
const (
	ActiveFirst  = "first"
	ActiveSecond = "second"

	SSHNone        = "none"
	SSHDefaultOnly = "default_only"
	SSHSeveral     = "several"
	SSHMissingKey  = "missing_key"

	KeysWithPub    = "with_pub"
	KeysWithoutPub = "without_pub"

	HomeEmpty       = "empty"
	HomeV0117       = "v0117"
	HomeSetups      = "setups"
	HomeRemoved     = "removed"
	HomeDisabled    = "disabled"
	HomeTrashed     = "trashed"
	HomeInvalidYAML = "invalid_yaml"

	StartLoreRepo = "lore_repo"
	StartGitRepo  = "git_repo"
	StartPlain    = "plain"

	PushNow    = "now"
	PushBatch  = "batch"
	PushManual = "manual"
)

// ParseShape decodes a shape JSON, refusing unknown fields, missing
// fields and invalid values.
func ParseShape(raw string) (Shape, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return Shape{}, fmt.Errorf("shape: %w", err)
	}
	for _, k := range []string{"gh_accounts", "gh_active", "ssh", "keys", "include_if", "lore_home", "start"} {
		if _, ok := probe[k]; !ok {
			return Shape{}, fmt.Errorf("shape: field %q is required", k)
		}
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Shape
	if err := dec.Decode(&s); err != nil {
		return Shape{}, fmt.Errorf("shape: %w", err)
	}
	return s, s.Validate()
}

// Validate reports a value outside its dimension or an invalid pair.
// Invalid pairs (the shape cannot exist on a real machine):
//   - gh_active second with fewer than 2 accounts;
//   - ssh none with keys without_pub (no keys at all, so the keys
//     dimension has one meaning there: with_pub is its canonical value).
func (s Shape) Validate() error {
	switch {
	case !contains(GHAccountsValues, s.GHAccounts):
		return fmt.Errorf("shape: gh_accounts %d not in %v", s.GHAccounts, GHAccountsValues)
	case !contains(GHActiveValues, s.GHActive):
		return fmt.Errorf("shape: gh_active %q not in %v", s.GHActive, GHActiveValues)
	case !contains(SSHValues, s.SSH):
		return fmt.Errorf("shape: ssh %q not in %v", s.SSH, SSHValues)
	case !contains(KeysValues, s.Keys):
		return fmt.Errorf("shape: keys %q not in %v", s.Keys, KeysValues)
	case !contains(IncludeIfValues, s.IncludeIf):
		return fmt.Errorf("shape: include_if %d not in %v", s.IncludeIf, IncludeIfValues)
	case !contains(LoreHomeValues, s.LoreHome):
		return fmt.Errorf("shape: lore_home %q not in %v", s.LoreHome, LoreHomeValues)
	case !contains(StartValues, s.Start):
		return fmt.Errorf("shape: start %q not in %v", s.Start, StartValues)
	case !contains(PushValues, s.PushMode()):
		return fmt.Errorf("shape: push %q not in %v", s.Push, PushValues)
	case s.GHActive == ActiveSecond && s.GHAccounts < 2:
		return fmt.Errorf("shape: gh_active second needs 2 gh accounts, have %d", s.GHAccounts)
	case s.SSH == SSHNone && s.Keys == KeysWithoutPub:
		return fmt.Errorf("shape: ssh none has no keys, so keys must be %s", KeysWithPub)
	}
	return nil
}

// Name is a short stable id of the shape, used in test names and logs.
func (s Shape) Name() string {
	return fmt.Sprintf("gh%d-%s_ssh-%s_%s_inc%d_home-%s_start-%s_push-%s", s.GHAccounts, s.GHActive, s.SSH, s.Keys, s.IncludeIf, s.LoreHome, s.Start, s.PushMode())
}

// AllShapes is the full product of the machine dimensions minus the
// invalid pairs, in a stable order. Push is not multiplied in: each shape
// takes one push value, rotating with the sum of its dimension indexes,
// so every push value meets every value of every other dimension
// (TestShapeValidate) while the matrix stays 1764 shapes. The push
// choice only matters through the store the Stores step ends with, which
// the machine dimensions decide; tripling the suite for it would buy no
// pair the rotation misses.
func AllShapes() []Shape {
	var out []Shape
	for gi, g := range GHAccountsValues {
		for ai, a := range GHActiveValues {
			for si, ss := range SSHValues {
				for ki, k := range KeysValues {
					for ii, inc := range IncludeIfValues {
						for hi, h := range LoreHomeValues {
							for ti, st := range StartValues {
								push := PushValues[(gi+ai+si+ki+ii+hi+ti)%len(PushValues)]
								s := Shape{GHAccounts: g, GHActive: a, SSH: ss, Keys: k, IncludeIf: inc, LoreHome: h, Start: st, Push: push}
								if s.Validate() == nil {
									out = append(out, s)
								}
							}
						}
					}
				}
			}
		}
	}
	return out
}

func contains[T comparable](list []T, v T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
