// upgrade_release.go — finding, downloading and checking a release for
// `lore upgrade` (FORMAT_EVOLUTION 8.2).
//
//	latestTag: GET <base>/releases/latest, redirect NOT followed, tag from
//	           Location (never api.github.com, which is rate limited)
//	fetch:     <base>/releases/download/<tag>/<name> (the exact tag, so two
//	           files never come from two releases)
//	verifySHA: archive sha256 == its line in checksums.txt, else E_UPGRADE_CHECKSUM
//	extract:   only lore / lore.exe and skills/** from the tar.gz or zip
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/model/errs"
)

// releaseBaseURL is the repository page releases hang off. A variable only
// so tests point it at an httptest server; never read from the environment
// (a redirected download source would be a supply-chain hole).
var releaseBaseURL = "https://github.com/thesatellite-ai/lore"

// checksumsFile is goreleaser's checksum.name_template.
const checksumsFile = "checksums.txt"

// releaseTagPath is the path segment of a tag page in the Location header.
const releaseTagPath = "/releases/tag/"

// maxReleaseBytes caps one download (the archives are ~8 MB).
const maxReleaseBytes = 200 << 20

// upgradeHTTPTimeout bounds `lore upgrade`'s requests; the daily notice uses
// noticeTimeout instead (8.6: 2 s, never delays the command).
const upgradeHTTPTimeout = 2 * time.Minute

// releaseClient talks to the release host.
type releaseClient struct {
	base string
	http *http.Client
}

// newReleaseClient never follows redirects on its own: latestTag needs the
// Location header itself; fetch follows them by hand (GitHub serves assets
// from a CDN host behind a 302).
func newReleaseClient(timeout time.Duration) releaseClient {
	return releaseClient{base: strings.TrimRight(releaseBaseURL, "/"), http: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// latestTag reads the newest release tag from the releases/latest redirect.
// Prereleases are skipped by GitHub's redirect itself.
func (c releaseClient) latestTag(ctx context.Context) (string, error) {
	resp, err := c.get(ctx, c.base+"/releases/latest")
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close() // only the header is read
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, releaseTagPath)
	if resp.StatusCode/100 != 3 || i < 0 {
		return "", errcodes.New(errcodes.Code(errs.NotFound),
			fmt.Sprintf("no latest release found (%d from %s)", resp.StatusCode, c.host()))
	}
	tag, err := url.PathUnescape(loc[i+len(releaseTagPath):])
	if err != nil || tag == "" || strings.ContainsAny(tag, "/?#") {
		return "", errcodes.New(errcodes.Internal, "unreadable release redirect: "+loc)
	}
	return tag, nil
}

// fetch downloads one asset of tag, following redirects by hand (at most
// maxRedirects). A 404 is E_NOT_FOUND (8.9: a --version that does not
// exist); a short body is E_UPGRADE_CHECKSUM (8.9: a partial download).
func (c releaseClient) fetch(ctx context.Context, tag, name string) ([]byte, error) {
	target := c.base + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
	for range maxRedirects {
		resp, err := c.get(ctx, target)
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode/100 == 3:
			_ = resp.Body.Close() // redirect body is empty
			next, perr := resp.Location()
			if perr != nil {
				return nil, errcodes.New(errcodes.Internal, "bad redirect for "+name).WithCause(perr)
			}
			target = next.String()
			continue
		case resp.StatusCode == http.StatusNotFound:
			_ = resp.Body.Close()
			return nil, errcodes.New(errcodes.Code(errs.NotFound),
				fmt.Sprintf("no release %s (404 from %s for %s)", tag, c.host(), name)).
				WithHint("pick an existing tag: " + c.base + "/releases")
		case resp.StatusCode != http.StatusOK:
			_ = resp.Body.Close()
			return nil, errcodes.New(errcodes.Code(errs.Offline),
				fmt.Sprintf("%s answered %d for %s", c.host(), resp.StatusCode, name)).WithHint("try again later")
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxReleaseBytes+1))
		_ = resp.Body.Close() // read done
		if rerr != nil || (resp.ContentLength > 0 && int64(len(body)) != resp.ContentLength) {
			return nil, errcodes.New(errcodes.Code(errs.UpgradeChecksum), "download of "+name+" was cut short").
				WithCause(rerr).WithHint("run `" + BinaryName + " upgrade` again")
		}
		if len(body) > maxReleaseBytes {
			return nil, errcodes.New(errcodes.Code(errs.UpgradeChecksum), name+" is larger than any lore release")
		}
		return body, nil
	}
	return nil, errcodes.New(errcodes.Internal, "too many redirects for "+name)
}

// maxRedirects bounds fetch's hand-followed redirects.
const maxRedirects = 5

// get is one GET; a transport failure is E_OFFLINE (8.9 "No network").
func (c releaseClient) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "build request").WithCause(err)
	}
	req.Header.Set("User-Agent", BinaryName+"/"+version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errcodes.New(errcodes.Code(errs.Offline),
			fmt.Sprintf("cannot reach %s (%v); %s %s unchanged", c.host(), err, BinaryName, version)).
			WithHint("try again later")
	}
	return resp, nil
}

// host is the release host for messages.
func (c releaseClient) host() string {
	if u, err := url.Parse(c.base); err == nil && u.Host != "" {
		return u.Host
	}
	return c.base
}

// archiveName is goreleaser's name_template plus the format override.
func archiveName(goos, goarch string) string {
	if goos == goosWindows {
		return BinaryName + "_" + goos + "_" + goarch + ".zip"
	}
	return BinaryName + "_" + goos + "_" + goarch + ".tar.gz"
}

// goosWindows is runtime.GOOS on Windows.
const goosWindows = "windows"

// verifySHA checks archive against its line in checksums.txt. It catches
// corruption and truncation, not a tampered release (both files come from
// the same place; signed releases are a later step, 8.2 point 3).
func verifySHA(archive, sums []byte, name string) error {
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if !strings.EqualFold(fields[0], got) {
			return errcodes.New(errcodes.Code(errs.UpgradeChecksum),
				fmt.Sprintf("sha256 of %s does not match %s (got %s)", name, checksumsFile, got[:12])).
				WithHint("run `" + BinaryName + " upgrade` again")
		}
		return nil
	}
	return errcodes.New(errcodes.Code(errs.UpgradeChecksum), checksumsFile+" has no line for "+name).
		WithHint("run `" + BinaryName + " upgrade` again")
}

// releaseFiles is what extract keeps from an archive.
type releaseFiles struct {
	binary []byte
	// skills maps a path under skills/ (slash-separated, cleaned) to its bytes.
	skills map[string][]byte
}

// skillsPrefix is the archive folder of the agent skill bundle.
const skillsPrefix = "skills/"

// extract keeps only the binary and skills/** (8.2 point 4). Entry names
// are cleaned and anything escaping the archive root is ignored.
func extract(archive []byte, goos string) (releaseFiles, error) {
	bin := BinaryName
	if goos == goosWindows {
		bin += ".exe"
	}
	out := releaseFiles{skills: map[string][]byte{}}
	keep := func(name string, r io.Reader) error {
		name = path.Clean(strings.TrimPrefix(name, "./"))
		if strings.HasPrefix(name, "..") || path.IsAbs(name) {
			return nil
		}
		switch {
		case name == bin:
			b, err := io.ReadAll(io.LimitReader(r, maxReleaseBytes))
			out.binary = b
			return err
		case strings.HasPrefix(name, skillsPrefix):
			b, err := io.ReadAll(io.LimitReader(r, maxReleaseBytes))
			out.skills[strings.TrimPrefix(name, skillsPrefix)] = b
			return err
		}
		return nil
	}
	var err error
	if goos == goosWindows {
		err = extractZip(archive, keep)
	} else {
		err = extractTarGz(archive, keep)
	}
	if err != nil {
		return out, errcodes.New(errcodes.Code(errs.UpgradeChecksum), "the release archive cannot be read").WithCause(err)
	}
	if len(out.binary) == 0 {
		return out, errcodes.New(errcodes.Code(errs.UpgradeChecksum), "the release archive has no "+bin)
	}
	return out, nil
}

func extractTarGz(archive []byte, keep func(string, io.Reader) error) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if err := keep(h.Name, tr); err != nil {
			return err
		}
	}
}

func extractZip(archive []byte, keep func(string, io.Reader) error) error {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		kerr := keep(f.Name, rc)
		_ = rc.Close() // read done; a close error after a full read is not actionable
		if kerr != nil {
			return kerr
		}
	}
	return nil
}
