package agentdetect

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// FileReader reads a small metadata file (package.json) for the version
// (OQ12). It is the only file access agentdetect makes, after the tool is
// decided, so it can never change the decision. nil: no metadata read.
type FileReader interface {
	ReadFile(name string) ([]byte, error)
}

// Version sources, recorded in Result.Sources["tool_version"].
const (
	SourceEnv      = "env"
	SourcePath     = "install_path"
	SourceMetadata = "package_metadata"
	SourceConfig   = "config"
)

// pathVersionRe finds a version folder in an install path: Homebrew
// Cellar/<tool>/<ver>/, a mise/asdf installs/<tool>/<ver>/, an nvm-style
// versions/<tool>/<ver>/.
var pathVersionRe = regexp.MustCompile(`/(?:Cellar|installs|versions)/[^/]+/v?([0-9]+\.[0-9]+(?:\.[0-9]+)?[^/]*)/`)

// VersionFromPath parses a version out of an install path ("" if none).
func VersionFromPath(p string) string {
	m := pathVersionRe.FindStringSubmatch(strings.ReplaceAll(p, `\`, "/"))
	if m == nil {
		return ""
	}
	return clean(m[1])
}

// metadataDepth is how many folders above a script the package.json walk
// looks (bin/x.js → the package root is usually 1-2 up).
const metadataDepth = 3

// VersionFromMetadata reads "version" from the package.json of the
// package that holds script (an npm-installed CLI); "" if none.
func VersionFromMetadata(fr FileReader, script string) string {
	if fr == nil || script == "" {
		return ""
	}
	dir := path.Dir(strings.ReplaceAll(script, `\`, "/"))
	for range metadataDepth {
		b, err := fr.ReadFile(path.Join(dir, "package.json"))
		if err == nil {
			var meta struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(b, &meta) == nil && meta.Version != "" {
				return clean(meta.Version)
			}
		}
		dir = path.Dir(dir)
	}
	return ""
}
