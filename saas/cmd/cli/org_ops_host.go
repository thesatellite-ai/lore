// org_ops_host.go — orgHost, the machinery behind the `lore org`
// operations (ops/orgcmd.Host, wire.Options.OrgCommands; PASS3 ONE-PATH,
// P3-ORG-G-2): the v0.1.17 bodies in org_cmd.go, org_create_cmd.go,
// org_import_cmd.go and org_rename_cmd.go, run against a buffer so their
// lines become the operation's Report (colour removed, CLIErrors as core
// errors). The operation asks; a body that stopped unconfirmed returns its
// question and has written nothing.
package main

import (
	"bytes"
	"context"

	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// orgHost implements opsorgcmd.Host.
type orgHost struct{}

var _ opsorgcmd.Host = orgHost{}

// orgReport runs an asking body against a buffer.
func orgReport(body func(w *bytes.Buffer) (string, error)) (opsorgcmd.Report, error) {
	var b bytes.Buffer
	ask, err := body(&b)
	return opsorgcmd.Report{Lines: plainLines(b.String()), Ask: ask}, hostErr(err)
}

// orgPlain runs a body that never asks.
func orgPlain(body func(w *bytes.Buffer) error) (opsorgcmd.Report, error) {
	return orgReport(func(w *bytes.Buffer) (string, error) { return "", body(w) })
}

// Create implements opsorgcmd.Host.
func (orgHost) Create(ctx context.Context, in opsorgcmd.CreateIn, yes bool) (opsorgcmd.Report, error) {
	return orgReport(func(w *bytes.Buffer) (string, error) { return runOrgCreate(ctx, w, in, yes) })
}

// Import implements opsorgcmd.Host.
func (orgHost) Import(ctx context.Context, in opsorgcmd.ImportIn, yes bool) (opsorgcmd.Report, error) {
	return orgReport(func(w *bytes.Buffer) (string, error) { return runOrgImport(ctx, w, in, yes) })
}

// Link implements opsorgcmd.Host.
func (orgHost) Link(ctx context.Context, in opsorgcmd.LinkIn, yes bool) (opsorgcmd.Report, error) {
	return orgReport(func(w *bytes.Buffer) (string, error) { return runOrgLink(ctx, w, in, yes) })
}

// Rename implements opsorgcmd.Host.
func (orgHost) Rename(ctx context.Context, in opsorgcmd.RenameIn) (opsorgcmd.RenameOut, error) {
	out, err := runOrgRename(ctx, in)
	return out, hostErr(err)
}

// Set implements opsorgcmd.Host.
func (orgHost) Set(ctx context.Context, in opsorgcmd.SetIn) (opsorgcmd.Report, error) {
	return orgPlain(func(w *bytes.Buffer) error { return runOrgSet(ctx, w, in) })
}

// Use implements opsorgcmd.Host.
func (orgHost) Use(ctx context.Context, in opsorgcmd.UseIn) (opsorgcmd.Report, error) {
	return orgPlain(func(w *bytes.Buffer) error { return runOrgUse(ctx, w, in) })
}
