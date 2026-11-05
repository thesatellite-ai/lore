package gitleaks

import "github.com/thesatellite-ai/lore/logic/scan"

// Lore pack rule ids. They are snake_case on purpose so they never collide
// with gitleaks' kebab-case ids: a secret_rules row names exactly one rule.
const (
	RuleAWSAccessKey        = "aws_access_key"
	RuleGitHubToken         = "github_token"
	RuleJWT                 = "jwt"
	RuleStripeLiveKey       = "stripe_live_key"
	RuleStripeTestKey       = "stripe_test_key"
	RuleModelKey            = "model_key"
	RuleSlackToken          = "slack_token"
	RuleGoogleAPIKey        = "google_api_key"
	RulePEMPrivateKey       = "pem_private_key"
	RuleShopifyToken        = "shopify_token"
	RuleNPMToken            = "npm_token"
	RuleTwilioKey           = "twilio_key"
	RuleAuthorizationBearer = "authorization_bearer"
	RulePasswordPair        = "password_pair"
	RuleURLWithPassword     = "url_with_password"
	RuleURLUserinfoID       = "url_token_user"
)

// passwordPairEntropy is the minimum Shannon entropy (bits/char) of a whole
// password_pair match ("password = aaaaaaaaaaaa" sits below it). Kept
// beside the D10 prose filter: it only drops repeated-character noise.
const passwordPairEntropy = 3.0

// Pack returns lore's own rule pack (SQ8), a fresh slice each call so a
// caller can never mutate the defaults. Ported from
// saas/pkg/aicoder/security/scrub.go; every regex is RE2 (linear time).
func Pack() []scan.Rule {
	b := func(id, desc, re string, kw ...string) scan.Rule {
		return scan.Rule{ID: id, Description: desc, Regex: re, Keywords: kw, Source: scan.SourceBuiltin}
	}
	// D10 / 3.4: the stricter password_pair; its post-filter proseValue
	// drops placeholders and an unquoted plain word followed by prose.
	pw := b(RulePasswordPair, "password=literal; an unquoted plain word followed by more prose on the line is not a hit "+
		"(an all-letters password followed by prose is missed: the accepted trade-off)",
		`(?i)\b(?:password|passwd|pwd|passphrase)\s*[=:]\s*(?:"([^"\r\n]{6,})"|'([^'\r\n]{6,})'|([^\s"'<>${}*,;]{6,}))`,
		"password", "passwd", "pwd", "passphrase")
	pw.Entropy = passwordPairEntropy
	return []scan.Rule{
		b(RuleAWSAccessKey, "AWS access key id", `\b(?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASCA)[A-Z0-9]{16}\b`,
			"akia", "asia", "agpa", "aida", "aroa", "aipa", "anpa", "anva", "asca"),
		b(RuleGitHubToken, "GitHub token", `\b(?:gh[pousr]_[A-Za-z0-9_]{36,251}|github_pat_[A-Za-z0-9_]{22,255})\b`, "gh", "github_pat_"),
		b(RuleJWT, "JSON web token", `\beyJ[A-Za-z0-9_=-]+\.eyJ[A-Za-z0-9_=-]+\.[A-Za-z0-9_=.+/-]+`, "eyj"),
		b(RuleStripeLiveKey, "Stripe live key", `\b(?:sk|rk)_live_[A-Za-z0-9]{24,}\b`, "_live_"),
		b(RuleStripeTestKey, "Stripe test key", `\b(?:sk|rk)_test_[A-Za-z0-9]{24,}\b`, "_test_"),
		b(RuleModelKey, "hosted model API key (sk-…, sk-ant-…, sk-proj-…)", `\bsk-[A-Za-z0-9_-]{20,}`, "sk-"),
		b(RuleSlackToken, "Slack token", `\bxox[abprs]-[A-Za-z0-9-]{10,}`, "xox"),
		b(RuleGoogleAPIKey, "Google API key", `\bAIza[0-9A-Za-z_-]{35}\b`, "aiza"),
		b(RulePEMPrivateKey, "PEM or OpenSSH private key block", `-----BEGIN [A-Z ]*PRIVATE KEY-----`, "private key"),
		b(RuleShopifyToken, "Shopify access token", `\bshp(?:at|ss|ca|pa)_[a-fA-F0-9]{32}\b`, "shpat_", "shpss_", "shpca_", "shppa_"),
		b(RuleNPMToken, "npm token", `\bnpm_[A-Za-z0-9]{36}\b`, "npm_"),
		b(RuleTwilioKey, "Twilio API key", `\bSK[0-9a-fA-F]{32}\b`, "sk"),
		b(RuleAuthorizationBearer, "Authorization: Bearer header with a literal token",
			`(?i)\bauthorization\s*[:=]\s*["']?bearer\s+[A-Za-z0-9._~+/=-]{12,}`, "bearer"),
		pw,
		// scheme://user:password@host. A bare user@host has no ":<secret>@";
		// placeholder passwords (<x>, ${x}, ***) are excluded by the class.
		b(RuleURLWithPassword, "URL with an inline password",
			`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+:[^\s/@<>${}*]{3,}@[^\s/]+`, "://"),
		// 14.6 / D26: http(s)://<token>@host. The post-filter tokenUser
		// keeps only a token-shaped user name (repourl.TokenShaped).
		b(RuleURLUserinfoID, "http(s) URL whose user name is a token",
			`(?i)\bhttps?://[^\s:/@<>${}*]{4,}@[^\s/]+`, "http"),
	}
}

// Covers maps each lore pack rule id to the gitleaks default rule ids that
// cover the same token shapes (SECRETS 3.3: `lore secrets rules
// --replaced`). Both run; a finding hit by both is reported once under the
// lore id (19.10). A lore rule with no entry has no gitleaks match at all
// (url_with_password, url_token_user, authorization_bearer, password_pair).
// Ids are those of the gitleaks version in go.mod (v8.30).
func Covers() map[string][]string {
	return map[string][]string{
		RuleAWSAccessKey:  {"aws-access-token"},
		RuleGitHubToken:   {"github-pat", "github-fine-grained-pat", "github-oauth", "github-app-token", "github-refresh-token"},
		RuleJWT:           {"jwt", "jwt-base64"},
		RuleStripeLiveKey: {"stripe-access-token"},
		RuleStripeTestKey: {"stripe-access-token"},
		RuleModelKey:      {"openai-api-key", "anthropic-api-key", "anthropic-admin-api-key"},
		RuleSlackToken:    {"slack-bot-token", "slack-user-token", "slack-app-token", "slack-legacy-token", "slack-legacy-bot-token", "slack-legacy-workspace-token", "slack-config-access-token", "slack-config-refresh-token"},
		RuleGoogleAPIKey:  {"gcp-api-key"},
		RulePEMPrivateKey: {"private-key"},
		RuleShopifyToken:  {"shopify-access-token", "shopify-custom-access-token", "shopify-private-app-access-token", "shopify-shared-secret"},
		RuleNPMToken:      {"npm-access-token"},
		RuleTwilioKey:     {"twilio-api-key"},
	}
}
