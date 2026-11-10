package settings

// `--prompt <field>` on resource add and edit (RES 3.3, RES-OQ4): the
// value is typed at a terminal instead of given on the command line.
// Flow:
//
//	resAdd / resEdit ─► valueSets (--set, --set-json, --set-stdin)
//	                 ─► promptSets(d, --prompt, sets)
//	                      ├─ "@required" ─► every required field not yet set (FA-RES-1)
//	                      ├─ caller not interactive, or an agent ─► E_NEEDS_CONFIRM, never a hang
//	                      └─ one line per field from the terminal; a
//	                         sensitive field is read with echo off
//	                 ─► the answers join sets as field=value pairs
//
// Why the op reads the terminal itself: only the CLI process that owns
// the terminal can run it (engine.Interactive is false on REST, so
// `lore api` never blocks on its own stdin).

import (
	"bufio"
	"context"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
)

// promptRequired is the --prompt word for "every required field not yet
// given" (FA-RES-1); "@" cannot start a field name, so it never clashes.
const promptRequired = "@required"

// resPromptIn is the terminal --prompt reads (a var so tests can swap it).
var resPromptIn = os.Stdin

// promptSets asks for each --prompt field and appends field=value pairs
// to sets. Nothing to ask returns sets unchanged.
func promptSets(ctx context.Context, d kindDef, fields, sets []string) ([]string, error) {
	want := promptFields(d, fields, sets)
	if len(want) == 0 {
		return sets, nil
	}
	for _, n := range want {
		if _, ok := d.byName[n]; !ok {
			return nil, errs.New(errs.ResourceInvalid, d.name()+": --prompt "+n+": no such field").WithHint("fields: " + strings.Join(d.fieldNames(), ", "))
		}
	}
	// Agents and pipes never wait on a prompt: they get the needs-person
	// error naming the non-interactive ways to give the value.
	if !engine.Interactive(ctx) || engine.AgentOf(ctx) != "" || !term.IsTerminal(int(resPromptIn.Fd())) {
		return nil, errs.New(errs.NeedsConfirm, "--prompt needs a person at a terminal: "+strings.Join(want, ", ")).
			WithHint("give the value with --set-stdin <field> or --set <field>=<value>")
	}
	out := slices.Clone(sets)
	rd := bufio.NewReader(resPromptIn)
	for _, n := range want {
		v, err := promptOne(rd, n, flag(d.byName[n].Row, fSensitive))
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, errs.New(errs.InvalidInput, "--prompt "+n+": no value typed")
		}
		out = append(out, n+setSep+v)
	}
	return out, nil
}

// promptFields expands --prompt to field names in order, without
// duplicates; @required adds every required field no set names.
func promptFields(d kindDef, fields, sets []string) []string {
	given := map[string]bool{}
	for _, s := range sets {
		n, _, _ := strings.Cut(s, setSep)
		given[n] = true
	}
	var out []string
	add := func(n string) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, raw := range fields {
		for p := range strings.SplitSeq(raw, ",") {
			p = strings.TrimSpace(p)
			switch p {
			case "":
			case promptRequired:
				for _, n := range d.fieldNames() {
					if flag(d.byName[n].Row, fRequired) && !given[n] {
						add(n)
					}
				}
			default:
				add(p)
			}
		}
	}
	return out
}

// promptOne prints "<field>: " on stderr and reads one line; a sensitive
// field is read with echo off.
func promptOne(rd *bufio.Reader, field string, hidden bool) (string, error) {
	_, _ = io.WriteString(os.Stderr, field+": ")
	if hidden {
		b, err := term.ReadPassword(int(resPromptIn.Fd()))
		_, _ = io.WriteString(os.Stderr, "\n")
		if err != nil {
			return "", errs.Wrap(errs.InvalidInput, "--prompt "+field+": read the terminal", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := rd.ReadString('\n')
	if err != nil && line == "" {
		return "", errs.Wrap(errs.InvalidInput, "--prompt "+field+": read the terminal", err)
	}
	return strings.TrimSpace(line), nil
}
