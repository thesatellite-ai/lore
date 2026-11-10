// Package probe is the onboarding engine's read-only probes (layer L3,
// ONBOARDING_DESIGN 2.1): git ls-remote, the dry-run write test, the store
// marker check, ssh -T, gh auth status and gh api reads, ~/.ssh/config and
// public-key reads, git config reads, PATH lookups and coding-tool and
// secrets-manager detection.
//
//	ops/setup.Prober (implemented in wire)
//	   └► Prober.<Probe>(ctx, …)
//	         └► Limiter (at most DefaultConcurrency probes machine-wide, ONB 6.1)
//	               └► run: LookPath ─► exec with the probe's timeout (kill tree on expiry)
//	                     stdout ─► parsed into a typed result
//	                     stderr ─► classified (Class) and dropped; Detail is one
//	                               sanitised line (no user:token@, no token shapes)
//
// Rules every probe keeps:
//   - Nothing is written: no remote ref (the write test is a dry-run push,
//     ONB 4.4), no git config, no ~/.ssh file, no known_hosts entry. The only
//     local write is the write test's temp bare repo under Options.TmpDir,
//     removed before the probe returns.
//   - No token or private key is ever read or returned: gh runs without
//     --show-token and its token lines are never parsed; KeyFingerprint
//     refuses any path that is not a .pub file holding a public key, and
//     KeyFileFingerprint hands a private key file's path (never its
//     content) to ssh-keygen -lf, which prints only the fingerprint. The
//     https route's credential helper runs gh auth token inside git's own
//     process per call; lore never sees or stores the token.
//   - Every remote call takes a Route, so a setup's ssh host alias (Q5) is
//     used: -c url.git@<alias>:.insteadOf=git@github.com: on git, the alias as
//     the ssh host, and GIT_SSH_COMMAND with -i <key> -o IdentitiesOnly=yes
//     only when there is no alias.
//   - No hidden state: the limiter, timeouts, HOME, PATH lookup and
//     environment are all in Options; wire builds one Prober per process and
//     shares it, which makes the limit machine-wide for that process.
package probe
