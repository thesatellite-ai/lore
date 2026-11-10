package wire

import (
	"context"
	"os"

	"github.com/thesatellite-ai/lore/adapters/llmcall"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsskill "github.com/thesatellite-ai/lore/ops/skill"
)

// SkillHome is the home folder the tools' skill folders live under
// (opsskill.InstallEnv, ONBOARDING_DESIGN 4.7.1).
func (a *App) SkillHome(context.Context) (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", errs.Wrap(errs.Internal, "find the home folder for the skill install", err).WithHint("set HOME")
	}
	return h, nil
}

// SkillTools is the default tool list's chooser (opsskill.InstallEnv, ONB
// 4.7.1): the active setup's tools, else the skill-capable tools whose
// home folder exists (opsskill.HomeDetector).
func (a *App) SkillTools(ctx context.Context) (opsskill.ToolChooser, error) {
	home, err := a.SkillHome(ctx)
	if err != nil {
		return nil, err
	}
	user, err := a.ToolProfiles(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := toolprofile.New(user)
	if err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "context.profiles", err).WithHint("check it with `lore config check`")
	}
	return setupToolChooser{a: a, det: opsskill.HomeDetector{Home: home, Registry: reg}}, nil
}

// setupToolChooser is SkillTools' chooser.
type setupToolChooser struct {
	a   *App
	det opsskill.HomeDetector
}

// ActiveSetupTools is the active setup's `tools` (3.3 resolution, the
// global --setup included). No setup, an empty list, or a setup that does
// not resolve here (ambiguous folders) falls back to detection: installing
// a skill is harmless, and `lore doctor` reports the setup problem itself.
func (c setupToolChooser) ActiveSetupTools(ctx context.Context) ([]string, bool, error) {
	res, err := c.a.SetupResolution(ctx, op.SetupOf(ctx))
	if err != nil || res.Active.Name == "" || len(res.Entry.Tools) == 0 {
		return nil, false, nil
	}
	return res.Entry.Tools, true, nil
}

// DetectedTools is opsskill.HomeDetector's.
func (c setupToolChooser) DetectedTools(ctx context.Context) ([]string, error) {
	return c.det.DetectedTools(ctx)
}

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
