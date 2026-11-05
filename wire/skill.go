package wire

import (
	"context"
	"os"

	"github.com/thesatellite-ai/lore/adapters/llmcall"
	opsskill "github.com/thesatellite-ai/lore/ops/skill"
)

// SkillModel is `lore skill compile`'s model caller (opsskill.Env,
// P3-SKILL-1): adapters/llmcall's router, configured from the environment
// here at the composition root (the hosted model's API when its key is
// set, else its CLI; ollama:<model> for a local Ollama).
func (a *App) SkillModel(context.Context) (opsskill.Model, error) {
	return skillModel{c: llmcall.Auto(llmcall.Config{
		AnthropicKey: os.Getenv(llmcall.EnvAnthropicKey),
		OllamaHost:   os.Getenv(llmcall.EnvOllamaHost),
	})}, nil
}

// skillModel adapts an llmcall.Caller to the operation's port.
type skillModel struct{ c llmcall.Caller }

// Compress runs one model call.
func (m skillModel) Compress(ctx context.Context, model, prompt string, o opsskill.CallOptions) (opsskill.Reply, error) {
	r, err := m.c.Call(ctx, model, prompt, llmcall.Options{Temperature: o.Temperature, MaxTokens: o.MaxTokens, Timeout: o.Timeout})
	if err != nil {
		return opsskill.Reply{}, err
	}
	return opsskill.Reply{Output: r.Output, CostUSD: r.CostUSDEstim, ElapsedMs: r.ElapsedMs}, nil
}
