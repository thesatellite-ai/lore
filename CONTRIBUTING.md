# Contributing to lore

Thanks for your interest. `lore` is open source under the [Apache License 2.0](LICENSE) (releases up to v0.1.17 were published under the PolyForm Perimeter License 1.0.0). Contributions are welcome, with the licensing terms below.

## Before you start

- Open an issue describing the bug/feature before large changes, so we can agree on the approach.
- Small fixes (docs, typos, obvious bugs) can go straight to a PR.

## Development

See **[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)** for layout, build, and release. Quick loop:

```sh
task lore:hooks:install   # once: the pre-push hook runs the full gate
task lore:build
task lore:test:quick      # quick tier: unit tests, cached, no race, no e2e — run it all the time
task check:fast           # fast tier (~5 min): enough to land a small fix; the full gate then runs on main
task check                # full tier, the gate — must pass before you push anything bigger
```

The landing rule (a small fix after `check:fast`, then `task check:after-land` on the landed `main`, a failure there fixed at once; a release always on a full pass) is in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md#landing-checkfast-for-a-small-fix-the-full-gate-on-main).

Before a release, or after changing sync, leak checks, claims or landing, also run the manual [real-repo trial](docs/team/real-repo-trial.md) (`task lore:trial:github`) against two empty private repos of your own. It is never part of `task check` or CI.

## Pull requests

- Branch from `main`; keep PRs focused.
- `task check` passes (build, lint, cross-builds, tidy, docs, unit and race tests, and every end-to-end suite: userscen, team, setup page, machines, journeys, scenarios, chaos, docs examples), or for a small fix `task check:fast` (then the full gate on `main` right after it lands). The pre-push hook enforces one of them once `task lore:hooks:install` has run.
- Use Conventional Commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`, `test:`). No attribution trailers for coding agents or tools, and no co-author trailers, in commit messages.
- A push to `main` without `[skip release]` in the commit subject tags a version (`vX.Y.Z`, the next patch); binaries are built and published separately. Maintainers handle merges with that in mind. Use `[skip release]` in the commit subject for docs-only changes.

## Contributor license terms

By submitting a contribution you agree that:

1. You have the right to submit it (it's your original work or you have permission), and
2. You license your contribution to the project under the [Apache License 2.0](LICENSE), as described in its section 5.

If you can't agree to this, please don't submit a PR — open an issue instead.

## Reporting security issues

Do **not** open a public issue for security problems. Contact the maintainer privately (see the repo's security policy or owner profile).
