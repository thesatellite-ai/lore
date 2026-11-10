package wire

// config_setuprename.go — setup.rename on ~/.lore/config.yaml: the
// setups[] entry takes the new name, and every repo_setups pin that named
// the old one names the new one. Everything else of the entry (unknown
// keys included) stays byte for byte; the candidate file is validated
// before anything is written (editSetups).

import (
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// RenameSetup renames the setups[] entry from to to and moves its repo
// pins. found is false when no setup is named from. E_INVALID_INPUT for a
// malformed new name, E_SETUP_NAME_TAKEN when a setup named to exists.
func RenameSetup(home, from, to string) (found bool, err error) {
	if !onboard.ValidSetupName(to) {
		return false, errs.New(errs.InvalidInput, "setup rename: "+strconv.Quote(to)+" is not a valid setup name").
			WithHint("snake_case or kebab-case, e.g. personal-old")
	}
	err = editSetups(home, func(root *yaml.Node) error {
		seq := mapGet(root, configKeySetups)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return nil
		}
		i := setupIndex(seq, from)
		if i < 0 {
			return nil
		}
		found = true
		if from == to {
			return nil
		}
		if setupIndex(seq, to) >= 0 {
			return errs.New(errs.SetupNameTaken, "a setup named "+strconv.Quote(to)+" exists; pick another name").
				WithHint("lore setup list shows the names in use")
		}
		mapSet(seq.Content[i], configKeyName, scalar(to))
		if pm := mapGet(root, configKeyRepoSetups); pm != nil && pm.Kind == yaml.MappingNode {
			for j := 1; j < len(pm.Content); j += 2 {
				if pm.Content[j].Value == from {
					pm.Content[j] = scalar(to)
				}
			}
		}
		return nil
	})
	return found, err
}
