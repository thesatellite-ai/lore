#!/usr/bin/env bash
# ci-tools.sh: install what a gate step needs on a plain GitHub Actions runner (.github/workflows/gate.yml).
#
#   tests/lib/ci-tools.sh task [<step>]
#
# "task" installs go-task at TASK_VERSION into ~/.local/bin (added to the job's PATH); a step name
# adds that step's own tools: staticcheck (STATICCHECK_VERSION) for the staticcheck step, docsync's
# ds (DOCSYNC_VERSION) for docsync, ripgrep for the recall report (its grep baseline). Why here and
# not in the steps: the shared gate server has them installed, a runner has none, and lint:staticcheck
# skips when staticcheck is missing, which on a runner would pass a gate that checked nothing.
set -euo pipefail

bin="$HOME/.local/bin"
mkdir -p "$bin"
[ -n "${GITHUB_PATH:-}" ] && echo "$bin" >>"$GITHUB_PATH"
gobin="$(go env GOPATH)/bin"
[ -n "${GITHUB_PATH:-}" ] && echo "$gobin" >>"$GITHUB_PATH"

# A synthetic git identity in the runner's global git config (never GIT_AUTHOR_* in the
# environment: that would override the identities the tests set up themselves). Tests that commit
# outside their own HOME need one, as they have one on a developer's machine.
git config --global user.name "Gate Runner"
git config --global user.email "gate-runner@example.invalid"

for want in "$@"; do
  case "$want" in
  task)
    v="${TASK_VERSION:?TASK_VERSION is not set}"
    curl -fsSL "https://github.com/go-task/task/releases/download/v$v/task_linux_amd64.tar.gz" | tar -xz -C "$bin" task
    "$bin/task" --version
    ;;
  staticcheck)
    go install "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION:?STATICCHECK_VERSION is not set}"
    "$gobin/staticcheck" -version
    ;;
  docsync)
    go install "github.com/ubgo/docsync/cli/cmd/ds@${DOCSYNC_VERSION:?DOCSYNC_VERSION is not set}"
    ;;
  recall)
    sudo apt-get install -y -qq ripgrep >/dev/null
    rg --version | head -1
    ;;
  *) ;; # a step with nothing extra to install
  esac
done
