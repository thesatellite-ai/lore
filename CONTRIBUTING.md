# Contributing to lore

Thanks for your interest. `lore` is source-available under the
[PolyForm Perimeter License 1.0.0](LICENSE) — commercial use is fine; only
providing a product that competes with `lore` is disallowed. The license is
perpetual (it does not convert to a permissive license over time).
Contributions are welcome, with the licensing terms below.

## Before you start

- Open an issue describing the bug/feature before large changes, so we can
  agree on the approach.
- Small fixes (docs, typos, obvious bugs) can go straight to a PR.

## Development

See **[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)** for layout, build, and
release. Quick loop:

```sh
task lore:build
task lore:test
```

## Pull requests

- Branch from `main`; keep PRs focused.
- `gofmt` clean; `go vet ./saas/cmd/cli/...` clean; tests pass (`task lore:test`).
- Use Conventional Commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`,
  `test:`). No AI-attribution / co-author trailers in commit messages.
- A push to `main` auto-publishes a release — maintainers handle merges with
  that in mind. Use `[skip release]` in the commit subject for docs-only
  changes.

## Contributor license terms

By submitting a contribution you agree that:

1. You have the right to submit it (it's your original work or you have
   permission), and
2. You license your contribution to the project under the same
   [PolyForm Perimeter License 1.0.0](LICENSE) terms, and grant the project
   owner the right to distribute it as part of the Software.

If you can't agree to this, please don't submit a PR — open an issue instead.

## Reporting security issues

Do **not** open a public issue for security problems. Contact the maintainer
privately (see the repo's security policy or owner profile).
