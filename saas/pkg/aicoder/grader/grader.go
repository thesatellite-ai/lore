// Package grader executes a benchmark eval's grader against a candidate
// output. Replaces the Python-side grader dispatch in bench/runner/run.py
// with an in-process Go implementation that stays close to the ent rows.
//
// Three grader kinds shipped:
//
//	programmatic  — shell command; exit 0 = PASS. $OUTPUT_FILE env points
//	                at a temp file containing the candidate output.
//	llm-judge     — send rubric + output to a judge model; PASS if response
//	                starts with "PASS".
//	golden-diff   — line-set similarity against a golden file; pass if
//	                ratio >= threshold (default 0.85).
//	composite     — list of sub-graders + policy (all-must-pass | majority |
//	                any-must-pass).
//
// Every grader returns a Verdict with a Trace map suitable for serializing
// into BenchResult.grader_trace.
package grader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"saas/pkg/aicoder/llmcall"
)

// Verdict is the result of grading.
type Verdict struct {
	Pass      bool
	Trace     map[string]any // arbitrary; serialized into bench_result.grader_trace
	Cost      float64        // USD; nonzero only for llm-judge
	JudgeInfo *JudgeInfo     // populated only for llm-judge graders
}

// JudgeInfo captures llm-judge details for forensic re-grading and
// judge-consistency audits.
type JudgeInfo struct {
	Model   string
	Rubric  string
	RawResp string
}

// Spec is the parsed shape of bench_eval.grader_spec (which is JSON in the
// DB). Caller is responsible for unmarshalling.
type Spec struct {
	Kind   string         // programmatic | llm-judge | golden-diff | composite
	Fields map[string]any // raw spec JSON
}

// Grade runs the appropriate grader for the spec.
//
// `output` is the candidate string to grade. `caller` is only used by
// llm-judge graders; pass nil if not needed.
func Grade(ctx context.Context, spec Spec, output string, caller llmcall.Caller) (Verdict, error) {
	switch spec.Kind {
	case "programmatic":
		return gradeProgrammatic(ctx, spec.Fields, output)
	case "llm-judge":
		return gradeLLMJudge(ctx, spec.Fields, output, caller)
	case "golden-diff":
		return gradeGoldenDiff(spec.Fields, output)
	case "composite":
		return gradeComposite(ctx, spec.Fields, output, caller)
	default:
		return Verdict{Trace: map[string]any{"error": "unknown grader kind: " + spec.Kind}},
			fmt.Errorf("unknown grader kind: %s", spec.Kind)
	}
}

// ── programmatic ────────────────────────────────────────────────────────────

func gradeProgrammatic(ctx context.Context, fields map[string]any, output string) (Verdict, error) {
	cmd, _ := fields["cmd"].(string)
	if cmd == "" {
		return Verdict{Trace: map[string]any{"error": "missing cmd"}},
			errors.New("programmatic grader: missing cmd field")
	}
	// Write output to a temp file so the cmd can grep $OUTPUT_FILE.
	tmp, err := os.CreateTemp("", "bench-output-*.txt")
	if err != nil {
		return Verdict{Trace: map[string]any{"error": err.Error()}}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(output); err != nil {
		tmp.Close()
		return Verdict{Trace: map[string]any{"error": err.Error()}}, err
	}
	tmp.Close()

	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Env = append(os.Environ(), "OUTPUT_FILE="+tmp.Name())
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	c.Stdout = stdout
	c.Stderr = stderr
	runErr := c.Run()
	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			return Verdict{
				Pass:  false,
				Trace: map[string]any{"error": runErr.Error(), "stdout": stdout.String(), "stderr": stderr.String()},
			}, nil
		}
	}
	return Verdict{
		Pass: exitCode == 0,
		Trace: map[string]any{
			"cmd_exit": exitCode,
			"stdout":   trimMax(stdout.String(), 2048),
			"stderr":   trimMax(stderr.String(), 2048),
		},
	}, nil
}

// ── llm-judge ───────────────────────────────────────────────────────────────

func gradeLLMJudge(ctx context.Context, fields map[string]any, output string, caller llmcall.Caller) (Verdict, error) {
	rubric, _ := fields["rubric"].(string)
	if rubric == "" {
		return Verdict{Trace: map[string]any{"error": "missing rubric"}},
			errors.New("llm-judge grader: missing rubric field")
	}
	judge, _ := fields["judge_model"].(string)
	if judge == "" {
		judge = "claude-opus-4-7"
	}
	if caller == nil {
		return Verdict{Trace: map[string]any{"error": "no llm caller available"}},
			errors.New("llm-judge grader: nil caller")
	}
	prompt := buildJudgePrompt(rubric, output)
	resp, err := caller.Call(ctx, judge, prompt, llmcall.Options{
		Temperature:  0,
		MaxTokens:    64,
		DisableTools: true,
	})
	if err != nil {
		return Verdict{
			Trace: map[string]any{"error": err.Error(), "raw": resp.Output},
		}, err
	}
	verdict := strings.ToUpper(strings.TrimSpace(resp.Output))
	pass := strings.HasPrefix(verdict, "PASS")
	return Verdict{
		Pass: pass,
		Trace: map[string]any{
			"verdict":     verdict,
			"raw":         trimMax(resp.Output, 4096),
			"judge_model": judge,
			"elapsed_ms":  resp.ElapsedMs,
		},
		Cost: resp.CostUSDEstim,
		JudgeInfo: &JudgeInfo{
			Model:   judge,
			Rubric:  rubric,
			RawResp: resp.Output,
		},
	}, nil
}

func buildJudgePrompt(rubric, output string) string {
	return "You are a strict grader. Apply the rubric below to the candidate " +
		"response. Answer ONLY with a single token: PASS or FAIL.\n\n" +
		"RUBRIC:\n" + rubric + "\n\n" +
		"CANDIDATE RESPONSE:\n" + output + "\n"
}

// ── golden-diff ─────────────────────────────────────────────────────────────

func gradeGoldenDiff(fields map[string]any, output string) (Verdict, error) {
	goldenPath, _ := fields["golden_file"].(string)
	threshold, _ := fields["threshold"].(float64)
	if threshold <= 0 {
		threshold = 0.85
	}
	if goldenPath == "" {
		return Verdict{Trace: map[string]any{"error": "missing golden_file"}},
			errors.New("golden-diff grader: missing golden_file field")
	}
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		return Verdict{Trace: map[string]any{"error": err.Error()}}, err
	}
	goldenLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	outputLines := strings.Split(strings.TrimSpace(output), "\n")
	in := map[string]bool{}
	for _, l := range outputLines {
		in[l] = true
	}
	matches := 0
	for _, l := range goldenLines {
		if in[l] {
			matches++
		}
	}
	total := len(goldenLines)
	ratio := 0.0
	if total > 0 {
		ratio = float64(matches) / float64(total)
	}
	return Verdict{
		Pass: ratio >= threshold,
		Trace: map[string]any{
			"matches":   matches,
			"total":     total,
			"ratio":     ratio,
			"threshold": threshold,
		},
	}, nil
}

// ── composite ───────────────────────────────────────────────────────────────

func gradeComposite(ctx context.Context, fields map[string]any, output string, caller llmcall.Caller) (Verdict, error) {
	policy, _ := fields["policy"].(string)
	if policy == "" {
		policy = "all-must-pass"
	}
	checks, _ := fields["checks"].([]any)
	if len(checks) == 0 {
		return Verdict{Trace: map[string]any{"error": "missing checks"}},
			errors.New("composite grader: missing checks field")
	}
	pass, fail := 0, 0
	traces := []any{}
	for _, c := range checks {
		cm, ok := c.(map[string]any)
		if !ok {
			fail++
			continue
		}
		kind, _ := cm["kind"].(string)
		v, _ := Grade(ctx, Spec{Kind: kind, Fields: cm}, output, caller)
		traces = append(traces, map[string]any{
			"kind":  kind,
			"pass":  v.Pass,
			"trace": v.Trace,
		})
		if v.Pass {
			pass++
		} else {
			fail++
		}
	}
	finalPass := false
	switch policy {
	case "all-must-pass":
		finalPass = fail == 0
	case "any-must-pass":
		finalPass = pass > 0
	case "majority":
		finalPass = pass > fail
	default:
		finalPass = fail == 0
	}
	return Verdict{
		Pass: finalPass,
		Trace: map[string]any{
			"policy":     policy,
			"pass_count": pass,
			"fail_count": fail,
			"sub_traces": traces,
		},
	}, nil
}

// trimMax bounds a string to n chars (UTF-8 safe at byte boundaries —
// we accept potential mid-codepoint cut as the trace is diagnostic).
func trimMax(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
