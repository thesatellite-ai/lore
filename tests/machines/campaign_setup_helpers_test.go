//go:build machines && !windows

package machines

// campaign_setup_helpers_test.go — helpers of the setup-area scenario
// campaign (TestCampaignSetup_*): the --json error envelope, a tree hash
// for --dry-run checks, the documented response schemas of
// docs/reference/openapi.json, and `lore api` started on a machine.
//
// Every test runs in a Machine's temp HOME with the fake gh and ssh: the
// real ~/.ssh, ~/.lore and gh of the host are never read.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// csErr is the spec 9.4 error object a --json refusal prints.
type csErr struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint"`
	Exit      int    `json:"exit"`
	Retryable bool   `json:"retryable"`
	Next      *struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"next"`
}

// csErrOf reads the error envelope of a --json run; nil when stdout holds
// no error member.
func csErrOf(r Result) *csErr {
	var env struct {
		Error *csErr `json:"error"`
	}
	if json.Unmarshal([]byte(r.Stdout), &env) != nil {
		return nil
	}
	return env.Error
}

// csRefused asserts r is a refusal with code: non-zero exit, the code in
// the envelope, the envelope's exit equal to the process's, and a next
// step (a hint or a next command) the user can act on.
func csRefused(t *testing.T, r Result, code string) *csErr {
	t.Helper()
	e := csErrOf(r)
	switch {
	case r.Code == 0:
		t.Errorf("want %s, got exit 0: %s", code, r)
	case e == nil:
		t.Errorf("want a JSON %s envelope: %s", code, r)
	case e.Code != code:
		t.Errorf("want %s, got %s: %s", code, e.Code, r)
	case e.Exit != r.Code:
		t.Errorf("envelope exit %d, process exit %d: %s", e.Exit, r.Code, r)
	case e.Hint == "" && e.Next == nil:
		t.Errorf("%s names no next step (no hint, no next): %s", code, r)
	}
	return e
}

// csOK asserts exit 0 and returns stdout.
func csOK(t *testing.T, r Result) string {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("want exit 0: %s", r)
	}
	return r.Stdout
}

// csRuntime are lore's own runtime files inside a repo (gitignored
// caches, the lock, the .git folder): a read may refresh them.
var csRuntime = []string{".git", "lore-cache.db", "lore-cache.db-wal", "lore-cache.db-shm", "lock"}

// csHash hashes every regular file and symlink under root (path, mode,
// content); sockets and other special files count by name only. Names in
// skip are left out (a folder with all it holds).
func csHash(t *testing.T, root string, skip ...string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root && slices.Contains(skip, d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		_, _ = io.WriteString(h, rel+"\x00"+info.Mode().String()+"\x00")
		switch {
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			_, _ = h.Write(b)
		case info.Mode()&fs.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			_, _ = io.WriteString(h, l)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// csConfig is ~/.lore/config.yaml ("" when missing).
func csConfig(t *testing.T, m *Machine) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.Home, ".lore", "config.yaml"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// csKindAnswers is the kind step's answers for a solo setup name.
func csKindAnswers(name string) string {
	b, _ := json.Marshal(map[string]string{"kind": "solo", "name": name})
	return string(b)
}

// csNewSetup saves the kind and identity steps of a solo setup.
func csNewSetup(t *testing.T, m *Machine, name, email string) {
	t.Helper()
	csOK(t, m.Lore(t, "", "setup", "apply", name, "--step", "kind", "--answers", csKindAnswers(name), "--json"))
	csOK(t, m.Lore(t, "", "setup", "apply", name, "--step", "identity", "--answers", `{"name":"`+PersonName+`","email":"`+email+`"}`, "--json"))
}

// ── documented response schemas ───────────────────────────────────────

// csSchema is the subset of an OpenAPI schema docs/reference uses.
type csSchema struct {
	Type                 json.RawMessage      `json:"type"`
	Properties           map[string]*csSchema `json:"properties"`
	Required             []string             `json:"required"`
	Items                *csSchema            `json:"items"`
	AdditionalProperties json.RawMessage      `json:"additionalProperties"`
	AnyOf                []*csSchema          `json:"anyOf"`
}

type csOperation struct {
	OperationID string `json:"operationId"`
	Responses   map[string]struct {
		Content map[string]struct {
			Schema *csSchema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

// csRoute is one documented operation: method, path and 2xx schema.
type csRoute struct {
	Method, Path string
	Schema       *csSchema
}

var (
	csRoutesOnce sync.Once
	csRoutes     map[string]csRoute
	csRoutesErr  error
)

// csRouteOf returns the documented route of an operation id.
func csRouteOf(t *testing.T, opID string) csRoute {
	t.Helper()
	csRoutesOnce.Do(func() {
		// The document the binary under test generates (`lore spec
		// openapi`, what docs/reference/openapi.json is made from and
		// TestReferenceIsCurrent keeps equal to it).
		dir, err := os.MkdirTemp("", "cs-spec-")
		if err != nil {
			csRoutesErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		c := exec.Command(loreBin, "spec", "openapi", "--out", dir)
		c.Dir, c.Env = dir, []string{"HOME=" + dir, "PATH=/usr/bin:/bin", "LORE_NO_UPDATE_CHECK=1"}
		if out, err := c.CombinedOutput(); err != nil {
			csRoutesErr = fmt.Errorf("lore spec openapi: %v: %s", err, out)
			return
		}
		b, err := os.ReadFile(filepath.Join(dir, "openapi.json"))
		if err != nil {
			csRoutesErr = err
			return
		}
		var doc struct {
			Paths map[string]map[string]csOperation `json:"paths"`
		}
		if csRoutesErr = json.Unmarshal(b, &doc); csRoutesErr != nil {
			return
		}
		csRoutes = map[string]csRoute{}
		for p, ms := range doc.Paths {
			for m, o := range ms {
				r := csRoute{Method: strings.ToUpper(m), Path: p}
				for _, code := range []string{"200", "201"} {
					if c, ok := o.Responses[code].Content["application/json"]; ok {
						r.Schema = c.Schema
					}
				}
				csRoutes[o.OperationID] = r
			}
		}
	})
	if csRoutesErr != nil {
		t.Fatalf("openapi.json: %v", csRoutesErr)
	}
	r, ok := csRoutes[opID]
	if !ok {
		t.Fatalf("openapi.json documents no operation %s", opID)
	}
	return r
}

// csTypes is a schema's type list ("string" or ["string","null"]).
func (s *csSchema) types() []string {
	if len(s.Type) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(s.Type, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(s.Type, &many) == nil {
		return many
	}
	return nil
}

// csKind is the JSON type of a raw value.
func csKind(v json.RawMessage) string {
	b := bytes.TrimSpace(v)
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	if bytes.ContainsAny(b, ".eE") {
		return "number"
	}
	return "integer"
}

// csConform lists every place v leaves its documented schema: a missing
// required field, an undocumented field, a wrong type.
func csConform(s *csSchema, v json.RawMessage, at string) []string {
	if s == nil {
		return nil
	}
	if len(s.AnyOf) > 0 {
		for _, alt := range s.AnyOf {
			if len(csConform(alt, v, at)) == 0 {
				return nil
			}
		}
		return []string{at + ": matches none of anyOf"}
	}
	k := csKind(v)
	if ts := s.types(); len(ts) > 0 {
		ok := slices.Contains(ts, k) || (k == "integer" && slices.Contains(ts, "number"))
		if !ok {
			return []string{at + ": is " + k + ", documented " + strings.Join(ts, "|")}
		}
	}
	var out []string
	switch k {
	case "object":
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) != nil {
			return []string{at + ": bad object"}
		}
		for _, r := range s.Required {
			if _, ok := obj[r]; !ok {
				out = append(out, at+"."+r+": required, missing")
			}
		}
		open := len(s.Properties) == 0 || (len(s.AdditionalProperties) > 0 && string(s.AdditionalProperties) != "false")
		for name, val := range obj {
			ps, ok := s.Properties[name]
			if !ok {
				if !open {
					out = append(out, at+"."+name+": not documented")
				}
				continue
			}
			out = append(out, csConform(ps, val, at+"."+name)...)
		}
	case "array":
		var arr []json.RawMessage
		if json.Unmarshal(v, &arr) != nil {
			return []string{at + ": bad array"}
		}
		for i, item := range arr {
			out = append(out, csConform(s.Items, item, at+"["+strconv.Itoa(i)+"]")...)
		}
	}
	return out
}

// csConforms fails t when stdout of a --json run leaves opID's documented
// response schema.
func csConforms(t *testing.T, opID string, stdout string) {
	t.Helper()
	r := csRouteOf(t, opID)
	if r.Schema == nil {
		t.Fatalf("%s: openapi.json documents no 2xx JSON schema", opID)
	}
	for _, p := range csConform(r.Schema, json.RawMessage(stdout), "$") {
		t.Errorf("%s --json leaves docs/reference: %s\n%s", opID, p, stdout)
	}
}

// ── lore api on a machine ─────────────────────────────────────────────

var csListenRe = regexp.MustCompile(`lore api listening on (http://127\.0\.0\.1:[0-9]+) \(token: ([^)]+)\)`)

// csAPI is a running `lore api` of a machine.
type csAPI struct {
	base, token string
}

// csStartAPI runs `lore api --port 0` in cwd (a repo: lore api needs one)
// until t ends.
func csStartAPI(t *testing.T, m *Machine, cwd string) *csAPI {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := exec.CommandContext(ctx, m.bin, "api", "--port", "0")
	c.Dir, c.Env, c.Stdout = cwd, m.env(), io.Discard
	se, err := c.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = c.Wait() })
	found := make(chan []string, 1)
	go func() {
		sc := bufio.NewScanner(se)
		var seen []string
		for sc.Scan() {
			seen = append(seen, sc.Text())
			if mm := csListenRe.FindStringSubmatch(sc.Text()); mm != nil {
				found <- mm
				_, _ = io.Copy(io.Discard, se)
				return
			}
		}
		found <- []string{strings.Join(seen, " | ")}
	}()
	select {
	case mm := <-found:
		if len(mm) != 3 {
			t.Fatalf("lore api did not start: %s", mm[0])
		}
		b, err := os.ReadFile(mm[2])
		if err != nil {
			t.Fatal(err)
		}
		return &csAPI{base: mm[1], token: strings.TrimSpace(string(b))}
	case <-time.After(30 * time.Second):
		t.Fatal("lore api printed no listening line")
	}
	return nil
}

// call sends one request and returns the status and body.
func (a *csAPI) call(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}

// csCanon is v re-encoded with sorted keys, so two JSON texts compare by
// value; fields named in drop (timestamps that differ per run) are removed
// at any depth.
func csCanon(t *testing.T, v string, drop ...string) string {
	t.Helper()
	var raw json.RawMessage = json.RawMessage(v)
	out, err := csCanonRaw(raw, drop)
	if err != nil {
		t.Fatalf("not JSON (%v): %s", err, v)
	}
	return out
}

func csCanonRaw(v json.RawMessage, drop []string) (string, error) {
	switch csKind(v) {
	case "object":
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(v, &obj); err != nil {
			return "", err
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			if !slices.Contains(drop, k) {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			s, err := csCanonRaw(obj[k], drop)
			if err != nil {
				return "", err
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteString(":" + s)
		}
		b.WriteString("}")
		return b.String(), nil
	case "array":
		var arr []json.RawMessage
		if err := json.Unmarshal(v, &arr); err != nil {
			return "", err
		}
		parts := make([]string, len(arr))
		for i, x := range arr {
			s, err := csCanonRaw(x, drop)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		return "", err
	}
	return buf.String(), nil
}
