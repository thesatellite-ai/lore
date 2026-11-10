// Command lore is the lore binary: the open core with the plain setup page
// (setupweb.DefaultUI). All of it lives in package app; this main only
// stamps the version and runs it. Another build composes the same command
// with more app Options (an enterprise build can add a setup page through
// WithSetupUI).
//
// `task lore:build` builds ./cmd/lore and stamps `version` through the
// ldflag -X main.version=…; the released binaries are built separately
// (docs/DEVELOPMENT.md "Releases") and stamp it the same way.
package main

import "github.com/thesatellite-ai/lore/app"

// version is stamped at build time (-ldflags "-X main.version=…");
// "dev" for a plain go build.
var version = "dev"

func main() { app.Main(app.WithVersion(version)) }
