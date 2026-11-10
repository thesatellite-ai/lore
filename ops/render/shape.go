package render

import "encoding/json"

// shape.go — the declared JSON of render.run, so docs/reference/openapi.json
// (built from RunOut's fields) says what a caller actually reads. The wire
// bytes are the host's own, kept as they are for v0.1.17 (RunOut.raw,
// MarshalJSON); Run decodes them into these types, so a Go caller reads the
// same values and a host whose JSON drifts from them fails loudly
// (E_INTERNAL); renderhost's TestRenderJSONMatchesDeclaredShape pins
// every member of the real host's JSON against these declarations.
//
//	plain run, --dry-run        {"output": "…"}
//	--report --json             {"data": {file, profile, budget, …}}
//	--report --scopes --json    {"data": {files, chains, dirs_off}}
//	--trend --json              {target, budget, profile, renders, …}

// Report is the "data" object of `--report --json` and of
// `--report --scopes --json` (the members of one or the other).
type Report struct {
	// --report --json
	File               string     `json:"file,omitempty" desc:"--report: the generated file (.lore/LORE.md)"`
	Profile            string     `json:"profile,omitempty" desc:"--report: the tool profile whose budget applies (the smallest of the targets)"`
	Budget             int        `json:"budget,omitempty" desc:"--report: the budget in bytes"`
	WarnAtPercent      int        `json:"warn_at_percent,omitempty" desc:"--report: the warn level, percent of the budget"`
	Before             int        `json:"before,omitempty" desc:"--report: bytes with every pinned entry in full (LORE.full.md)"`
	After              int        `json:"after,omitempty" desc:"--report: bytes written"`
	CriticalHotfixes   int        `json:"critical_hotfixes,omitempty"`
	MustFull           int        `json:"must_full,omitempty" desc:"--report: must rules written in full"`
	MustLine           int        `json:"must_line,omitempty" desc:"--report: must rules written as one line"`
	HighHotfixLines    int        `json:"high_hotfix_lines,omitempty"`
	Topics             int        `json:"topics,omitempty"`
	IndexCollapsed     bool       `json:"index_collapsed,omitempty" desc:"--report: the topic index was folded to one line"`
	BudgetExceeded     bool       `json:"budget_exceeded,omitempty"`
	MustWithoutSummary int        `json:"must_without_summary,omitempty"`
	SupersededRules    int        `json:"superseded_rules,omitempty"`
	SupersededHotfixes int        `json:"superseded_hotfixes,omitempty"`
	ManualRules        int        `json:"manual_rules,omitempty"`
	PathScopedRules    int        `json:"path_scoped_rules,omitempty"`
	LineForms          []LineForm `json:"line_forms,omitempty" desc:"--report: the entries written as one line, and why"`
	OverWarnLevel      bool       `json:"over_warn_level,omitempty"`
	Dirs               []DirFile  `json:"dirs,omitempty" desc:"--report: the folder files"`
	DirFilesOff        bool       `json:"dir_files_off,omitempty" desc:"--report: context.dir_files is off"`
	TailsLeftOut       []string   `json:"tails_left_out,omitempty" desc:"--report: tail blocks left out for lack of room"`
	// --report --scopes --json
	Files   []ScopeFile  `json:"files,omitempty" desc:"--report --scopes: one row per generated file per tool"`
	Chains  []ScopeChain `json:"chains,omitempty" desc:"--report --scopes: the root-to-folder sums a tool loads"`
	DirsOff bool         `json:"dirs_off,omitempty" desc:"--report --scopes: context.dir_files is off"`
}

// LineForm is one entry written as one line.
type LineForm struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// DirFile is one folder file of --report.
type DirFile struct {
	File           string `json:"file"`
	Scope          string `json:"scope"`
	Budget         int    `json:"budget"`
	Bytes          int    `json:"bytes"`
	MustFull       int    `json:"must_full"`
	MustLine       int    `json:"must_line"`
	BudgetExceeded bool   `json:"budget_exceeded"`
}

// ScopeFile is one row of --report --scopes.
type ScopeFile struct {
	File     string `json:"file"`
	Scope    string `json:"scope"`
	Tool     string `json:"tool"`
	Budget   int    `json:"budget"`
	Bytes    int    `json:"bytes"`
	MustFull int    `json:"must_full"`
	MustLine int    `json:"must_line"`
	Status   string `json:"status" desc:"ok, warn or over"`
}

// ScopeChain is one root-to-folder sum of --report --scopes. The wire
// object also carries "parts" (the bytes of each file in the chain, a list
// of integers): the operation field model has no integer-list type, so it
// is described here and decoded into Parts by Run, not declared.
type ScopeChain struct {
	Dir    string `json:"dir"`
	Tool   string `json:"tool"`
	Budget int    `json:"budget" desc:"the chain's budget (context.chain_max.<tool>); 0 when only reported"`
	Bytes  int    `json:"bytes" desc:"the chain's sum; the object also lists \"parts\", the bytes of each file"`
	Status string `json:"status" desc:"ok, warn or over"`
	// Parts is the bytes of each file in the chain (the wire's "parts").
	Parts []int `json:"-"`
}

// TrendPoint is one render of --trend.
type TrendPoint struct {
	At    string `json:"at" desc:"RFC 3339 time of the render"`
	Bytes int    `json:"bytes"`
}

// UnmarshalJSON implements json.Unmarshaler: the declared members and
// "parts".
func (c *ScopeChain) UnmarshalJSON(b []byte) error {
	type plain ScopeChain // no methods: the default decoding
	var w struct {
		plain
		Parts []int `json:"parts"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*c = ScopeChain(w.plain)
	c.Parts = w.Parts
	return nil
}
