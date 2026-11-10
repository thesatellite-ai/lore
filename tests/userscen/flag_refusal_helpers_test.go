//go:build userscen

package userscen

import (
	"encoding/json"
	"strings"
)

// cliFlagRefusal is the exact stderr of an unknown flag on the command
// `lore <path…>` (FLAG2): the usage line, then the hint naming where that
// command's flags are listed.
func cliFlagRefusal(path []string, flag string) string {
	cmd := strings.Join(append([]string{"lore"}, path...), " ")
	return "ERROR: [E_INVALID_INPUT] unknown flag: " + flag + "\n  hint: " + cmd + " --help lists its flags\n"
}

// cliFlagRefusedJSON reports whether r is the --json form of the same
// refusal (surface/cli usageError, 9efcfbc3): exit 20, nothing on stderr,
// and exactly one error object on stdout carrying E_INVALID_INPUT, the
// usage message, exit 20 and the hint naming the command's --help.
func cliFlagRefusedJSON(r cliProc, path []string, flag string) bool {
	cmd := strings.Join(append([]string{"lore"}, path...), " ")
	var doc cliErrorDoc
	raw, err := cliOneJSON(r.Stdout)
	if err == nil {
		err = json.Unmarshal(raw, &doc)
	}
	return err == nil && doc.Error != nil && r.Code == cliExitUsage && r.Stderr == "" &&
		doc.Error.Code == "E_INVALID_INPUT" && doc.Error.Exit == cliExitUsage &&
		doc.Error.Message == "unknown flag: "+flag && doc.Error.Hint == cmd+" --help lists its flags"
}
