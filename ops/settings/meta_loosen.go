package settings

// The loosening check of staged key rows (METADATA 3.8 RX7, 3.11; OV64):
// a meta_keys row that loses internal, sensitive or strict compared with
// HEAD must carry a NEW changed_by entry for that field, which only
// `lore meta edit` writes after its confirmation. A hand edit, script or
// bad merge that drops a flag without one is E_LEAK_BLOCKED.
//
//	pre-commit / landing / check --ci ─► wire.StagedKeyLoosenings (git:
//	  staged meta_keys files vs HEAD) ─► CheckKeyLoosening per file

import (
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// keyRowFlags is the part of a key row file the check reads.
type keyRowFlags struct {
	Name      string           `json:"name"`
	Internal  bool             `json:"internal"`
	Sensitive bool             `json:"sensitive"`
	Strict    bool             `json:"strict"`
	ChangedBy []changedByEntry `json:"changed_by"`
}

// flagOf is one protection field's value.
func (k keyRowFlags) flagOf(field string) bool {
	switch field {
	case mInternal:
		return k.Internal
	case mSensitive:
		return k.Sensitive
	case mStrict:
		return k.Strict
	}
	return false
}

// CheckKeyLoosening compares one key row file in HEAD (headDoc) with its
// staged version and returns E_LEAK_BLOCKED naming the key and the
// command to use when a protection was dropped without a new changed_by
// record. A file that does not decode is not judged here (meta lint and
// the import quarantine report it); a missing side is nothing to compare.
func CheckKeyLoosening(path string, headDoc, stagedDoc []byte) error {
	if len(headDoc) == 0 || len(stagedDoc) == 0 {
		return nil
	}
	var head, staged keyRowFlags
	if json.Unmarshal(headDoc, &head) != nil || json.Unmarshal(stagedDoc, &staged) != nil {
		return nil
	}
	old := map[string]bool{}
	for _, c := range head.ChangedBy {
		old[c.ID] = true
	}
	var lost []string
	for _, f := range []string{mInternal, mSensitive, mStrict} {
		if !head.flagOf(f) || staged.flagOf(f) {
			continue
		}
		recorded := false
		for _, c := range staged.ChangedBy {
			if c.Field == f && !c.To && c.ID != "" && !old[c.ID] {
				recorded = true
				break
			}
		}
		if !recorded {
			lost = append(lost, f)
		}
	}
	if len(lost) == 0 {
		return nil
	}
	name := staged.Name
	if name == "" {
		name = head.Name
	}
	use := make([]string, 0, len(lost))
	for _, f := range lost {
		use = append(use, "--no-"+f)
	}
	return errs.New(errs.LeakBlocked, path+": "+name+" lost "+strings.Join(lost, ": true, ")+": true without a changed_by record").
		WithHint("use: lore meta edit " + name + " " + strings.Join(use, " "))
}
