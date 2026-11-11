#!/usr/bin/env bash
# run-examples.sh: run every command example shown in docs/team/** against the
# real lore binary, in throwaway worlds (fake GitHub, one HOME per person), and
# write what each command printed to tests/team/docs/transcripts/<section>.txt.
#
# The docs paste their examples from these transcripts, so every example is a
# real run. The script exits 1 when any command that should succeed fails, or
# any command that should fail (the error examples) succeeds.
#
#   tests/team/docs/run-examples.sh                 # build lore, run every section
#   tests/team/docs/run-examples.sh agents leaks    # only these sections
#   LORE_BIN=/path/to/lore tests/team/docs/run-examples.sh   # use a built binary
#   JOBS=1 tests/team/docs/run-examples.sh          # one section at a time (default: JOBS=8)
#
# Two or more sections run in parallel, JOBS at a time, each as its own run of this script
# (tests/lib/parallel-sections.sh): every section starts its own world and transcript, so the
# order they run in changes nothing they print.
#
# The binary is built with -tags loretest so LORE_NOW (a fixed clock) works:
# the agents page shows a lease expiring without waiting twenty minutes. That
# tag changes nothing else in lore's behaviour or output.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
OUT="$HERE/transcripts"
# The worlds live under /tmp, never under $TMPDIR: lore prints real paths (a file:// store url
# under ~) and aligns table columns to them, and clean() shortens the home folder to "~"
# only afterwards, so the padding left in a transcript follows the length of this path. A
# fixed-length root keeps every run (any TMPDIR, any gate step) byte-identical.
SCRATCH="$(mktemp -d "/tmp/lore-docs-examples.XXXXXX")"
[ -n "${KEEP:-}" ] && echo "keeping worlds in $SCRATCH" >&2 || trap 'rm -rf "$SCRATCH"' EXIT

if [ -z "${LORE_BIN:-}" ]; then
  LORE_BIN="$SCRATCH/bin/lore"
  (cd "$REPO" && CGO_ENABLED=0 go build -tags loretest -o "$LORE_BIN" ./cmd/lore)
fi
export LORE_BIN

# shellcheck source=lib.sh
source "$HERE/lib.sh"
# shellcheck source=../../lib/parallel-sections.sh
source "$REPO/tests/lib/parallel-sections.sh"
# How many sections run at once. 8: each section keeps about one core busy, and the gate runs
# this beside other suites under its load budget (docs/DEVELOPMENT.md "The gate").
JOBS="${JOBS:-8}"

# section NAME: start a transcript and a fresh world for one docs section.
section() {
  # Normalize the previous section's transcript (never the default /dev/stdout: that is the caller's terminal or log).
  [[ "${TRANSCRIPT:-}" == "$OUT"/* ]] && [ -f "$TRANSCRIPT" ] && python3 "$HERE/normalize.py" "$TRANSCRIPT"
  mkdir -p "$OUT"
  TRANSCRIPT="$OUT/$1.txt"
  : > "$TRANSCRIPT"
  new_world "$SCRATCH/world-$1"
  # Every section runs on the test clock (LORE_NOW and git dates), so times, ages and
  # commit dates are the same on every run; normalize.py then fixes the random ids.
  CLOCK=2026-11-02T10:00:00Z STEP=5
  echo "== $1" >&2
}

# note TEXT: a heading inside a transcript, so a docs page can find its part.
note() { printf '\n### %s\n' "$*" >> "$TRANSCRIPT"; }

# config NAME: write ~/.lore/config.yaml for NAME from stdin.
config() {
  mkdir -p "$HOMES/$1/.lore"
  cat > "$HOMES/$1/.lore/config.yaml"
}

# make_store PERSON NAME URL: setup step, URL becomes a lore store (0.1.18: `lore store init <name> <url>`
# runs inside a repo and attaches the store there by name). It runs in ~/work/stores, a plain git
# folder of PERSON's that no example reads, so no client repo ever names the store.
make_store() {
  quiet "$1" work "mkdir -p stores && cd stores && { [ -d .git ] || git init -q; } && lore store init $2 '$3' --yes"
}

# client_repo SLUG DIR [PERSON]: the client (default carol) creates SLUG with an initial commit and lore set up.
client_repo() {
  local slug="$1" dir="$2" who="${3:-carol}"
  remote "$slug"
  [ -d "$HOMES/$who" ] || person "$who" "$who@${slug%%/*}.example"
  quiet "$who" work "git clone -q https://github.com/$slug $dir && cd $dir && echo '# $dir' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=$dir >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"
}

# agency_config NAME KEY DISPLAY HANDLE: the agency's org entry (one monorepo store).
agency_config() {
  config "$1" <<Y
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "$2", name: "$3", handle: $4}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:   [github.com/agency/*]
    repos: [github.com/client/*, github.com/client2/*, github.com/client3/*]
    defaults:
      client_repos: {task: agency, memory: agency, rule: repo, decision: repo}
Y
}

# ---------------------------------------------------------------------------
# getting-started: one person, one repo, then a first org.
s_getting_started() {
  section getting-started
  remote alice/notes-app
  person alice alice@example.com
  quiet alice work "git clone -q https://github.com/alice/notes-app && cd notes-app && echo '# notes' > README.md && git add . && git commit -qm init && git push -q origin main"

  note "version"
  on alice work/notes-app "lore --version"

  note "init"
  on alice work/notes-app "lore init --non-interactive --name=notes-app"
  on alice work/notes-app "git add -A && git commit -qm 'lore: init' && git push -q origin main"

  note "whoami without config"
  on alice work/notes-app "lore whoami"

  note "first live task"
  on alice work/notes-app "lore task add 'fix login timeout'"
  local t; t=$(id tsk)
  on alice work/notes-app "lore task list"
  on alice work/notes-app "git status --short"
  on alice work/notes-app "git ls-remote origin"
  on alice work/notes-app "lore live show"

  note "first landing"
  on alice work/notes-app "lore task done $t"
  on alice work/notes-app "git fetch -q origin && git log --oneline origin/main"
  on alice work/notes-app "lore live show"
  on alice work/notes-app "lore sync"
  on alice work/notes-app "lore live show"

  note "old identity file"
  # A v0.1.17 ~/.lore/identity.yaml: read until `lore migrate run` moves it into
  # identity: of config.yaml (CFG9); the old file is kept as .migrated (OWNERQ-8).
  quiet alice . "mkdir -p .lore && printf 'stable_key: human:alice@example.com\ndisplay_name: Alice Doe\n' > .lore/identity.yaml"
  on alice work/notes-app "lore identity show"
  on alice work/notes-app "lore migrate run"
  on alice . "ls ~/.lore; cat ~/.lore/config.yaml"

  note "first config"
  config alice <<'Y'
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:   [github.com/agency/*]
    repos: [github.com/client/*]
Y
  on alice . "cat ~/.lore/config.yaml"
  on alice . "lore config check"
  remote agency/notes
  remote agency/tools
  quiet alice work "git clone -q https://github.com/agency/tools && cd tools && echo '# tools' > README.md && git add . && git commit -qm init && git push -q origin main"
  on alice work/tools "lore init --non-interactive --name=tools"
  # 0.1.18: a store is made from inside a repo, which attaches it by name (.lore/stores.yaml);
  # the agency's own repo is the place (a client repo never names the store).
  on alice work/tools "lore store init agency git@github.com:agency/notes --yes"
  TTY=1 on alice work/tools "lore whoami"
  on alice work/tools "lore actor register"
  on alice work/notes-app "lore whoami"
}

# ---------------------------------------------------------------------------
# solo: one person, a repo with no remote, then the old behaviour on purpose.
s_solo() {
  section solo
  person alice alice@example.com
  quiet alice work "mkdir -p scratch && cd scratch && git init -q && git commit -q --allow-empty -m init"

  note "no remote"
  on alice work/scratch "lore init --non-interactive --name=scratch >/dev/null 2>&1 && lore rule add --body 'use pnpm, never npm'"
  on alice work/scratch "lore task add 'write the README'"
  local t; t=$(id tsk)
  on alice work/scratch "lore task done $t"
  on alice work/scratch "lore land --all --dry-run"
  on alice work/scratch "git status --short"

  note "tasks with the code"
  config alice <<'Y'
live:
  defaults: {tasks: false}
Y
  on alice work/scratch "lore task add 'tidy the scripts folder'"
  on alice work/scratch "git status --short"
}

# ---------------------------------------------------------------------------
# small-team: three developers on branches in one repo, no org config at all.
s_small_team() {
  section small-team
  remote agency/billing
  person alice alice@agency.example; person bob bob@agency.example
  quiet alice work "git clone -q https://github.com/agency/billing && cd billing && echo '# billing' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=billing >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"
  quiet bob work "git clone -q https://github.com/agency/billing"

  note "register"
  on alice work/billing "lore actor register"
  on bob work/billing "lore actor register"

  note "assign across branches"
  on bob work/billing "git pull -q && git switch -q -c feat/payments && lore task add 'check the checkout page on a phone' --assign @alice"
  local t1; t1=$(id tsk)
  on alice work/billing "git switch -q -c feat/login && lore task list --mine"
  on alice work/billing "lore sync && lore task list --mine"
  on alice work/billing "lore inbox"

  note "done lands on main"
  on alice work/billing "lore task start $t1"
  on alice work/billing "echo ok > checkout.txt && git add checkout.txt && git commit -qm 'fix the checkout page on phones' && lore task done $t1 --commit \$(git rev-parse --short HEAD)"
  on alice work/billing "git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3"
  on alice work/billing "git log --oneline -1 && git status --short"

  note "urgent rule"
  # 0.1.18: no --live on rules (no live ref on the core); the rule is copied onto main with sync promote.
  on bob work/billing "lore rule add --body 'Never deploy on a Friday'"
  local r1; r1=$(id rul)
  on bob work/billing "lore sync promote $r1 --to main --dry-run"
  # promote commits onto the LOCAL main (it never pushes): bring it up to date first.
  on bob work/billing "git fetch -q origin main:main && lore sync promote $r1 --to main"
  on bob work/billing "git push -q origin main && git fetch -q origin && git log --oneline -1 origin/main"

  note "explain"
  on alice work/billing "lore explain $t1"

  note "protected main"
  protect agency/billing
  on bob work/billing "lore task add 'ask the client for the new logo' --assign @alice"
  local t2; t2=$(id tsk)
  on alice work/billing "lore sync >/dev/null && lore task done $t2"
  on alice work/billing "lore explain $t2"
  on alice work/billing "echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'"
  on alice work/billing "git log --oneline -3"
  on alice work/billing "git push -q -u origin feat/login"
  on alice work/billing "git status --short"
}

# ---------------------------------------------------------------------------
# agency: the agency works in the client repo client/website and keeps internal notes
# in its store agency/notes; the client's own developer (carol) uses lore too.
s_agency() {
  section agency
  person alice alice@agency.example; person bob bob@agency.example; person carol carol@client.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  agency_config bob "human:bob@agency.example" "Bob Roe" bob
  client_repo client/website app
  remote agency/notes
  remote agency/tools
  quiet alice work "git clone -q https://github.com/agency/tools && cd tools && echo '# tools' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=tools >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"

  note "store init"
  # 0.1.18: the store is made from the agency's own repo, which attaches it by name;
  # the client repo never names it (the link below lives only in the store).
  on alice work/tools "lore store init agency git@github.com:agency/notes --yes"
  quiet alice work "git clone -q https://github.com/client/website website"
  quiet bob work "git clone -q https://github.com/client/website website"

  note "link"
  TTY=1 on alice work/website "lore whoami"
  TTY=1 ANSWER=y on alice work/website "lore org link agency --project client"
  on alice work/website "git status --short"
  on alice work/website "lore actor register"
  on bob work/website "lore actor register"
  TTY=1 on alice work/website "lore whoami"
  on alice work/website "lore whoami"

  note "internal by default"
  on alice work/website "lore memory add --body 'they approve estimates faster when we send them on WhatsApp first'"
  local m1; m1=$(id mem)
  on alice work/website "lore rule add --body 'run the tests before you push' --to repo"
  on alice work/website "git status --short"
  on alice work/website "git add -A && git commit -qm 'lore: rule' && git push -q origin main"

  note "internal task"
  on alice work/website "git switch -q -c feat/checkout && lore task add 'send invoice reminder before demo' --to agency --assign @bob"
  local t1; t1=$(id tsk)
  on bob work/website "lore sync >/dev/null; git switch -q -c feat/login && lore task list"
  on bob work/website "lore inbox"
  on bob work/website "lore task done $t1"
  on bob work/website "git status --short"

  note "render"
  # 0.1.18: one private render file per internal store under ~/.lore/render/<repo_id>/, with an index;
  # the checkout gets nothing (no CLAUDE.local.md import line). TTY: a person's terminal, paths unredacted.
  TTY=1 on alice work/website "lore render"
  on alice work/website "cat ~/.lore/render/*/index.md"
  on alice work/website "git status --short"

  note "leak blocked"
  on alice work/website "mkdir -p src && echo '// they approve estimates faster when we send them on WhatsApp' > src/notes.ts && git add src/notes.ts"
  on! alice work/website "git commit -m 'add notes'"
  on alice work/website "git reset -q src/notes.ts && rm -rf src"
  on alice work/website "printf 'Fixes checkout.\n\nThey approve estimates faster when we send them on WhatsApp first.\n' > pr.md"
  on! alice work/website "lore check pr-body pr.md"
  on alice work/website "printf 'Fixes checkout totals for INR.\n' | lore check pr-body -"
  on alice work/website "rm pr.md && lore check leaks --ci"

  note "agent rule refused"
  on alice work/website "lore agent start --label agent-backend --owner @alice"
  local actor session
  actor=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2); session=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  EXTRA_ENV="LORE_ACTOR=$actor LORE_SESSION=$session" on! alice work/website "lore rule add --body 'require prepayment from this client'"

  note "move"
  on alice work/website "lore memory add --body 'checkout must support INR and USD'"
  local m2; m2=$(id mem)
  # 0.1.18: lore share is removed (a row has one home); lore move gives it to the client repo, same id.
  TTY=1 ANSWER=y on alice work/website "lore move $m2 --to repo"
  on alice work/website "git status --short"

  note "client view"
  on carol work/app "git pull -q && lore memory list"
  on carol work/app "lore rule list"
  on carol work/app "lore task list"
  on carol work/app "lore whoami"
  on carol work/app "git fetch -q origin '+refs/*:refs/remotes/all/*' && git grep -i -c whatsapp \$(git for-each-ref --format='%(refname)' refs/remotes/all) -- . || echo 'no match in any ref'"
}

# ---------------------------------------------------------------------------
# group: three client 2 repos act as one product through one internal project.
s_group() {
  section group
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes
  make_store alice agency git@github.com:agency/notes
  for r in api app infra; do client_repo "client2/$r" "$r" client2dev; quiet alice work "git clone -q https://github.com/client2/$r"; done

  note "link three repos"
  on alice work/api "lore org link agency --project client2 --yes"
  on alice work/app "lore org link agency --project client2 --yes"
  on alice work/infra "lore org link agency --project client2 --yes"
  on alice work/api "lore actor register"

  note "mission across repos"
  on alice work/api "lore mission add 'Invite landing page' --to agency/client2 --key invite"
  local mis; mis=$(id msn)
  on alice work/api "lore task add 'send the invite email from the API' --to agency/client2 --mission $mis --repo api --key invite/backend"
  local tb; tb=$(id tsk)
  on alice work/api "lore task add 'build the invite page' --to agency/client2 --mission $mis --repo app --after $tb --key invite/frontend"
  on alice work/api "lore task add 'send the invite email from the API' --to agency/client2 --mission $mis --repo api --key invite/backend"
  on alice work/app "lore task list"
  on alice work/app "lore task list --all-repos"
  on alice work/infra "lore mission status $mis"
}

# ---------------------------------------------------------------------------
# staging: the same store, a second branch, chosen only on purpose.
s_staging() {
  section staging
  person alice alice@agency.example
  config alice <<'Y'
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: org}
    repos: [github.com/client/*]
  - name: agency-stag
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", branch: staging, org_folder: org}
Y
  remote agency/notes
  client_repo client/website app
  make_store alice agency git@github.com:agency/notes
  quiet alice work "git clone -q https://github.com/client/website website"
  quiet alice work/website "lore org link agency --project client --yes"

  note "staging branch"
  # 0.1.18: a store branch is made on purpose with ?branch= (never from a client repo, which must not name the store).
  on alice work/stores "lore store init agency_stag 'git@github.com:agency/notes?branch=staging' --yes"
  on alice work/website "lore org use agency-stag"
  on alice work/website "lore whoami"
  on alice work/website "lore org link agency-stag --project client --yes"
  on alice work/website "lore task add 'seed staging with demo data' --to agency-stag"
  on alice work/website "lore task list"
  on alice work/website "lore org use agency"
  on alice work/website "lore task list"
  on alice work/website "LORE_ORG=agency-stag lore task list"
  on alice work/website "git ls-remote https://github.com/agency/notes"
}

# ---------------------------------------------------------------------------
# contractor: one store per client; the contractor lists only the company store and the client's.
s_contractor() {
  section contractor
  person alice alice@agency.example; person bob bob@agency.example
  remote agency/notes; remote agency/notes-client; remote agency/notes-client2
  client_repo client/website app
  config alice <<'Y'
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: "."}
      - {url: "git@github.com:agency/notes-client"}
      - {url: "git@github.com:agency/notes-client2"}
    repos: [github.com/client/*, github.com/client2/*]
Y
  config bob <<'Y'
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:bob@agency.example", name: "Bob Roe", handle: bob}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: "."}
      - {url: "git@github.com:agency/notes-client"}
    repos: [github.com/client/website]
Y
  note "one store per client"
  quiet alice work "mkdir -p stores && cd stores && git init -q"
  on alice work/stores "lore store init agency git@github.com:agency/notes --yes && lore store init client git@github.com:agency/notes-client --yes && lore store init client2 git@github.com:agency/notes-client2 --yes"
  quiet alice work "git clone -q https://github.com/client/website website"
  on alice work/website "lore org link agency --project client --store git@github.com:agency/notes-client --yes"
  on alice work/website "lore actor register"
  on alice work/website "lore memory add --body 'the client staging database is restored every Sunday night'"
  on alice work/website "lore rule add --to agency/. --body 'every client estimate is reviewed by two people before it is sent'"

  note "contractor view"
  quiet bob work "git clone -q https://github.com/client/website website"
  TTY=1 on bob work/website "lore whoami"
  on bob work/website "lore actor register"
  on bob work/website "lore memory list"
  on bob . "lore store show"
  on bob . "ls ~/.lore/clones/github.com/agency"
}

# ---------------------------------------------------------------------------
# two-orgs: one person, two companies, one laptop.
s_two_orgs() {
  section two-orgs
  person alice alice@example.com
  config alice <<'Y'
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: org}
    own:   [github.com/agency/*]
    repos: [github.com/client/*]
  - name: client3
    identity: {key: "human:alice@client3.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:client3/notes", org_folder: org}
    own: [github.com/client3/*]
Y
  remote agency/notes; remote client3/notes
  client_repo client/website app
  client_repo client3/api api client3dev
  client_repo someone/tool tool somedev
  make_store alice agency git@github.com:agency/notes
  make_store alice client3 git@github.com:client3/notes
  quiet alice work "git clone -q https://github.com/client/website website && git clone -q https://github.com/client3/api client3-api && git clone -q https://github.com/someone/tool forked-tool"
  quiet alice work/website "lore org link agency --project client --yes"

  note "whoami per repo"
  on alice work/website "lore whoami"
  on alice work/client3-api "lore whoami"
  on alice work/forked-tool "lore whoami"
  on alice work/forked-tool "lore whoami --explain"

  note "personal notes split"
  on alice work/website "lore memory add --body 'ask the client PM before touching the payment flow' --to me"
  on alice work/client3-api "lore memory add --body 'client3 deploys only on Tuesdays' --to me"
  on alice work/website "lore memory list"
  on alice work/client3-api "lore memory list"
  on alice work/forked-tool "lore memory list"
}

# ---------------------------------------------------------------------------
# personal: private notes, local or synced through your own private repo.
s_personal() {
  section personal
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes; client_repo client/website app; remote alice/scratch
  make_store alice agency git@github.com:agency/notes
  quiet alice work "git clone -q https://github.com/client/website website"
  quiet alice work/website "lore org link agency --project client --yes"
  quiet alice work "git clone -q https://github.com/alice/scratch 2>/dev/null; cd scratch && git commit -q --allow-empty -m init && git push -q origin main && lore init --non-interactive --name=scratch >/dev/null 2>&1"

  note "local personal store"
  on alice work/website "lore memory add --body 'I always forget the staging VPN profile' --to me"
  on alice work/scratch "lore memory add --body 'I prefer tabs over spaces' --to me"
  on alice work/website "lore memory list"
  on alice work/scratch "lore memory list"
  on alice . "find ~/.lore/personal -name '*.json' -path '*memories*' | sed 's#.*/personal/##'"

  note "git personal store"
  remote alice/lore-me
  person alice-devbox alice@agency.example
  for who in alice alice-devbox; do
    agency_config "$who" "human:alice@agency.example" "Alice Doe" alice
    sed -i 's#^  store: local#  store: git@github.com:alice/lore-me#' "$HOMES/$who/.lore/config.yaml"
  done
  on alice work/scratch "lore store init me git@github.com:alice/lore-me --yes"
  on alice work/website "lore memory add --body 'my invoice template lives in the shared drive' --to me"
  quiet alice-devbox work "git clone -q https://github.com/client/website website"
  on alice-devbox work/website "lore memory list"
}

# ---------------------------------------------------------------------------
# agents: four agents and a person on one client 2 mission (the worked example).
# Agents share alice's machine (HOME) like tmux sessions on one server; each
# has its own clone and its own LORE_ACTOR / LORE_SESSION from `lore agent start`.
declare -A AGENT_ENV
# start_agent LABEL DIR: run `lore agent start` as alice in DIR and remember the agent's env.
start_agent() {
  on alice "$2" "lore agent start --label $1 --owner @alice"
  local a s
  a=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  s=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  AGENT_ENV[$1]="LORE_ACTOR=$a LORE_SESSION=$s"
}
# ag LABEL DIR CMD: run CMD as agent LABEL (alice's HOME, the agent's env).
ag()  { AS="$1" EXTRA_ENV="${AGENT_ENV[$1]}" on alice "$2" "$3"; }
ag!() { AS="$1" EXTRA_ENV="${AGENT_ENV[$1]}" on! alice "$2" "$3"; }
# agk BUG LABEL DIR CMD / agk! …: ag / ag! for an example that misbehaves today because of a reported bug (on_known).
agk()  { AS="$2" EXTRA_ENV="${AGENT_ENV[$2]}" on_known "$1" alice "$3" "$4"; }
agk!() { AS="$2" EXTRA_ENV="${AGENT_ENV[$2]}" on_known_fail "$1" alice "$3" "$4"; }

s_agents() {
  section agents
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes
  make_store alice agency git@github.com:agency/notes
  for r in api app infra; do
    client_repo "client2/$r" "$r" client2dev
    quiet alice work "git clone -q https://github.com/client2/$r && cd $r && lore org link agency --project client2 --yes"
  done
  quiet alice work/api "lore actor register"
  # 70 s per step: past lore's 60 s fetch cache, so each agent sees the others' last write.
  STEP=70

  note "plan"
  cat > "$HOMES/alice/work/api/invite.yaml" <<'Y'
mission: {key: invite, title: "Invite landing page", to: agency/client2}
approvers: ["@alice"]
tasks:
  - {key: backend,  title: "send the invite email from the API", repo: api,   timebox: 60m}
  - {key: frontend, title: "build the invite page",       repo: app,   after: [backend], timebox: 60m}
  - {key: proxy,    title: "allow /invite on the proxy", repo: infra}
  - {key: deploy,   title: "deploy to staging",          role: coordinator, after: [frontend, proxy], gate: required}
Y
  on alice work/api "cat invite.yaml"
  start_agent agent-coord work/api
  ag agent-coord work/api "lore mission apply invite.yaml"
  local mis; mis=$(id msn)
  ag agent-coord work/api "lore mission apply invite.yaml"
  ag agent-coord work/api "lore task claim $mis"

  note "workers start"
  for r in api app infra; do quiet alice work/agents "mkdir -p \$(echo $r) && cd $r && [ -d .git ] || git clone -q https://github.com/client2/$r ."; done
  start_agent agent-backend work/agents/api
  ag agent-backend work/agents/api "lore inbox"
  ag agent-backend work/agents/api "lore task next --mission $mis --repo api"
  local tb; tb=$(id tsk)
  start_agent agent-frontend work/agents/app
  ag! agent-frontend work/agents/app "lore task next --mission $mis --repo app"
  start_agent agent-infra work/agents/infra
  ag agent-infra work/agents/infra "lore task next --mission $mis --repo infra --owns nginx/sites/invite.conf"
  local tp; tp=$(id tsk)

  note "shared fact"
  # lore finding add is not in 0.1.18 (dropped): a fact every agent needs is a live memory.
  ag agent-infra work/agents/infra "lore memory add --live --body 'invite links must use the public website address, not the server name'"
  ag agent-backend work/agents/api "lore sync >/dev/null && lore search 'invite links'"
  ag agent-infra work/agents/infra "lore task done $tp"

  note "done then merged"
  ag agent-backend work/agents/api "lore task note $tb 'the API sends the invite email and returns a link; the email text is in invite.html'"
  ag agent-backend work/agents/api "git switch -q -c agent-backend/invite && mkdir -p api && echo 'package api' > api/invite.go && git add api && git commit -qm 'send the invite email' && git push -q -u origin agent-backend/invite"
  ag agent-backend work/agents/api "lore task done $tb --commit \$(git rev-parse HEAD) --lesson 'invite links stop working after 48 hours; the README says so'"
  ag! agent-frontend work/agents/app "lore task next --mission $mis --repo app"
  on alice work/api "git pull -q && git merge -q --no-ff --no-edit origin/agent-backend/invite && git push -q origin main"
  on alice work/api "lore task list"
  ag agent-frontend work/agents/app "lore task next --mission $mis --repo app"
  local tf; tf=$(id tsk)

  note "crash and reclaim"
  ag agent-frontend work/agents/app "git switch -q -c agent-frontend/invite-route && mkdir -p src/routes && echo 'export {}' > src/routes/invite.tsx && git add src && git commit -qm 'invite page'"
  ag agent-frontend work/agents/app "lore task note $tf 'route + loader done, form next'"
  CLOCK=$(date -u -d "$CLOCK + 25 minutes" +%Y-%m-%dT%H:%M:%SZ)
  ag agent-coord work/api "lore inbox"
  ag agent-coord work/api "lore mission status $mis"
  quiet alice work/agents "mkdir -p fe2 && cd fe2 && git clone -q https://github.com/client2/app"
  start_agent agent-frontend-2 work/agents/fe2/app
  ag agent-frontend-2 work/agents/fe2/app "lore task next --mission $mis --repo app"
  ag agent-frontend-2 work/agents/fe2/app "git fetch -q origin agent-frontend/invite-route && git log --oneline -1 FETCH_HEAD"
  ag! agent-frontend work/agents/app "lore task done $tf"
  ag agent-frontend-2 work/agents/fe2/app "git switch -q -c agent-frontend-2/invite-route FETCH_HEAD && echo form >> src/routes/invite.tsx && git commit -qam 'invite form' && git push -q -u origin agent-frontend-2/invite-route && lore task done $tf --commit \$(git rev-parse HEAD)"
  on alice work/app "git pull -q && git merge -q --no-ff --no-edit origin/agent-frontend-2/invite-route && git push -q origin main && lore task list >/dev/null"

  note "merged by hand"
  ag! agent-coord work/api "lore task next --mission $mis --role coordinator"
  on alice work/infra "lore task merged $tp"

  note "gate"
  ag! agent-coord work/api "lore task next --mission $mis --role coordinator"
  local tg; tg=$(grep -o 'tsk_[0-9a-f]\{32\}' <<<"$LAST_OUT" | head -1 || true)
  ag! agent-coord work/api "lore gate approve $tg"
  on alice work/api "lore gate approve $tg"
  ag agent-coord work/api "lore task next --mission $mis --role coordinator"

  note "mission status"
  on alice work/infra "lore mission status $mis"
  on alice work/infra "lore task show $tg --from agency/client2"
  on alice work/infra "lore task show $tf"
}

# ---------------------------------------------------------------------------
# leak-cleanup: an internal note was written to the client's live ref by mistake.
s_leak_cleanup() {
  section leak-cleanup
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes; client_repo client/website app
  make_store alice agency git@github.com:agency/notes
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes"

  note "the mistake"
  on alice work/website "lore task add 'chase the client about the unpaid March invoice' --to repo"
  local t; t=$(id tsk)
  on carol work/app "lore sync >/dev/null; lore task list"

  note "purge"
  on alice work/website "lore live purge $t --confirm"
  on alice work/website "lore live compact --dry-run"
  on alice work/website "lore live compact"
  on alice work/website "lore live log"
  on carol work/app "lore sync >/dev/null; lore task list"
  person newdev newdev@client.example
  on newdev work "git clone -q --mirror https://github.com/client/website app.git && cd app.git && git for-each-ref --format='%(refname)' && for c in \$(git rev-list --all); do git grep -q -i 'unpaid March' \$c && echo \"found in \$c\"; done; echo 'searched every commit the remote serves'"
}

# ---------------------------------------------------------------------------
# errors: the error messages people meet, each produced for real.
s_errors() {
  section errors
  person alice alice@agency.example; person bob bob@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  agency_config bob "human:bob@agency.example" "Bob Roe" bob
  remote agency/notes; client_repo client/website app
  make_store alice agency git@github.com:agency/notes
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes && lore actor register"

  note "config invalid"
  cp "$HOMES/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  sed -i 's#org_folder: org#org_folder: org\n        branch: release/2026#' "$HOMES/alice/.lore/config.yaml"
  on! alice . "lore config check"
  on! alice work/website "lore whoami"
  cp "$WORLD/alice-config.bak" "$HOMES/alice/.lore/config.yaml"

  note "not a store"
  client_repo client2/docs docs client2dev
  quiet client2dev work/docs "mkdir -p src && echo 'export {}' > src/index.ts && echo '{}' > package.json && git add . && git commit -qm code && git push -q origin main"
  cp "$HOMES/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  sed -i 's#^    own:#      - url: git@github.com:client2/docs\n    own:#' "$HOMES/alice/.lore/config.yaml"
  on! alice work/website "lore whoami"
  on! alice . "lore store doctor"
  cp "$WORLD/alice-config.bak" "$HOMES/alice/.lore/config.yaml"

  note "org ambiguous"
  cp "$HOMES/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  cat >> "$HOMES/alice/.lore/config.yaml" <<'Y'
  - name: client3
    identity: {key: "human:alice@client3.example", name: "Alice Doe"}
    repos: [github.com/client/*]
Y
  client_repo client/blog site
  quiet alice work "git clone -q https://github.com/client/blog blog"
  on! alice work/blog "lore whoami"
  on! alice work/blog "lore memory add --body 'the site deploys from the release branch'"
  cp "$WORLD/alice-config.bak" "$HOMES/alice/.lore/config.yaml"

  note "link unknown"
  mv "$HOSTS/agency/notes.git" "$WORLD/lore.git.away"
  quiet bob work "git clone -q https://github.com/client/website website"
  on! bob work/website "lore memory add --body 'the client wants weekly demos'"

  note "offline claim"
  mv "$WORLD/lore.git.away" "$HOSTS/agency/notes.git"
  quiet alice work/website "lore task add 'rotate the API keys' --to agency"
  local t; t=$(grep -o 'tsk_[0-9a-f]\{32\}' "$WORLD/quiet.log" | head -1 || true)
  mv "$HOSTS/agency/notes.git" "$WORLD/lore.git.away"
  on! alice work/website "lore task claim $t"
  on alice work/website "lore memory add --body 'the API keys rotate every 90 days' --to agency || true"
  mv "$WORLD/lore.git.away" "$HOSTS/agency/notes.git"
  on alice work/website "lore sync"

  note "store branch missing"
  cat >> "$HOMES/alice/.lore/config.yaml" <<'Y'
  - name: agency-stag
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", branch: staging, org_folder: org}
Y
  on! alice work/website "LORE_ORG=agency-stag lore org link agency-stag --project client --yes"

  note "refs refused by the host"
  remote widgets/portal
  cat > "$HOSTS/widgets/portal.git/hooks/pre-receive" <<'HOOK'
#!/bin/sh
while read old new ref; do
  case "$ref" in refs/lore/*) echo "remote: refusing $ref: only branches and tags may be pushed here" >&2; exit 1;; esac
done
HOOK
  chmod +x "$HOSTS/widgets/portal.git/hooks/pre-receive"
  quiet bob work "git clone -q https://github.com/widgets/portal 2>/dev/null; cd portal && git commit -q --allow-empty -m init && git push -q origin main && lore init --non-interactive --name=portal >/dev/null 2>&1"
  on bob work/portal "lore task add 'set up the portal CI'"
  on bob work/portal "lore live diff"
  on bob work/portal "lore doctor 2>&1 | grep -i -E 'live|refs/lore|push' || true"
}

# ---------------------------------------------------------------------------
# internals: what lore actually writes in git, for the concepts and how-it-works pages.
s_internals() {
  section internals
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes; client_repo client/website app
  make_store alice agency git@github.com:agency/notes
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes && lore actor register"

  note "refspec"
  on alice work/website "git config --get-all remote.origin.fetch"

  note "monorepo live tree"
  on carol work/app "mkdir -p web && cd web && lore init --non-interactive --name=web >/dev/null 2>&1; cd .. && lore task add 'fix header' --to repo && cd web && lore rule add --body 'Buttons are blue' --to repo"
  on carol work/app "git ls-tree -r --name-only refs/lore/live"
  on carol work/app "git log --oneline refs/lore/live"
  on carol work/app "git cat-file -p refs/lore/live:_live/meta.json; echo; git cat-file -p refs/lore/live:_live/epoch"
  on carol work/app "git cat-file -p refs/lore/live:\$(git ls-tree -r --name-only refs/lore/live | grep '_root/tasks/')"
  on carol work/app "git branch -a"

  note "store layout"
  on alice work/website "lore task add 'renew the client SSL certificate' --to agency"
  local t; t=$(id tsk)
  on alice work/website "lore memory add --body 'the client wants a demo every second Friday'"
  on alice . "git -C ~/.lore/clones/github.com/agency/notes.git ls-tree -r --name-only main | grep -v '_meta.json\|.gitattributes' | head -20"
  on alice . "git -C ~/.lore/clones/github.com/agency/notes.git for-each-ref --format='%(refname)'"
  on alice . "git -C ~/.lore/clones/github.com/agency/notes.git cat-file -p main:client/.lore/data/_meta.json"

  note "signals"
  on alice work/website "lore agent start --label agent-backend --owner @alice"
  local a s2
  a=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2); s2=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  EXTRA_ENV="LORE_ACTOR=$a LORE_SESSION=$s2" AS=agent-backend on alice work/website "lore task claim $t"
  on alice . "cd ~/.lore/clones-v017/github.com/agency/notes.git && git fetch -q origin && git ls-tree -r --name-only refs/lore-remote/origin/signals/main && git log --oneline refs/lore-remote/origin/signals/main"
  on alice . "cd ~/.lore/clones-v017/github.com/agency/notes.git && git cat-file -p refs/lore-remote/origin/signals/main:\$(git ls-tree -r --name-only refs/lore-remote/origin/signals/main | head -1)"
  EXTRA_ENV="LORE_ACTOR=$a LORE_SESSION=$s2" AS=agent-backend on alice work/website "lore task heartbeat $t"
  on alice . "cd ~/.lore/clones-v017/github.com/agency/notes.git && git fetch -q origin && git log --oneline refs/lore-remote/origin/signals/main"

  note "hooks"
  on alice work/website "ls .git/hooks | grep -v sample; grep -h 'lore' .git/hooks/pre-commit | head -5"
  on alice work/website "cat .git/info/exclude | tail -2"
}

# ---------------------------------------------------------------------------
# team-ops: assigning by @handle, and the claim commands a person uses on an agent's work
# (release, revoke, reassign) plus shared resources (docs/team/agents.md).
s_team_ops() {
  section team-ops
  person alice alice@agency.example; person bob bob@agency.example
  # 70 s per step: past lore's 60 s fetch cache, so each person sees the other's last write.
  STEP=70
  # First a repo with no org config (like the small-team page): the roster and shared resources.
  remote agency/billing
  quiet alice work "git clone -q https://github.com/agency/billing && cd billing && echo '# billing' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=billing >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main && lore actor register"
  quiet bob work "git clone -q https://github.com/agency/billing && cd billing && lore actor register"

  note "teams"
  on alice work/billing "lore actor list"
  on alice work/billing "lore task add 'make the buttons blue' --assign @bob"
  on bob work/billing "lore task list"

  note "resource"
  on alice work/billing "lore hold claim full-suite --ttl 30m"
  on! bob work/billing "lore hold claim full-suite --ttl 30m"
  on alice work/billing "lore hold release full-suite"
  on bob work/billing "lore hold claim full-suite --ttl 30m"

  # Then the agency's client repo, with the org config: claims a person takes back or moves.
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  agency_config bob "human:bob@agency.example" "Bob Roe" bob
  remote agency/notes; client_repo client/website app
  make_store alice agency git@github.com:agency/notes
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes && lore actor register"
  quiet bob work "git clone -q https://github.com/client/website website && cd website && lore actor register"

  note "release"
  on alice work/website "lore task add 'send the invoice reminder before Friday' --to agency"
  local t1; t1=$(id tsk)
  on bob work/website "lore task claim $t1"
  on bob work/website "lore task release $t1"
  on alice work/website "lore task show $t1"

  note "revoke"
  start_agent agent-backend work/website
  AS=agent-backend EXTRA_ENV="${AGENT_ENV[agent-backend]}" on alice work/website "lore task claim $t1"
  on alice work/website "lore task revoke $t1"
  AS=agent-backend EXTRA_ENV="${AGENT_ENV[agent-backend]}" on! alice work/website "lore task done $t1"

  note "reassign"
  on alice work/website "lore task add 'update the contact page' --to agency --assign @bob"
  local t2; t2=$(id tsk)
  on bob work/website "lore task claim $t2"
  on alice work/website "lore task reassign $t2 --assign @alice"
  on! bob work/website "lore task done $t2"
}

# ---------------------------------------------------------------------------
# people: your identity, joining an org from its store, and store housekeeping
# (docs/team/identity-and-stores.md).
s_people() {
  section people
  person alice alice@agency.example; person bob bob@agency.example
  remote agency/notes; client_repo client/website app

  note "identity"
  quiet alice work "git clone -q https://github.com/client/website website"
  on alice work/website "lore identity show"
  on alice . "lore identity set alice@example.com"
  on alice work/website "lore identity show"
  on alice . "lore identity unset"
  on alice work/website "lore identity show"

  note "identity per org"
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  make_store alice agency git@github.com:agency/notes
  on alice . "lore identity set --org agency --key human:alice@agency.example --name 'Alice Doe' --handle alice"
  quiet alice work/website "lore org link agency --project client --yes && lore actor register"
  on alice work/website "lore identity show"

  note "suggest an org entry"
  quiet alice . "git clone -q https://github.com/agency/notes notes-checkout"
  cat > "$HOMES/alice/notes-checkout/org/.lore/lore-org.yaml" <<'Y'
name: agency
stores:
  - {url: "git@github.com:agency/notes", org_folder: org}
own: [github.com/agency/*]
repos: [github.com/client/*]
Y
  on alice notes-checkout "git add org/.lore/lore-org.yaml && git commit -qm 'suggest the agency org entry' && git push -q origin main"

  note "org import"
  # Run outside any repo: the register step opens only the org store (BUG-T3).
  on bob . "lore org import git@github.com:agency/notes --key human:bob@agency.example --name 'Bob Roe' --handle bob --yes"
  on bob . "cat ~/.lore/config.yaml"
  quiet bob work "git clone -q https://github.com/client/website website"
  on bob work/website "lore whoami"

  note "org set"
  remote agency/tools
  quiet alice work "git clone -q https://github.com/agency/tools && cd tools && echo '# tools' > README.md && git add . && git commit -qm init && git push -q origin main && lore init --non-interactive --name=tools >/dev/null 2>&1"
  on alice work/tools "lore org set agency --commit"
  on alice work/tools "cat .lore/org.yaml"
  on! alice work/website "lore org set agency --commit"

  note "move"
  on alice work/website "lore memory add --body 'the client replies faster on WhatsApp than email' --to repo"
  local m; m=$(id mem)
  on alice work/website "lore move $m --to agency --yes"
  on alice work/website "lore memory list"

  note "store open and forget"
  on alice . "lore store open git@github.com:agency/notes"
  on alice . "ls ~/.lore/worktrees/github.com/agency/notes/main"
  on bob . "lore store forget git@github.com:agency/notes"

  note "cache prune"
  on alice . "lore cache prune --dry-run"
}

# ---------------------------------------------------------------------------
# store-push: how writes to the personal store and an org store reach the remote
# (push modes, batches, history compaction; docs/team/identity-and-stores.md).
s_store_push() {
  section store-push
  person alice alice@agency.example; person bob bob@agency.example
  remote alice/lore-me; remote agency/notes; client_repo client/website app
  for who in alice bob; do
    config "$who" <<Y
orgs:
  - name: agency
    identity: {key: "human:$who@agency.example", name: "${who^}", handle: $who}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:   [github.com/agency/*]
    repos: [github.com/client/*]
    defaults:
      client_repos: {memory: agency}
Y
  done
  # Alice's machine holds an admin setup of the agency: she decides the org store's settings.
  cat >> "$HOMES/alice/.lore/config.yaml" <<'Y'
setups:
  - name: agency
    kind: admin
    org: agency
Y
  # The org store gets its first rows in August, so a compaction has old history to fold.
  CLOCK=2026-08-03T09:00:00Z
  make_store alice agency git@github.com:agency/notes
  quiet alice work "git clone -q https://github.com/client/website website"
  quiet alice work/website "lore org link agency --project client --yes && lore actor register"
  quiet alice work/website "lore memory add --body 'the client signs off designs on Thursdays' --to agency"
  quiet alice work/website "lore memory add --body 'staging resets every Sunday night' --to agency"
  CLOCK=2026-11-02T10:00:00Z
  quiet alice . "lore store init me git@github.com:alice/lore-me"
  quiet bob work "git clone -q https://github.com/client/website website"
  quiet bob work/website "lore actor register && lore sync"

  note "show"
  on alice work/website "lore store show me"

  note "batch"
  on alice work/website "lore store set me push=batch push_every=10m"
  on alice work/website "lore memory add --body 'I always forget the staging VPN profile' --to me"
  on alice work/website "lore memory add --body 'I prefer short standups' --to me"
  on alice work/website "lore store show me"
  on alice work/website "lore sync"
  on alice work/website "git fetch -q https://github.com/alice/lore-me main && git log -1 --format=%B FETCH_HEAD"

  note "manual"
  on alice work/website "lore store set me push=manual"
  on alice work/website "lore doctor | grep 'store me:'"

  note "refused"
  on! alice work/website "lore store set repo push=batch"
  on! bob work/website "lore store set agency push=batch"
  on! alice work/website "lore store set me push_every=30s"

  note "org settings"
  on alice work/website "lore store set agency push=batch push_every=15m"
  on bob work/website "lore sync >/dev/null && lore store show agency"

  note "compact"
  # Bob's write waits for his next batch: his clone holds work the remote has not seen.
  on bob work/website "lore memory add --body 'the client wants the invoice on the 1st' --to agency"
  on alice work/website "lore store compact agency --before 30d --dry-run"
  on! alice work/website "lore store compact agency --before 30d"
  on alice work/website "lore store compact agency --before 30d --yes"
  on bob work/website "lore sync"
  on bob work/website "lore memory list"
  on bob work/website "git fetch -q https://github.com/agency/notes main && git log --format=%s FETCH_HEAD"

  note "auto compact"
  on alice work/website "lore store set me compact=on compact_after=90d"
}

# ---------------------------------------------------------------------------
# live-repair: the live ref deleted on the server and put back with `lore live
# restore`, and --trace showing the git commands lore runs (docs/team/commands.md,
# docs/team/troubleshooting.md).
s_live_repair() {
  section live-repair
  remote agency/billing
  person alice alice@agency.example
  quiet alice work "git clone -q https://github.com/agency/billing && cd billing && echo '# billing' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=billing >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"

  note "live ref deleted"
  on alice work/billing "lore task add 'send the March invoices'"
  on alice work/billing "lore task add 'chase the unpaid February invoice'"
  on alice work/billing "git push -q origin :refs/lore/live && git ls-remote origin"
  on! alice work/billing "lore task add 'close the books for Q1'"
  on alice work/billing "lore doctor | grep 'remote live ref'"

  note "live restore"
  on alice work/billing "lore live restore"
  on alice work/billing "git ls-remote origin refs/lore/live"
  on alice work/billing "lore task add 'close the books for Q1'"
  on alice work/billing "lore task list"

  note "trace"
  on alice work/billing "lore sync --trace 2>&1 | grep -e '^trace: git fetch' -e '^trace: git push' -e '^·'"
}

# ---------------------------------------------------------------------------
# profiles: team profiles on the org roster: fields, contacts, contact types,
# statuses, company periods, project memberships and roles, offboarding and
# rejoining, org create/rename and machine profiles (docs/team/profiles.md).
s_profiles() {
  section profiles
  person tomas tomas@agency.example; person mira mira@agency.example
  agency_config tomas "human:tomas@agency.example" "Tomas Weber" tomas
  agency_config mira "human:mira@agency.example" "Mira Patel" mira
  remote agency/notes; client_repo client/acme-hub acme-hub; client_repo client/northwind-app northwind-app
  make_store tomas agency git@github.com:agency/notes
  quiet tomas work "git clone -q https://github.com/client/acme-hub && cd acme-hub && lore org link agency --project acme-hub --yes && lore actor register"
  quiet tomas work "git clone -q https://github.com/client/northwind-app && cd northwind-app && lore org link agency --project northwind-app --yes"
  quiet mira work "git clone -q https://github.com/client/acme-hub && cd acme-hub && lore actor register"
  # Mira joined on 2026-01-12: the story starts then.
  CLOCK=2026-01-12T09:00:00Z

  note "profile fields"
  on tomas work/acme-hub "lore actor edit @mira --title 'Backend engineer' --timezone Europe/Istanbul --location 'Ankara, TR'"
  on tomas work/acme-hub "lore actor edit @mira --bio 'Payments and billing.' --photo-url https://example.com/mira.png"
  on tomas work/acme-hub "lore actor edit @mira --clear photo_url,bio"
  on! tomas work/acme-hub "lore actor edit @mira --timezone 'Istanbul time'"

  note "contacts"
  on tomas work/acme-hub "lore actor contact add @mira email mira@agency.example --label work --primary --identity"
  on tomas work/acme-hub "lore actor contact add @mira email mira.patel@example.com --label personal --identity"
  on tomas work/acme-hub "lore actor contact add @mira github mira-agency --label work --primary --identity"
  on tomas work/acme-hub "lore actor contact add @mira github mirap --label personal --identity"
  on tomas work/acme-hub "lore actor contact add @mira phone '+90 532 000 0000' --label work --primary"
  on tomas work/acme-hub "lore actor contact add @mira github mirap --label personal --identity"
  on tomas work/acme-hub "lore actor contact list --type phone"
  on! tomas work/acme-hub "lore actor contact add @mira phone 12 --label work"
  on! tomas work/acme-hub "lore actor contact add @tomas github mirap --identity"
  on tomas work/acme-hub "lore actor show github:mirap"

  note "contact edit and remove"
  on tomas work/acme-hub "lore actor contact add @mira linkedin mira-patel --label work"
  local li; li=$(id acn)
  on tomas work/acme-hub "lore actor contact edit $li --note 'old profile, not updated'"
  on tomas work/acme-hub "lore actor contact remove $li --reason 'account closed'"
  on tomas work/acme-hub "lore actor contact list @mira --identity"

  note "contact types"
  on tomas work/acme-hub "lore contact-type add bitbucket --kind handle --identity --url-template 'https://bitbucket.org/{value}'"
  on tomas work/acme-hub "lore contact-type edit github --label GitHub"
  on tomas work/acme-hub "lore contact-type list"
  on! tomas work/acme-hub "lore contact-type add email --kind email"

  note "periods"
  on tomas work/acme-hub "lore actor period add @mira --type employee --started-on 2026-01-12 --org-role 'Senior engineer'"
  on tomas work/acme-hub "lore actor period list @mira"
  on! tomas work/acme-hub "lore actor period add @mira --type contractor --started-on 2026-02-01"

  note "roles"
  on tomas work/acme-hub "lore role add tech_writer --label 'Technical writer'"
  on tomas work/acme-hub "lore role list"
  on! tomas work/acme-hub "lore project member add acme-hub @mira --role wizard"
  on tomas work/acme-hub "lore role edit tech_writer --description 'writes the user docs'"
  on tomas work/acme-hub "lore role edit qa --retire"

  note "members"
  on tomas work/acme-hub "lore project member add acme-hub @mira --role dev,reviewer"
  CLOCK=2026-03-01T09:00:00Z
  on tomas work/northwind-app "lore project member add northwind-app @mira --role lead"
  on tomas work/acme-hub "lore project member add acme-hub @tomas --role lead"
  on tomas work/acme-hub "lore project member list acme-hub"
  on tomas work/acme-hub "lore actor show @mira"

  note "member edit and remove"
  on tomas work/acme-hub "lore project member edit acme-hub @tomas --role lead,pm"
  on tomas work/northwind-app "lore project member add northwind-app @tomas --role reviewer"
  on tomas work/northwind-app "lore project member remove northwind-app @tomas --reason 'Mira reviews her own project now'"
  on! tomas work/northwind-app "lore project member remove northwind-app @tomas"
  on tomas work/northwind-app "lore project member list northwind-app --history"

  note "status"
  CLOCK=2026-05-20T09:00:00Z
  on tomas work/acme-hub "lore actor status @mira on_leave --from 2026-06-01 --until 2026-06-30"
  CLOCK=2026-07-03T09:00:00Z
  on tomas work/acme-hub "lore actor list"
  on tomas work/acme-hub "lore actor status @mira active"
  on! tomas work/acme-hub "lore actor status @mira left"

  note "offboard"
  CLOCK=2026-09-30T17:00:00Z
  on tomas work/acme-hub "lore task add 'move the invoice export to the new API' --to agency --assign @mira"
  on! tomas work/acme-hub "lore actor offboard @mira --reason 'moved abroad'"
  on tomas work/acme-hub "lore actor offboard @mira --reason 'moved abroad' --yes"
  on tomas work/acme-hub "lore actor list"
  on! tomas work/acme-hub "lore task add 'review the refund flow' --to agency --assign @mira"
  CLOCK=2026-10-15T09:00:00Z
  on tomas work/acme-hub "lore actor status @mira archived"
  on tomas work/acme-hub "lore actor list --archived"

  note "rejoin"
  CLOCK=2026-11-02T09:00:00Z
  on tomas work/acme-hub "lore actor rejoin @mira --type contractor --org-role 'Contract engineer' --yes"
  on tomas work/acme-hub "lore project member add acme-hub @mira --role reviewer"
  on tomas work/acme-hub "lore project member list acme-hub --history"
  on tomas work/acme-hub "lore actor period list @mira"

  note "whole year"
  on tomas work/acme-hub "lore actor show @mira"
  on tomas work/acme-hub "lore project member list --actor @mira --history"

  note "org create and rename"
  remote acme/handbook; remote acme/lore-notes
  person ana ana@acme.example
  quiet ana work "git clone -q https://github.com/acme/handbook && cd handbook && echo '# handbook' > README.md && git add . && git commit -qm init && git push -q origin main"
  on ana work/handbook "lore store init acme git@github.com:acme/lore-notes --yes"
  on ana work/handbook "lore org create acme --store acme --own 'github.com/acme/*' --key human:ana@acme.example --name 'Ana Lima' --handle ana --yes"
  on ana work/handbook "lore org rename acme acme-studio"
  on ana work/handbook "lore whoami"

  note "machines"
  on tomas work/acme-hub "lore machine label 'office desktop'"
  # Only the columns that do not describe the build machine's hardware (and never its OS user).
  on tomas work/acme-hub "lore machine list | cut -c1-38"
  local m; m=$(grep -o 'mch_[0-9a-f]\{8\}' <<<"$LAST_OUT" | head -1 || true)
  on tomas work/acme-hub "lore machine show ${m:-mch_missing} | grep -e '^mch_' -e 'label:' -e 'host:' -e 'runtime:' -e 'last_write:'"
  on tomas work/acme-hub "lore machine label ''"
}

# ---------------------------------------------------------------------------
# setup-cli: several setups on one machine, managed with the lore setup
# subcommands instead of the page (docs/team/setup-cli.md). No command here
# probes ssh or GitHub: aliases and discover would, so they are not run.
s_setup_cli() {
  section setup-cli
  person tomas tomas@agency.example
  agency_config tomas "human:tomas@agency.example" "Tomas Weber" tomas
  remote agency/notes; remote agency/site; remote tomas/dotfiles
  make_store tomas agency git@github.com:agency/notes
  quiet tomas work "mkdir -p agency personal && git clone -q https://github.com/agency/site agency/site && cd agency/site && echo '# site' > README.md && git add . && git commit -qm init && git push -q origin main && lore init --non-interactive --name=site >/dev/null 2>&1"
  quiet tomas work "git clone -q https://github.com/tomas/dotfiles personal/dotfiles && cd personal/dotfiles && echo '# dotfiles' > README.md && git add . && git commit -qm init && git push -q origin main && lore init --non-interactive --name=dotfiles >/dev/null 2>&1"
  cat >> "$HOMES/tomas/.lore/config.yaml" <<'Y'
setups:
  - name: agency
    kind: admin
    org: agency
    default: true
    folders:
      - ~/work/agency/**
  - name: personal
    kind: solo
    folders:
      - ~/work/personal/**
    identity:
      key: human:tomas@home.example
      name: Tomas Weber
    personal_store: none
Y

  note "list"
  on tomas work/agency/site "lore setup list"
  on tomas work/personal/dotfiles "lore setup list"
  on tomas work/personal/dotfiles "lore whoami --explain | sed -n '/setup resolution/,\$p'"

  note "show"
  on tomas . "lore setup show personal"

  note "choose"
  on tomas work/agency/site "lore --setup personal whoami | head -2"
  on tomas work/agency/site "lore setup use personal"
  on tomas work/agency/site "lore setup use personal --here"
  on tomas work/agency/site "lore setup list | tail -2"
  on tomas work/agency/site "lore setup use --here --clear"
  on! tomas work/agency/site "lore --setup nope task add 'check the sitemap'"

  note "edit"
  on tomas . "lore setup edit personal --folder '~/work/personal/**' --folder '~/src/**' --note 'side projects and dotfiles'"
  on tomas . "lore setup show personal"

  note "disable and enable"
  on tomas . "lore setup disable personal"
  on! tomas work/personal/dotfiles "lore --setup personal task add 'rotate the backup key'"
  on tomas . "lore setup enable personal"

  note "remove and restore"
  on tomas work/personal/dotfiles "lore setup use personal --here"
  on tomas . "lore setup remove personal"
  on tomas . "lore setup trash list"
  on tomas . "lore setup trash restore personal"
  on tomas . "lore setup remove personal"
  on tomas . "lore setup trash purge personal --dry-run"
  on! tomas . "lore setup trash purge personal"
  on tomas . "lore setup trash purge personal --confirm"
  on tomas . "lore setup trash list"

  note "steps"
  # The plan's HELP column is long: only the step ids and titles are shown.
  on tomas . "lore setup plan --kind solo | cut -c1-52"
  # Only the step lines: the checks table under them carries how long each check took.
  on tomas . "lore setup apply side --step kind --answers '{\"kind\":\"solo\",\"name\":\"side\"}' | head -4"
  on tomas . "lore setup check side --step identity --answers '{\"name\":\"Tomas Weber\",\"email\":\"tomas@home.example\"}' | head -2"
  on! tomas . "lore setup apply side --step github --answers '{\"user\":\"tomas-dev\"}'"

  note "import"
  on tomas . "lore setup import-scan --folder ~/work"
  on tomas . "lore setup import --setups '[{\"name\":\"personal\",\"kind\":\"solo\"}]'"

  note "join"
  on! tomas . "lore setup join lore-invite:v1:garbage"
}

# ---------------------------------------------------------------------------
# invites: lore team invite and its list/resend/revoke, from an admin setup
# (docs/team/invites.md). No --github or --grant: those ask GitHub through gh.
s_invites() {
  section invites
  person tomas tomas@agency.example
  agency_config tomas "human:tomas@agency.example" "Tomas Weber" tomas
  remote agency/notes; client_repo client/acme-hub acme-hub
  make_store tomas agency git@github.com:agency/notes
  quiet tomas work "git clone -q https://github.com/client/acme-hub && cd acme-hub && lore org link agency --project acme-hub --yes && lore actor register"
  cat >> "$HOMES/tomas/.lore/config.yaml" <<'Y'
setups:
  - name: agency
    kind: admin
    org: agency
    default: true
    folders:
      - ~/work/**
  - name: personal
    kind: solo
    folders:
      - ~/personal/**
    identity:
      key: human:tomas@home.example
      name: Tomas Weber
Y

  note "invite"
  on tomas work/acme-hub "lore team invite mira@agency.example --role dev,reviewer --project acme-hub"
  on tomas work/acme-hub "lore team invite ana@agency.example --type contractor --expires 30d"

  note "invite refusals"
  on! tomas work/acme-hub "lore team invite mira@agency.example"
  on! tomas work/acme-hub "lore team invite bo@agency.example --role dev"
  on! tomas work/acme-hub "lore --setup personal team invite bo@agency.example"

  note "list"
  on tomas work/acme-hub "lore team invite list"
  on tomas work/acme-hub "lore team invite list --status pending --json"

  note "resend and revoke"
  on tomas work/acme-hub "lore team invite resend mira@agency.example"
  on! tomas work/acme-hub "lore team invite revoke ana@agency.example"
  on tomas work/acme-hub "lore team invite revoke ana@agency.example --yes"
  on! tomas work/acme-hub "lore team invite resend ana@agency.example"
  on tomas work/acme-hub "lore team invite list"
}

# ---------------------------------------------------------------------------
# client-folders: an admin keeps the clients' repos in one plain parent folder
# (not a git repo) and makes, lists and attaches the team store's client
# folders from there (docs/team/client-folders.md).
s_client_folders() {
  section client-folders
  person tomas tomas@agency.example
  agency_config tomas "human:tomas@agency.example" "Tomas Weber" tomas
  remote agency/notes
  make_store tomas agency git@github.com:agency/notes
  cat >> "$HOMES/tomas/.lore/config.yaml" <<'Y'
setups:
  - name: agency
    kind: admin
    org: agency
    default: true
    folders:
      - ~/work/clients/**
Y
  quiet tomas work/clients "true"

  note "from the parent folder"
  on tomas work/clients "lore store list"
  on tomas work/clients "lore store folder add team client_north"
  on tomas work/clients "lore store folder add team client_south"
  on tomas work/clients "lore store folder add git@github.com:agency/notes client_east"

  note "list the folders"
  on tomas work/clients "lore store folder list team"

  note "names that need a repo"
  on! tomas work/clients "lore store folder add golang tips"
  on! tomas work/clients "lore store add golang git@github.com:agency/notes"

  note "a client repo attaches one"
  client_repo client/north-web north-web tomas
  quiet tomas work "mv north-web clients/north-web"
  on tomas work/clients/north-web "lore org link agency --project client_north --yes"
  on tomas work/clients/north-web "lore store folder list team"

  note "a client repo with no lore files"
  remote client/south-app
  quiet tomas work/clients "git clone -q https://github.com/client/south-app south-app && cd south-app && echo '# south-app' > README.md && git add . && git commit -qm init && git push -q origin main"
  on tomas work/clients/south-app "lore org link agency --project client_south --yes"
  on tomas work/clients/south-app "lore task add 'set up the staging deploy'"
  on tomas work/clients/south-app "lore task list"
  on tomas work/clients/south-app "git status --short --ignored && ls -A"

  note "remove a folder made by mistake"
  on tomas work/clients "lore store folder add team client_old"
  on tomas work/clients "lore store folder remove team client_old --yes"
  on! tomas work/clients "lore store folder remove team client_north --yes"
}

# fake_ssh LOGIN: an ssh on PATH that answers GitHub's greeting as LOGIN for every
# host (ssh -T), and a gh with no login; nothing else is faked, nothing reaches the network.
fake_ssh() {
  cat > "$SHIM/ssh" <<SH
#!/bin/sh
case " \$* " in *" -T "*) echo "Hi $1! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1;; esac
echo "ssh: not available in the docs world" >&2; exit 255
SH
  cat > "$SHIM/gh" <<'SH'
#!/bin/sh
echo "To get started with GitHub CLI, please run:  gh auth login" >&2
exit 4
SH
  chmod +x "$SHIM/ssh" "$SHIM/gh"
}

# ---------------------------------------------------------------------------
# guided-setup: what a setup is and how lore picks one, shown with the commands
# behind the setup page (docs/team/guided-setup.md). A fake ssh and a fake gh
# (not logged in) stand in for GitHub's: nothing reaches the network.
s_guided_setup() {
  section guided-setup
  person tomas tomas@agency.example
  agency_config tomas "human:tomas@agency.example" "Tomas Weber" tomas
  fake_ssh tomas-dev
  remote agency/notes; remote agency/site; remote acme/lab
  make_store tomas agency git@github.com:agency/notes
  quiet tomas work "mkdir -p agency personal clients && git clone -q https://github.com/agency/site agency/site && cd agency/site && echo '# site' > README.md && git add . && git commit -qm init && git push -q origin main && lore init --non-interactive --name=site >/dev/null 2>&1"
  quiet tomas work "git clone -q https://github.com/acme/lab lab && cd lab && echo '# lab' > README.md && git add . && git commit -qm init && git push -q origin main && lore init --non-interactive --name=lab >/dev/null 2>&1"
  mkdir -p "$HOMES/tomas/.ssh"
  printf 'Host github.com\n  HostName github.com\n  IdentityFile ~/.ssh/id_ed25519\n\nHost gh-agency\n  HostName github.com\n  IdentityFile ~/.ssh/id_agency\n  IdentitiesOnly yes\n' > "$HOMES/tomas/.ssh/config"
  cat >> "$HOMES/tomas/.lore/config.yaml" <<'Y'
setups:
  - name: agency
    kind: admin
    org: agency
    default: true
    folders:
      - ~/work/agency/**
    github:
      user: tomas-dev
      host_alias: github.com
  - name: personal
    kind: solo
    folders:
      - ~/work/personal/**
    identity:
      key: human:tomas@home.example
      name: Tomas Weber
    personal_store: none
Y

  note "plan"
  on tomas . "lore setup plan --kind member | cut -c1-52"
  on tomas . "lore setup < /dev/null; echo \"exit \$?\""

  note "aliases"
  on tomas . "lore setup aliases"

  note "this folder"
  on tomas work/agency/site "lore setup list | tail -1"
  on tomas work/lab "lore setup list | tail -2"

  note "tie"
  on tomas . "lore setup edit personal --folder '~/work/**'"
  # setup edit refuses to make a tie; a hand-edited config.yaml can still hold one
  on! tomas . "lore setup edit agency --folder '~/work/**'"
  sed -i 's#^      - ~/work/agency/\*\*$#      - ~/work/**#' "$HOMES/tomas/.lore/config.yaml"
  on! tomas work/lab "lore task add 'renew the certificate'"
  on tomas work/lab "lore task list"
  on tomas . "lore setup edit agency --folder '~/work/agency/**'"

  note "unknown name"
  on! tomas work/agency/site "lore --setup nope task add 'check the sitemap'"
  on! tomas work/agency/site "lore --setup nope task list"

  note "no personal store"
  on! tomas work/agency/site "lore --setup personal memory add --to me --body 'Renew the domain in March.'"

  note "invite and join"
  quiet tomas work/agency/site "lore actor register"
  on tomas work/agency/site "lore team invite mira@agency.example"
  # The line holds the random invite id: Mira's command reads it from $INVITE, so the
  # transcript stays the same on every run.
  local invite
  invite=$(grep -o 'lore-invite:v1:[A-Za-z0-9_=-]*' <<<"$LAST_OUT" | head -1)
  person mira mira@agency.example
  EXTRA_ENV="INVITE=$invite" on mira . "lore setup join \"\$INVITE\" --json | python3 -m json.tool | sed -n '/\"invite\": {/,/}/p'"

  note "fix"
  on! tomas work/agency/site "lore setup fix agency --check github.gh_account"
}

main() {
  local all=(getting_started solo small_team agency group staging contractor two_orgs personal agents leak_cleanup errors internals team_ops people store_push live_repair profiles setup_cli invites client_folders guided_setup)
  local want=("$@")
  [ ${#want[@]} -eq 0 ] && want=("${all[@]}")
  if [ ${#want[@]} -gt 1 ] && [ "$JOBS" -gt 1 ]; then
    parallel_sections "$JOBS" "$SCRATCH/logs" "$OUT" "$HERE/run-examples.sh" "${want[@]}" || exit 1
    echo "run-examples: every example ran as documented; transcripts in ${OUT#"$REPO"/}" >&2
    return 0
  fi
  for s in "${want[@]}"; do
    "s_${s//-/_}"
  done
  python3 "$HERE/normalize.py" "$TRANSCRIPT"
  if [ ${#KNOWN[@]} -ne 0 ]; then
    echo "run-examples: ${#KNOWN[@]} known failure(s), each a reported lore bug (see the KNOWN FAILURE lines above):" >&2
    printf '  %s\n' "${KNOWN[@]}" >&2
  fi
  if [ "$FAILURES" -ne 0 ]; then
    echo "run-examples: $FAILURES example command(s) did not behave as documented" >&2
    exit 1
  fi
  echo "run-examples: every example ran as documented; transcripts in ${OUT#"$REPO"/}" >&2
}

main "$@"
