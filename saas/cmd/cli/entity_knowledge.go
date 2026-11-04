// entity_knowledge.go — the --source and description helpers of the old
// knowledge commands. The snapshot, external-source and techdoc command
// groups that lived here moved to the 0.1.18 core (ops/snapshot,
// ops/externalsource, ops/techdoc), mounted by core_mount.go.
package main

import (
	"fmt"
	"saas/pkg/constants"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/textnorm"
)

// knownSourceKinds returns the closed set of provenance values a row's
// source_kind may hold (constants.SourceKind; the LifecycleMixin comment in
// dbent/schema lists the same set). One list feeds both the --source help
// text and the validation below, so they can never disagree. A function, not
// a package var, so no caller can mutate the set.
func knownSourceKinds() []constants.SourceKind {
	return []constants.SourceKind{
		constants.SourceManual,
		constants.SourceLearnFrom,
		constants.SourceAgentProposal,
		constants.SourcePlugin,
		constants.SourceImported,
		constants.SourceMigrated,
	}
}

// sourceKindNames returns the accepted --source values in their canonical
// order, for help text and error messages.
func sourceKindNames() []string {
	out := make([]string, 0, len(knownSourceKinds()))
	for _, k := range knownSourceKinds() {
		out = append(out, k.String())
	}
	return out
}

// parseSourceKind validates a --source value against the closed set. An
// empty value means a person typed the row, so it reads as manual. Any other
// value outside the set is refused with E_INVALID_INPUT rather than stored,
// because source_kind feeds trust and filtering and a typo would silently
// fall out of every filter.
func parseSourceKind(v string) (constants.SourceKind, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return constants.SourceManual, nil
	}
	for _, k := range knownSourceKinds() {
		if k.String() == v {
			return k, nil
		}
	}
	return "", errcodes.New(errcodes.InvalidInput,
		fmt.Sprintf("--%s %q: want one of %s", constants.FlagSource, v, strings.Join(sourceKindNames(), ", ")))
}

// knowledgeDescription returns the one-line summary stored in a playbook's
// or prompt's required description column. The schema refuses an empty
// description, and every documented `add` call passes only --title and
// --body, so an omitted --description falls back to the title: the title is
// already the short human name of the row, which is exactly what a summary
// line shows. Both values go through textnorm like the title and body.
func knowledgeDescription(description, title string) (string, error) {
	d := strings.TrimSpace(description)
	if d == "" {
		d = strings.TrimSpace(title)
	}
	if d == "" {
		return "", errcodes.New(errcodes.InvalidInput,
			fmt.Sprintf("--%s (or --%s) must not be empty", constants.FlagDescription, constants.FlagTitle))
	}
	clean, err := textnorm.Normalize(d)
	if err != nil {
		return "", errcodes.New(errcodes.InvalidInput, "description: "+err.Error())
	}
	return clean, nil
}
