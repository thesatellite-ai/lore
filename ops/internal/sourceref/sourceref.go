// Package sourceref is the one --source-ref flag of every add whose row
// keeps a source_ref (memory, rule, decision, hotfix, pattern, playbook,
// snapshot): the same flag, field and JSON on each (devco D-425: memory,
// rule and decision had it, hotfix, pattern and playbook did not, so
// agents filling a store put their sources into the body).
package sourceref

// Field is the row field and the JSON name.
const Field = "source_ref"

// Flag is embedded (with no json tag) in an add's input: op.Define
// flattens it into --source-ref and "source_ref".
type Flag struct {
	SourceRef string `json:"source_ref,omitempty" flag:"source-ref" desc:"where it came from: a free-form provenance pointer (a url, ticket, commit, file:line)"`
}
