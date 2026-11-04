package agentdetect

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Flags are short snake_case notes on a detection (6.2 flags).
const (
	FlagWeakOnly        = "weak_only"
	FlagModelFromConfig = "model_from_config"
	FlagCI              = "ci"
	FlagTTY             = "tty"
)

// Inputs is everything Detect reads; every field is a seam.
type Inputs struct {
	// Getenv reads one allowlisted variable. Required.
	Getenv func(string) string
	// TTY is true when stdin is a terminal (OQ11).
	TTY bool
	// Procs lists ancestors; nil skips the walk.
	Procs ProcessSource
	// Files reads version metadata; nil skips it.
	Files FileReader
	// Extra are plugin detectors added on top (PQ1).
	Extra []Detector
}

// Result is one detection. Tool is "" when no agent ran the command.
type Result struct {
	Tool            string            `json:"tool,omitempty"`
	SessionID       string            `json:"tool_session_id,omitempty"`
	Version         string            `json:"tool_version,omitempty"`
	Model           string            `json:"model,omitempty"`
	ModelFromConfig bool              `json:"model_from_config,omitempty"`
	Signal          string            `json:"signal,omitempty"`
	Outer           []string          `json:"outer_tools,omitempty"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	CI              *CI               `json:"ci,omitempty"`
	Terminal        string            `json:"terminal,omitempty"`
	Sources         map[string]string `json:"sources,omitempty"`
	Flags           []string          `json:"flags,omitempty"`
	Confidence      Confidence        `json:"confidence,omitempty"`
}

// IsAgent reports whether a tool was found.
func (r Result) IsAgent() bool { return r.Tool != "" }

// maxValueLen caps every value read (6.3: one line, at most 200 chars).
const maxValueLen = 200

// clean makes a read value one line, control characters removed, capped.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxValueLen {
		s = s[:maxValueLen]
	}
	return s
}

// matches tests one Signal.
func (s Signal) matches(getenv func(string) string) bool {
	v := getenv(s.Env)
	switch s.Match {
	case Present:
		return v != ""
	case Equals:
		return v == s.Value
	case Prefix:
		return v != "" && strings.HasPrefix(v, s.Value)
	case Contains:
		return v != "" && strings.Contains(v, s.Value)
	case BothWith:
		return v != "" && getenv(s.Value) != ""
	}
	return false
}

// tokenLikeRe is the "never read a token" test (section 5 allowlist).
var tokenLikeRe = regexp.MustCompile(`(?i)token|secret|password|passwd|api_?key|auth|cookie|credential|private`)

// slugRe is a snake_case tool slug (MQ10).
var slugRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validation errors of plugin detectors (PQ1).
var (
	ErrBuiltinSlug = errors.New("agentdetect: a plugin detector may not reuse a built-in tool slug")
	ErrTokenName   = errors.New("agentdetect: a plugin detector may not read a token-like variable")
	ErrBadSlug     = errors.New("agentdetect: a detector slug must be snake_case")
)

// ValidateExtra refuses a plugin detector that would change a built-in
// one or read a token-like variable (PQ1).
func ValidateExtra(extra []Detector) error {
	builtin := map[string]bool{UnknownAgent: true}
	for _, d := range Builtin() {
		builtin[d.Tool] = true
	}
	for _, d := range extra {
		if !slugRe.MatchString(d.Tool) {
			return ErrBadSlug
		}
		if builtin[d.Tool] {
			return ErrBuiltinSlug
		}
		names := slices.Concat(d.SessionEnv, d.ModelEnv)
		for _, s := range slices.Concat(d.Strong, d.Weak) {
			names = append(names, s.Env)
			if s.Match == BothWith {
				names = append(names, s.Value)
			}
		}
		for _, n := range names {
			if tokenLikeRe.MatchString(n) {
				return ErrTokenName
			}
		}
	}
	return nil
}

// hit is one tool found, with the signal that found it.
type hit struct {
	d      Detector
	signal string
	weak   bool
	script string // matched process path (version from path / metadata)
	near   int    // ancestor index of the tool's process (-1 unknown)
	ver    string // version carried in AI_AGENT
}

// Detect runs steps 3-5 of section 3 (LORE_ACTOR and LORE_HUMAN are the
// caller's, steps 1-2). An invalid plugin detector is an error and no
// detection (fail closed: a bad plugin never silently decides).
func Detect(in Inputs) (Result, error) {
	if err := ValidateExtra(in.Extra); err != nil {
		return Result{}, err
	}
	getenv := func(n string) string { return clean(in.Getenv(n)) }
	table := Builtin()
	var procs []Process
	if in.Procs != nil {
		procs = in.Procs.Ancestors(MaxAncestors)
	}

	var strong []hit
	for _, d := range table {
		for _, s := range d.Strong {
			if s.matches(getenv) {
				strong = append(strong, hit{d: d, signal: s.Env})
				break
			}
		}
	}
	if len(strong) == 0 {
		if h, ok := conventionHit(table, getenv); ok {
			strong = append(strong, h)
		}
	}
	if len(strong) == 0 && !in.TTY {
		for _, d := range table {
			for _, s := range d.Weak {
				if s.matches(getenv) {
					strong = append(strong, hit{d: d, signal: s.Env, weak: true})
					break
				}
			}
			if len(strong) > 0 {
				break
			}
		}
	}
	if len(strong) == 0 {
		for _, d := range in.Extra {
			for _, s := range d.Strong {
				if s.matches(getenv) {
					strong = append(strong, hit{d: d, signal: "plugin:" + d.Tool + "/" + s.Env})
					break
				}
			}
		}
	}
	// Ancestor positions (nested ordering, 10.1) and the step-4 fallback.
	for i := range strong {
		strong[i].near = -1
		for j, pr := range procs {
			if path, ok := procMatches(strong[i].d, pr); ok {
				strong[i].near, strong[i].script = j, path
				break
			}
		}
	}
	if len(strong) == 0 {
		if h, ok := walkHit(slices.Concat(table, in.Extra), procs); ok {
			strong = append(strong, h)
		}
	}

	res := Result{CI: DetectCI(getenv), Terminal: terminal(getenv), Sources: map[string]string{}}
	if in.TTY {
		res.Flags = append(res.Flags, FlagTTY)
	}
	if res.CI != nil {
		res.Flags = append(res.Flags, FlagCI)
	}
	if len(strong) == 0 {
		if len(res.Sources) == 0 {
			res.Sources = nil
		}
		return res, nil
	}
	// Nearest ancestor first; unknown positions keep table order after.
	slices.SortStableFunc(strong, func(a, b hit) int {
		switch {
		case a.near == b.near:
			return 0
		case a.near < 0:
			return 1
		case b.near < 0:
			return -1
		}
		return a.near - b.near
	})
	h := strong[0]
	res.Tool, res.Signal, res.Confidence = h.d.Tool, h.signal, h.d.Confidence
	res.Sources["tool"] = sourceOfSignal(h.signal)
	if h.weak {
		res.Flags = append(res.Flags, FlagWeakOnly)
	}
	res.SessionID = firstEnv(getenv, h.d.SessionEnv)
	if res.SessionID != "" {
		res.Sources["tool_session_id"] = SourceEnv
	}
	for _, o := range strong[1:] {
		res.Outer = append(res.Outer, o.d.Tool)
		if res.ParentSessionID == "" {
			res.ParentSessionID = firstEnv(getenv, o.d.SessionEnv)
		}
	}
	if h.ver == "" {
		// A strong marker decided; AI_AGENT may still carry the version
		// (claude-code_<version>_agent, section 4).
		if d, ver, ok := namedTool(table, getenv(EnvAIAgent)); ok && d.Tool == h.d.Tool {
			h.ver = ver
		}
	}
	switch {
	case h.ver != "":
		res.Version, res.Sources["tool_version"] = h.ver, SourceEnv
	case VersionFromPath(h.script) != "":
		res.Version, res.Sources["tool_version"] = VersionFromPath(h.script), SourcePath
	case VersionFromMetadata(in.Files, h.script) != "":
		res.Version, res.Sources["tool_version"] = VersionFromMetadata(in.Files, h.script), SourceMetadata
	}
	if m := firstEnv(getenv, h.d.ModelEnv); m != "" {
		res.Model, res.ModelFromConfig, res.Sources["model"] = m, true, SourceConfig
		res.Flags = append(res.Flags, FlagModelFromConfig)
	}
	return res, nil
}

// sourceOfSignal names where the tool came from for Sources.
func sourceOfSignal(sig string) string {
	switch {
	case strings.HasPrefix(sig, "process:"):
		return "process"
	case strings.HasPrefix(sig, "plugin:"):
		return "plugin"
	}
	return SourceEnv
}

// conventionHit applies AI_AGENT / AGENT (step 3.2, OQ15).
func conventionHit(table []Detector, getenv func(string) string) (hit, bool) {
	for _, name := range []string{EnvAIAgent, EnvAgent} {
		v := getenv(name)
		if v == "" {
			continue
		}
		if d, ver, ok := namedTool(table, v); ok {
			return hit{d: d, signal: name, ver: ver}, true
		}
		if v == "1" || strings.EqualFold(v, "true") || name == EnvAIAgent {
			return hit{d: Detector{Tool: UnknownAgent, Confidence: Low}, signal: name}, true
		}
		// Any other AGENT value (build-01) is ignored: AGENT is common in CI.
	}
	return hit{}, false
}

// namedTool matches a convention value: exact name, <tool>_<ver>_<mode>,
// or <tool>@<ver>.
func namedTool(table []Detector, v string) (Detector, string, bool) {
	for _, d := range table {
		if slices.Contains(d.AgentNames, v) {
			return d, "", true
		}
	}
	name, ver := "", ""
	if parts := strings.SplitN(v, "_", 3); len(parts) == 3 {
		name, ver = parts[0], parts[1]
	} else if n, vv, ok := strings.Cut(v, "@"); ok {
		name, ver = n, vv
	}
	if name == "" {
		return Detector{}, "", false
	}
	for _, d := range table {
		if slices.Contains(d.AgentNames, name) {
			return d, ver, true
		}
	}
	return Detector{}, "", false
}

// walkHit is the parent-process fallback (step 4): the nearest ancestor
// whose executable or script names a tool.
func walkHit(table []Detector, procs []Process) (hit, bool) {
	for j, pr := range procs {
		for _, d := range table {
			if path, ok := procMatches(d, pr); ok {
				return hit{d: d, signal: "process:" + baseName(path), near: j, script: path}, true
			}
		}
	}
	return hit{}, false
}

// firstEnv is the first non-empty value of names.
func firstEnv(getenv func(string) string, names []string) string {
	for _, n := range names {
		if v := getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// terminal is "TERM_PROGRAM TERM_PROGRAM_VERSION" or TERMINAL_EMULATOR.
func terminal(getenv func(string) string) string {
	if t := getenv("TERM_PROGRAM"); t != "" {
		if v := getenv("TERM_PROGRAM_VERSION"); v != "" {
			return t + " " + v
		}
		return t
	}
	return getenv("TERMINAL_EMULATOR")
}
