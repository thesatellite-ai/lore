// Package docs writes the two files lore publishes for people and tools
// outside the binary (STORE_SERVER_SPEC 9.6): the CLI_REFERENCE command list
// and error table (generated blocks inside the hand-written markdown) and
// openapi.json (OpenAPI 3.1 of the fixed operations).
//
// Why it exists: the CLI and REST surfaces are built from the registry at
// startup, so only these written copies can drift (ONE OPERATION SPEC;
// DOCS O12c: docs/reference/ is the generated public copy). Every
// function here is pure and deterministic over the registry and
// model/errs, and CheckStale lets a test fail when a committed copy
// differs.
//
// The REST surface serves the same OpenAPI document at /openapi.json, plus
// one schema per dynamic kind of the attached stores.
package docs
