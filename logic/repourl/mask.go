package repourl

// Mask and MaskText are the one rule for showing a URL (or a text that
// may hold one) to a person, a log or an agent's transcript.
//
// A store's or remote's plain address (git@host:org/x.git,
// https://host/org/x.git, a local path) is not a secret: it is printed as
// it is, on a terminal, in a pipe and in CI alike. Only the secret part of
// a URL is replaced with Masked, and the rest stays readable:
//
//	https://user:<password>@host/x.git ─▶ https://user:<redacted>@host/x.git
//	https://<token>@host/x.git     ─▶ https://<redacted>@host/x.git   (token-shaped user, TokenShaped)
//	ssh://git:<password>@host/x.git    ─▶ ssh://git:<redacted>@host/x.git
//	user:<password>@host:org/x.git ─▶ user:<redacted>@host:org/x.git
//	https://host/x?access_token=t  ─▶ https://host/x?access_token=<redacted>
//	git@host:org/x.git, /srv/x, https://mira@host/x  ─▶ unchanged
//
// Mask never parses and re-encodes the URL (url.URL.String would change
// the person's spelling): it cuts the user info and the query by hand, so
// everything that is not a secret comes back byte for byte.

import (
	"regexp"
	"strings"
)

// Masked replaces the secret part of a URL.
const Masked = "<redacted>"

// secretQueryKeys are query parameter names whose value is a credential
// (compared lower-cased).
var secretQueryKeys = map[string]bool{
	"token": true, "access_token": true, "private_token": true, "auth_token": true, "oauth_token": true,
	"password": true, "passwd": true, "pwd": true, "secret": true, "client_secret": true,
	"key": true, "api_key": true, "apikey": true, "sig": true, "signature": true, "x-access-token": true,
}

// Mask returns raw with only its credentials replaced by Masked: a
// password in the user info, a token-shaped user name, and the value of a
// credential query parameter (secretQueryKeys). Anything else, including
// a plain address or a local path, is returned unchanged.
func Mask(raw string) string {
	if !strings.ContainsAny(raw, "@?") {
		return raw // no user info, no query: nothing can be secret
	}
	scheme, rest := "", raw
	if i := strings.Index(raw, schemeSep); i >= 0 {
		scheme, rest = raw[:i+len(schemeSep)], raw[i+len(schemeSep):]
	}
	// the authority ends at the first "/", "?" or "#" (scp-like: the
	// path follows a ":" after the "@", which LastIndex below skips)
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	auth, tail := rest[:end], rest[end:]
	if at := strings.LastIndex(auth, "@"); at >= 0 {
		if scheme != "" {
			auth = maskUserinfo(auth[:at], TokenShaped) + auth[at:]
		} else if scpHost(auth[at+1:]) {
			// scp-like (user@host:path): a "user:pw@" is a password, and
			// only a known token prefix marks a token user (the entropy
			// rule would mask long plain logins). Without the host's ":"
			// it is no URL at all: human:alice@example.com is an actor
			// key, an address in a sentence is an email, both stay.
			auth = maskUserinfo(auth[:at], knownTokenUser) + auth[at:]
		}
	}
	return scheme + auth + maskQuery(tail)
}

// maskUserinfo masks the user info of a URL (the part before its "@"):
// all of it when the user name is a token, else only a password.
func maskUserinfo(ui string, isToken func(string) bool) string {
	user, _, hasPw := strings.Cut(ui, ":")
	switch {
	case isToken(user):
		return Masked
	case hasPw:
		return user + ":" + Masked
	default:
		return ui
	}
}

// scpHost reports whether the part after a scheme-less URL's "@" is
// scp-like "host:path" (a ":" with text on both sides).
func scpHost(s string) bool {
	c := strings.Index(s, ":")
	return c > 0 && c < len(s)-1
}

// knownTokenUser reports a user name that starts with a token prefix
// (tokenUserPrefixes), without TokenShaped's entropy rule.
func knownTokenUser(user string) bool {
	low := strings.ToLower(user)
	for _, p := range tokenUserPrefixes {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// maskQuery masks credential values in the query of tail (the URL from
// its path on); the path and fragment stay.
func maskQuery(tail string) string {
	q := strings.Index(tail, "?")
	if q < 0 {
		return tail
	}
	head, query, frag := tail[:q+1], tail[q+1:], ""
	if h := strings.Index(query, "#"); h >= 0 {
		query, frag = query[:h], query[h:]
	}
	parts := strings.Split(query, "&")
	for i, p := range parts {
		if k, v, ok := strings.Cut(p, "="); ok && v != "" && secretQueryKeys[strings.ToLower(k)] {
			parts[i] = k + "=" + Masked
		}
	}
	return head + strings.Join(parts, "&") + frag
}

// urlWord is one space-delimited word of a text that may be a URL: it
// holds "://", "@" or "?" (MaskText masks each with Mask).
var urlWord = regexp.MustCompile(`[^\s"'<>()\[\],]*(?:://|@|\?)[^\s"'<>()\[\],]*`)

// MaskText applies Mask to every URL-like word of text (an error message,
// a hint, a doctor line), so a credential inside a sentence is masked and
// every plain address in it stays readable.
func MaskText(text string) string {
	if !strings.ContainsAny(text, "@?") {
		return text
	}
	return urlWord.ReplaceAllStringFunc(text, Mask)
}
