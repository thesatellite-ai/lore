package wire

import (
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
)

// yamlSyntaxStart begins yaml.v3's syntax errors (a value error names its
// line and key instead, through yamlerr).
const yamlSyntaxStart = "yaml: "

// configYAMLErr is the E_CONFIG_INVALID of a ~/.lore/config.yaml that does
// not decode, err being yamlerr.Decode's error: the problems (line, key
// path, what was expected) are in the message itself, because a person
// reads the message, not the wrapped cause. Only a syntax error is "not
// valid YAML".
func configYAMLErr(err error) error {
	what := " is not valid: "
	var ye *yamlerr.Error
	if errors.As(err, &ye) && strings.HasPrefix(errors.Unwrap(ye).Error(), yamlSyntaxStart) {
		what = " is not valid YAML: "
	}
	return errs.Wrap(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+what+err.Error(), err)
}
