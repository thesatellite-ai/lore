package wire

// configproblems.go — config.yaml's problems, each located, for Home
// (polish item 4, realmac #23). With `identity.key: mira` Home used to
// list the setups as if all was well while every setup failed to open with
// E_CONFIG_INVALID; setup.list now carries these problems and Home shows
// one panel naming the file, the line and the fix.
//
//	config.yaml ─▶ orgconf.Parse (identity, personal, orgs, repo_orgs …)
//	            ─▶ setupsDoc.problems (setups[], setup:, doctor:)
//	            ─▶ store.lock_timeout, policies
//	            ─▶ each problem: its key path → the line in the YAML node tree

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.ConfigChecker = (*App)(nil)

// Problem text shapes this file reads (orgconf, yamlerr and onboard write
// them): a leading key path such as identity.key, setups[personal] or
// orgs[0]; "line N" for a decode error; orgconf's own prefix.
var (
	problemPathRe = regexp.MustCompile(`^([a-z_]+(?:\[[^\]]*\])?(?:\.[a-z_]+(?:\[[^\]]*\])?)*)(?::| ")`)
	problemLineRe = regexp.MustCompile(`\bline (\d+)\b`)
	pathPartRe    = regexp.MustCompile(`^([a-z_]+)(?:\[([^\]]*)\])?$`)
	// problemLineLead is a decode error's leading "line N: ".
	problemLineLead = regexp.MustCompile(`^line \d+: `)
)

// orgconfPrefix starts orgconf's decode errors ("orgconf: config.yaml: …").
const orgconfPrefix = "orgconf: "

// yamlTabProblem is in yaml.v3's error for a tab in indentation.
const yamlTabProblem = "tab character"

// firstTabLine is the 1-based line of the first tab in raw's indentation
// (0 when none).
func firstTabLine(raw []byte) int {
	for i, l := range strings.Split(string(raw), "\n") {
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if strings.Contains(indent, "\t") {
			return i + 1
		}
	}
	return 0
}

// configCheckCmd is the command that lists every problem (the fix names it).
const configCheckCmd = "lore config check"

// humanKeyPrefix is the identity key's required prefix (TEAM-029).
const humanKeyPrefix = "human:"

// ConfigProblems implements opssetup.ConfigChecker: every problem `lore
// config check` reports for config.yaml, with its line and a fix; nil
// when the file is absent or valid. Reading problems never fails: a file
// that cannot be read is no problem to show here (the ops that need it
// report their own error).
func (a *App) ConfigProblems(context.Context) []opssetup.ConfigProblem {
	raw, err := readConfigRaw(a.opts.Home)
	if err != nil || raw == nil {
		return nil
	}
	var problems []string
	if _, perr := orgconf.Parse(raw); perr != nil {
		problems = append(problems, splitConfigProblems(perr)...)
	}
	var doc yaml.Node
	var root *yaml.Node
	if yaml.Unmarshal(raw, &doc) == nil {
		if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
			root = doc.Content[0]
		}
		if _, lerr := readLockTimeout(a.opts.Home); lerr != nil {
			problems = append(problems, errMessage(lerr))
		}
		if d, derr := decodeSetupsDoc(raw); derr == nil {
			problems = append(problems, d.problems()...)
			// SOURCE_OF_TRUTH row 1: reported, never a write refusal.
			problems = append(problems, keylessSetupProblems(d.Setups)...)
		} else {
			problems = append(problems, errMessage(derr))
		}
		if _, merr := CheckMachinePolicy(a.opts.Home); merr != nil {
			problems = append(problems, errMessage(merr))
		}
	}
	file := tildeHome(configPath(a.opts.Home), globHome())
	suggest := suggestedHumanKey(raw)
	out := make([]opssetup.ConfigProblem, 0, len(problems))
	seen := map[string]bool{}
	for _, p := range problems {
		p = cleanProblem(p)
		if p == "" || seen[p] {
			continue // the same problem from two readers is shown once
		}
		seen[p] = true
		key, line := locateProblem(root, p)
		if t := firstTabLine(raw); t > 0 && strings.Contains(p, yamlTabProblem) {
			line = t // yaml.v3 names the line before the tab's for this error
		}
		p = problemLineLead.ReplaceAllString(p, "") // the line shows beside it
		if key != "" && strings.HasPrefix(p, key) {
			// The page shows the key beside the problem: say it once.
			p = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(p, key), ":"))
		}
		out = append(out, opssetup.ConfigProblem{File: file, Line: line, Key: key, Problem: p, Fix: problemFix(file, key, line, suggest)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cleanProblem drops the reader prefixes a person does not need ("orgconf:
// config.yaml: ", "wire: ~/.lore/config.yaml is not valid: "), keeping the
// key path and what is wrong.
func cleanProblem(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, orgconfPrefix)
	for _, pre := range []string{"wire: ~/.lore/" + ConfigFile + " is not valid YAML: ", "wire: ~/.lore/" + ConfigFile + " is not valid: ",
		"~/.lore/" + ConfigFile + ": ", ConfigFile + " is not valid YAML: ", ConfigFile + ": "} {
		p = strings.TrimPrefix(p, pre)
	}
	return strings.TrimSpace(p)
}

// locateProblem is the key path a problem names and its line in root: the
// "line N" a decode error states, else the line of the deepest node of the
// leading key path that exists (setups[personal] is the entry named
// personal, orgs[0] the first org). 0 when neither is found.
func locateProblem(root *yaml.Node, p string) (string, int) {
	if m := problemLineRe.FindStringSubmatch(p); m != nil {
		n, _ := strconv.Atoi(m[1]) // the pattern is digits only
		key := ""
		if pm := problemPathRe.FindStringSubmatch(strings.TrimPrefix(p, m[0]+": ")); pm != nil {
			key = pm[1]
		}
		return key, n
	}
	m := problemPathRe.FindStringSubmatch(p)
	if m == nil {
		return "", 0
	}
	return m[1], keyLine(root, m[1])
}

// keyLine is the line of path's deepest existing node under root (0 when
// not even its first part exists).
func keyLine(root *yaml.Node, path string) int {
	line := 0
	cur := root
	for _, part := range strings.Split(path, ".") {
		pm := pathPartRe.FindStringSubmatch(part)
		if pm == nil || cur == nil || cur.Kind != yaml.MappingNode {
			return line
		}
		var val *yaml.Node
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == pm[1] {
				val, line = cur.Content[i+1], cur.Content[i].Line
				break
			}
		}
		if val == nil {
			return line
		}
		cur = val
		if pm[2] == "" {
			continue
		}
		item := seqItem(cur, pm[2])
		if item == nil {
			return line
		}
		cur, line = item, item.Line
	}
	return line
}

// seqItem is the element of seq at index sel ("0") or the mapping whose
// name is sel ("personal"); nil when absent.
func seqItem(seq *yaml.Node, sel string) *yaml.Node {
	if seq.Kind != yaml.SequenceNode {
		return nil
	}
	if i, err := strconv.Atoi(sel); err == nil {
		if i >= 0 && i < len(seq.Content) {
			return seq.Content[i]
		}
		return nil
	}
	for _, it := range seq.Content {
		if it.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(it.Content); i += 2 {
			if it.Content[i].Value == "name" && it.Content[i+1].Value == sel {
				return it
			}
		}
	}
	return nil
}

// problemFix is the fix of one problem: where to edit, a concrete value
// for the identity key when a setup already holds a valid one, and the
// command that confirms it.
func problemFix(file, key string, line int, suggestKey string) string {
	at := file
	if line > 0 {
		at += " line " + strconv.Itoa(line)
	}
	switch {
	case key == "identity.key" && suggestKey != "":
		return "Edit " + at + " to read  key: " + suggestKey + "  (the key your setups use), save, then reload this page; " + configCheckCmd + " confirms it."
	case strings.HasSuffix(key, "identity.key"):
		return "Edit " + at + " to read  key: " + humanKeyPrefix + "<your email>, save, then reload this page; " + configCheckCmd + " confirms it."
	}
	return "Edit " + at + " as the problem says, save, then reload this page; " + configCheckCmd + " lists every problem."
}

// suggestedHumanKey is a valid human:<email> key some setup already uses
// ("" none), offered as the identity.key fix.
func suggestedHumanKey(raw []byte) string {
	d, err := decodeSetupsDoc(raw)
	if err != nil {
		return ""
	}
	for _, s := range d.Setups {
		if k := strings.TrimSpace(s.Identity.Key); k != "" && orgconf.ValidateHumanKey(k) == nil {
			return k
		}
	}
	return ""
}
