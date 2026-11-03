// Package llmcall is a thin, pluggable abstraction over model calls
// used by the bench engine.
//
// Providers shipped: a hosted model's command-line tool (default;
// matches Phase-1 runner.py behavior), a direct call to the hosted
// model's API (used when its API key variable, read below, is set), and
// a local Ollama model.
//
// The interface is intentionally minimal:
//
//	type Caller interface {
//	    Call(ctx, model, prompt, opts) (Response, error)
//	}
//
// Response includes the raw text + best-effort token counts + cost
// estimate (rate table indexed by model). Cost is an ESTIMATE — the
// API returns exact usage; the CLI provider has to guess from char count.
package llmcall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Response is what a Caller returns.
type Response struct {
	Output        string
	InputTokens   int // best-effort
	OutputTokens  int // best-effort
	ElapsedMs     int64
	CostUSDEstim  float64 // estimated, not authoritative
	ProviderUsed  string  // "claude-cli" | "anthropic-api"
	RawProviderID string  // request id from provider, if available
}

// Options controls per-call behavior.
type Options struct {
	Temperature float64       // 0..1
	MaxTokens   int           // default 4096
	Timeout     time.Duration // default 120s
	// DisableTools is honored only by the command-line provider (which
	// uses --tools "" to suppress tool-use stubs).
	DisableTools bool
}

func (o Options) withDefaults() Options {
	if o.MaxTokens <= 0 {
		o.MaxTokens = 4096
	}
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	return o
}

// Caller is the provider-side interface.
type Caller interface {
	Call(ctx context.Context, model, prompt string, opts Options) (Response, error)
	Name() string
}

// Auto returns a router Caller that picks a provider per-call based on
// the model name:
//
//   - "ollama:<model>"     → Ollama (default host http://localhost:11434,
//     override via OLLAMA_HOST). Model name after the prefix is passed
//     verbatim to /api/chat — so "ollama:qwen3-coder:latest" works.
//   - "openai:<model>"     → reserved prefix for a future local
//     chat-completions endpoint (LM Studio / vLLM); not wired yet.
//   - anything else        → the hosted model's API if its API key is set,
//     otherwise the hosted model's local command-line tool.
//
// Using a router (not a single fixed provider) means a single bench run
// can mix providers — e.g. llm-judge graders calling a hosted model while
// the system-under-test is a local Ollama model.
func Auto() Caller {
	return &router{
		anthropicKey: os.Getenv("ANTHROPIC_API_KEY"),
		ollamaHost:   ollamaHostOrDefault(),
	}
}

type router struct {
	anthropicKey string
	ollamaHost   string
}

func (r *router) Name() string { return "router" }

func (r *router) Call(ctx context.Context, model, prompt string, opts Options) (Response, error) {
	switch {
	case strings.HasPrefix(model, "ollama:"):
		return (&ollamaProvider{host: r.ollamaHost}).
			Call(ctx, strings.TrimPrefix(model, "ollama:"), prompt, opts)
	case r.anthropicKey != "":
		return (&anthropicAPI{apiKey: r.anthropicKey}).Call(ctx, model, prompt, opts)
	default:
		return (&claudeCLI{}).Call(ctx, model, prompt, opts)
	}
}

// ds:def id=env-ollama-host-hj0y341b owner=@khanakia stability=stable desc="OLLAMA_HOST and its default for local models"
func ollamaHostOrDefault() string {
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		// OLLAMA_HOST may be bare host:port or full URL; normalize.
		if !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") {
			h = "http://" + h
		}
		return strings.TrimRight(h, "/")
	}
	return "http://localhost:11434"
}

// ── provider: hosted model command-line tool ───────────────────────────────

// cliBinary is the hosted model's command-line program, run through PATH.
// It is a real program name, so it stays exactly as the vendor ships it.
const cliBinary = "claude"

type claudeCLI struct{}

func (c *claudeCLI) Name() string { return "claude-cli" }

func (c *claudeCLI) Call(ctx context.Context, model, prompt string, opts Options) (Response, error) {
	opts = opts.withDefaults()
	if _, err := exec.LookPath(cliBinary); err != nil {
		return Response{}, fmt.Errorf("model command-line tool %q not on PATH; install it or set ANTHROPIC_API_KEY", cliBinary)
	}
	// We do NOT override the user's settings/hooks. The user's actual
	// environment (caveman-mode banners, statusline offers, etc.) is part
	// of the real-world deployment. The bench answers "does the skill
	// bundle work in the user's actual environment" — not "does it work
	// in a sterile vacuum." If the skill is too fragile to survive
	// SessionStart-hook noise, that's a SKILL.md problem to fix, not a
	// runner problem to paper over.
	args := []string{
		"--print",
		"--no-session-persistence",
		"--model", model,
	}
	if opts.DisableTools {
		args = append(args, "--tools", "")
	}
	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, cliBinary, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return Response{
				Output:       stdout.String() + "\n[stderr]\n" + stderr.String(),
				ElapsedMs:    elapsed,
				ProviderUsed: c.Name(),
			},
			fmt.Errorf("model command-line tool failed: %w (stderr: %s)", err, stderr.String())
	}
	out := stdout.String()
	inTok := estimateTokens(prompt)
	outTok := estimateTokens(out)
	return Response{
		Output:       out,
		InputTokens:  inTok,
		OutputTokens: outTok,
		ElapsedMs:    elapsed,
		CostUSDEstim: estimateCost(model, inTok, outTok),
		ProviderUsed: c.Name(),
	}, nil
}

// ── provider: hosted model API (direct) ────────────────────────────────────

type anthropicAPI struct {
	apiKey string
}

func (a *anthropicAPI) Name() string { return "anthropic-api" }

type anthropicReq struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (a *anthropicAPI) Call(ctx context.Context, model, prompt string, opts Options) (Response, error) {
	opts = opts.withDefaults()
	body, _ := json.Marshal(anthropicReq{
		Model:       model,
		MaxTokens:   opts.MaxTokens,
		Temperature: opts.Temperature,
		Messages:    []anthropicMessage{{Role: "user", Content: prompt}},
	})
	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return Response{ElapsedMs: elapsed, ProviderUsed: a.Name()}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var ar anthropicResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return Response{ElapsedMs: elapsed, ProviderUsed: a.Name()}, err
	}
	if ar.Error != nil {
		return Response{ElapsedMs: elapsed, ProviderUsed: a.Name()},
			errors.New("hosted model api: " + ar.Error.Message)
	}
	var text string
	for _, c := range ar.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	return Response{
		Output:        text,
		InputTokens:   ar.Usage.InputTokens,
		OutputTokens:  ar.Usage.OutputTokens,
		ElapsedMs:     elapsed,
		CostUSDEstim:  estimateCost(model, ar.Usage.InputTokens, ar.Usage.OutputTokens),
		ProviderUsed:  a.Name(),
		RawProviderID: ar.ID,
	}, nil
}

// ── provider: ollama (local) ────────────────────────────────────────────────

// ollamaProvider talks to a local Ollama daemon via its native
// /api/chat endpoint. Cost is hard-coded to $0 — local inference has
// no marginal $ cost, only wall-clock + watts.
type ollamaProvider struct {
	host string
}

func (o *ollamaProvider) Name() string { return "ollama" }

type ollamaReq struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict"`
}

type ollamaResp struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error,omitempty"`
}

func (o *ollamaProvider) Call(ctx context.Context, model, prompt string, opts Options) (Response, error) {
	opts = opts.withDefaults()
	body, _ := json.Marshal(ollamaReq{
		Model:    model,
		Messages: []ollamaMessage{{Role: "user", Content: prompt}},
		Stream:   false,
		Options: ollamaOptions{
			Temperature: opts.Temperature,
			NumPredict:  opts.MaxTokens,
		},
	})
	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		o.host+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return Response{ElapsedMs: elapsed, ProviderUsed: o.Name()},
			fmt.Errorf("ollama: %w (is daemon up at %s?)", err, o.host)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return Response{ElapsedMs: elapsed, ProviderUsed: o.Name()},
			fmt.Errorf("ollama: http %d: %s", resp.StatusCode, string(raw))
	}
	var or ollamaResp
	if err := json.Unmarshal(raw, &or); err != nil {
		return Response{ElapsedMs: elapsed, ProviderUsed: o.Name()},
			fmt.Errorf("ollama: parse response: %w", err)
	}
	if or.Error != "" {
		return Response{ElapsedMs: elapsed, ProviderUsed: o.Name()},
			errors.New("ollama: " + or.Error)
	}
	return Response{
		Output:       or.Message.Content,
		InputTokens:  or.PromptEvalCount,
		OutputTokens: or.EvalCount,
		ElapsedMs:    elapsed,
		CostUSDEstim: 0, // local inference — no $ cost
		ProviderUsed: o.Name(),
	}, nil
}

// ── token + cost estimation ─────────────────────────────────────────────────

// estimateTokens uses a rough 4-char-per-token heuristic. Good enough
// for cost-budget enforcement; not for billing reconciliation.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// CostRate is $/MTok per model (input, output). Update via
// `aicoder bench config set --model-cost=…` in v0.2.x.
type CostRate struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Per-model rate table — sourced from the vendor's public pricing as of
// 2026-01. Wrong rates produce wrong cost estimates but never wrong
// pass/fail verdicts.
var rateTable = map[string]CostRate{
	// Haiku family
	"claude-haiku-4-5-20251001": {InputPerMTok: 1.00, OutputPerMTok: 5.00},
	"claude-haiku-4-5":          {InputPerMTok: 1.00, OutputPerMTok: 5.00},
	// Sonnet family
	"claude-sonnet-4-6": {InputPerMTok: 3.00, OutputPerMTok: 15.00},
	// Opus family
	"claude-opus-4-7": {InputPerMTok: 15.00, OutputPerMTok: 75.00},
}

// estimateCost returns USD; falls back to sonnet rate for unknown models.
func estimateCost(model string, inTok, outTok int) float64 {
	r, ok := rateTable[model]
	if !ok {
		// Fallback to sonnet pricing rather than zero — better to
		// over-estimate slightly than to silently report $0.
		r = rateTable["claude-sonnet-4-6"]
	}
	return (float64(inTok)*r.InputPerMTok + float64(outTok)*r.OutputPerMTok) / 1_000_000.0
}
