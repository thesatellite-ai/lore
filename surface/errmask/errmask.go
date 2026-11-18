// Package errmask masks secret-shaped values in the error envelope every
// surface prints (layer L6: the CLI's error line and --json object, the
// local REST API's error body).
//
// Why (devco R-11): many refusals repeat the value a person typed (an id,
// a store name, a config key, a flag value). A token pasted into the wrong
// argument was echoed in full into terminal scrollback, CI logs and agent
// transcripts. Masking at the one formatter every error passes through
// covers each refusal, today's and future ones, instead of each message
// remembering to mask its own input.
package errmask

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Envelope returns env with every text a person reads (message, hint,
// question, the suggested next command and its arguments) run through the
// secret rules (gitleaks.MaskText). Code, class, exit and the findings
// (already hash-only) are kept as they are.
func Envelope(env errs.Envelope) errs.Envelope {
	b := &env.Error
	b.Message = gitleaks.MaskText(withoutPackagePrefixes(b.Message))
	b.Hint = gitleaks.MaskText(b.Hint)
	b.Question = gitleaks.MaskText(b.Question)
	if b.Next != nil {
		n := *b.Next
		n.Command = gitleaks.MaskText(n.Command)
		n.Args = maskAll(n.Args)
		if len(n.AddArgsOneOf) > 0 {
			one := make([][]string, len(n.AddArgsOneOf))
			for i, args := range n.AddArgsOneOf {
				one[i] = maskAll(args)
			}
			n.AddArgsOneOf = one
		}
		b.Next = &n
	}
	return env
}

// Of is Envelope(errs.EnvelopeOf(err)): the masked envelope of any error.
func Of(err error) errs.Envelope { return Envelope(errs.EnvelopeOf(err)) }

// maskAll masks each string of a copy of in (the caller's slice is never
// changed: an error value may be printed twice).
func maskAll(in []string) []string {
	out := slices.Clone(in)
	for i, s := range out {
		out[i] = gitleaks.MaskText(s)
	}
	return out
}

// internalPackages are the Go package names whose "<name>: " prefix some
// error messages start with (errs.New(…, "wire: …"), errs.Prefix chains).
// None of them is a lore command or a word a person knows, so the surface
// drops them (devco D-641: "[E_NOT_FOUND] wire: this repository has no
// lore project"). Prefixes that name a lore command or a tool a person
// runs (git:, migrate:, sync:, task:, …) are kept: they say where it failed.
var internalPackages = []string{
	"wire", "engine", "attach", "rowfs", "rowmd", "meta", "sqlite", "ids", "row",
	"repourl", "localstate", "gitleaks", "orgcfg", "gitref", "gitremote", "gitfast",
	"lorehome", "onboard", "schema", "op", "value",
}

// withoutPackagePrefixes is msg with every internal package prefix removed
// where one starts the message or follows a ": " join (errs.Prefix puts
// what + ": " before the inner message, so a prefix can sit mid-chain).
func withoutPackagePrefixes(msg string) string {
	parts := strings.Split(msg, ": ")
	out := parts[:0]
	for i, p := range parts {
		if i < len(parts)-1 && slices.Contains(internalPackages, p) {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ": ")
}
