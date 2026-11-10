// Package cli is lore's command-line surface (STORE_SERVER_SPEC 9.3, 9.6;
// CLI_REFERENCE 1.4, 1.5): cobra commands built at startup from an
// op.Registry, one `lore <noun> <verb>` command per operation.
//
// Why it exists: the CLI is a thin translation of the registry (ONE
// OPERATION SPEC: every surface comes from one definition so they match). Positional
// arguments come from `arg:"N"` tags, flags from `flag:` tags (typed by the
// Go field), --help from the Summary, field descriptions and Examples, and
// every command runs through the injected op.Runner, so the scanner, policy,
// confirmations and audit apply exactly as on REST. No command ever calls an
// operation's run function itself (cli_test proves it).
//
// Errors: with --json the spec 9.4 object goes to stdout, otherwise the
// human `ERROR: [E_CODE] message` line (plus `  hint:`) goes to stderr. The
// process exit status comes from the error's class in model/errs; read it
// with ExitCode after Execute.
//
// Layering: surface (L6) imports op and model only; the engine that
// implements op.Runner is injected by the binary.
package cli
