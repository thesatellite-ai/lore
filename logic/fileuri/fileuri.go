// Package fileuri converts between a local file path and the path part of
// a file: URL, the one place lore does it, for every operating system.
//
// Why it exists: a Windows absolute path is `C:\Users\a\x.db`. Put as-is
// into url.URL{Scheme: "file", Path: filepath.ToSlash(p)} it prints as
// `file://C:/Users/a/x.db`, where "C:" is the URL's host: SQLite refuses
// that DSN ("invalid uri authority: C:") and the folder store backend
// refuses it as a file url naming another machine. The URL form needs a
// slash before the drive (`file:///C:/Users/a/x.db`, the form SQLite's
// documentation gives for Windows), and the way back drops that slash.
//
// Invariant: URLPath(p) always starts with "/" for an absolute p, so the
// printed url never has a host; OSPath(windows, URLPath(p)) == p for a
// drive path. The functions are pure; goos is a parameter so the Windows
// rules are tested on every platform.
package fileuri

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// Scheme is the file: URL scheme.
const Scheme = "file"

// goosWindows is runtime.GOOS on Windows, the one system with drive letters.
const goosWindows = "windows"

// drivePrefixLen is the length of a drive prefix ("C:").
const drivePrefixLen = 2

// hasDrive reports whether s starts with a drive letter and a colon.
func hasDrive(s string) bool {
	if len(s) < drivePrefixLen || s[1] != ':' {
		return false
	}
	c := s[0]
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// URLPath is the path part of the file: URL of the local path p: slash
// separated, with a "/" before a drive letter. A path already in slash
// form (from a test or another system) is accepted as well.
func URLPath(p string) string {
	s := filepath.ToSlash(p)
	if hasDrive(s) {
		return "/" + s
	}
	return s
}

// URL is the file: URL of the local path p with the raw query q ("" for
// none); the path is escaped, so a '?' or '#' in a folder name cannot be
// read as URL options.
func URL(p, q string) string {
	return (&url.URL{Scheme: Scheme, Path: URLPath(p), RawQuery: q}).String()
}

// OSPathFor is the local path a file: URL's path names on goos: on
// Windows the slash before a drive is dropped and the separators become
// backslashes; elsewhere the path is returned unchanged.
func OSPathFor(goos, urlPath string) string {
	if goos != goosWindows {
		return urlPath
	}
	s := urlPath
	if strings.HasPrefix(s, "/") && hasDrive(s[1:]) {
		s = s[1:]
	}
	return strings.ReplaceAll(s, "/", `\`)
}

// OSPath is OSPathFor on this machine's operating system.
func OSPath(urlPath string) string { return OSPathFor(runtime.GOOS, urlPath) }

// FoldDriveHost reads a file url whose host is a drive letter
// (`file://C:/Users/a/x`, how people and naive code write a Windows path
// as a url) as the drive path it means (`file:///C:/Users/a/x`): the host
// moves to the front of the path. Any other url is left as it is. Without
// it the drive was taken for another machine's name: the folder backend
// refused the store, and a store url cleaned for config.yaml came out as
// `file://C:/C:/Users/a/x`, which lore then refused to read.
func FoldDriveHost(u *url.URL) {
	if u == nil || u.Scheme != Scheme || len(u.Host) != drivePrefixLen || !hasDrive(u.Host) {
		return
	}
	u.Path = "/" + u.Host + u.Path
	u.Host, u.RawPath = "", "" // RawPath is derived again from Path when printed
}

// LocalPath is the local path a file: URL string names on this machine
// ("" when raw is no file url): the inverse of URL. Callers that cut the
// "file://" prefix off by hand get "/C:/Users/a/x" on Windows, a path
// Windows reads as `\C:\Users\a\x` and refuses.
func LocalPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != Scheme {
		return ""
	}
	FoldDriveHost(u)
	return OSPath(u.Path)
}

// IsDrivePath reports a Windows drive path, `C:\x` or `C:/x` (a letter, a
// colon and a separator), on every OS: lore reads it as a local path, never
// as a url whose scheme is the drive letter (B3-27). On a system without
// drives it simply does not exist.
func IsDrivePath(s string) bool {
	return hasDrive(s) && len(s) > drivePrefixLen && (s[drivePrefixLen] == '\\' || s[drivePrefixLen] == '/')
}

// DriveURLPath is the url path of the drive path s: `C:\x\y` → "/C:/x/y",
// the form a file: url and lore's store urls carry.
func DriveURLPath(s string) string { return "/" + strings.ReplaceAll(s, `\`, "/") }

// IsDriveURLPath reports a url path that names a drive path ("/C:/x").
func IsDriveURLPath(p string) bool { return strings.HasPrefix(p, "/") && IsDrivePath(p[1:]) }
