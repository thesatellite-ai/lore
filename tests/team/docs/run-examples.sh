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
#
# The binary is built with -tags loretest so LORE_NOW (a fixed clock) works:
# the agents page shows a lease expiring without waiting twenty minutes. That
# tag changes nothing else in lore's behaviour or output.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
OUT="$HERE/transcripts"
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/lore-docs-examples.XXXXXX")"
[ -n "${KEEP:-}" ] && echo "keeping worlds in $SCRATCH" >&2 || trap 'rm -rf "$SCRATCH"' EXIT

if [ -z "${LORE_BIN:-}" ]; then
  LORE_BIN="$SCRATCH/bin/lore"
  (cd "$REPO" && CGO_ENABLED=0 go build -tags loretest -o "$LORE_BIN" ./saas/cmd/cli)
fi
export LORE_BIN

# shellcheck source=lib.sh
source "$HERE/lib.sh"

# section NAME: start a transcript and a fresh world for one docs section.
section() {
  mkdir -p "$OUT"
  TRANSCRIPT="$OUT/$1.txt"
  : > "$TRANSCRIPT"
  new_world "$SCRATCH/world-$1"
  echo "== $1" >&2
}

# note TEXT: a heading inside a transcript, so a docs page can find its part.
note() { printf '\n### %s\n' "$*" >> "$TRANSCRIPT"; }

# config NAME: write ~/.lore/config.yaml for NAME from stdin.
config() {
  mkdir -p "$WORLD/home/$1/.lore"
  cat > "$WORLD/home/$1/.lore/config.yaml"
}

# client_repo SLUG DIR [PERSON]: the client (default acmedev) creates SLUG with an initial commit and lore set up.
client_repo() {
  local slug="$1" dir="$2" who="${3:-acmedev}"
  remote "$slug"
  [ -d "$WORLD/home/$who" ] || person "$who" "dev@${slug%%/*}.example"
  quiet "$who" work "git clone -q https://github.com/$slug $dir && cd $dir && echo '# $dir' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=$dir >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"
}

# northwind_config NAME KEY DISPLAY HANDLE: the agency's org entry (one monorepo store).
northwind_config() {
  config "$1" <<Y
personal:
  store: local
orgs:
  - name: northwind
    identity: {key: "$2", name: "$3", handle: $4}
    stores:
      - url: git@github.com:northwind/lore
        org_folder: org
    own:   [github.com/northwind/*]
    repos: [github.com/acme/*, github.com/globex/*, github.com/initech/*]
    defaults:
      client_repos: {task: northwind, memory: northwind, rule: repo, decision: repo}
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

  note "toml migration"
  quiet alice . "mkdir -p .lore && printf 'stable_key = \"human:alice@example.com\"\ndisplay_name = \"Alice Doe\"\n' > .lore/identity.toml"
  on alice work/notes-app "lore whoami"
  on alice . "ls ~/.lore; cat ~/.lore/identity.yaml"

  note "first config"
  config alice <<'Y'
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - url: git@github.com:northwind/lore
        org_folder: org
    own:   [github.com/northwind/*]
    repos: [github.com/acme/*]
Y
  on alice . "cat ~/.lore/config.yaml"
  on alice . "lore config check"
  remote northwind/lore
  on alice . "lore store init git@github.com:northwind/lore"
  remote northwind/tools
  quiet alice work "git clone -q https://github.com/northwind/tools && cd tools && echo '# tools' > README.md && git add . && git commit -qm init && git push -q origin main"
  on alice work/tools "lore init --non-interactive --name=tools"
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
  remote initech/billing
  person dev1 dev1@initech.example; person dev2 dev2@initech.example
  quiet dev1 work "git clone -q https://github.com/initech/billing && cd billing && echo '# billing' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=billing >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"
  quiet dev2 work "git clone -q https://github.com/initech/billing"

  note "register"
  on dev1 work/billing "lore actor register"
  on dev2 work/billing "lore actor register"

  note "assign across branches"
  on dev2 work/billing "git switch -q -c feat/payments && lore task add 'review the retry logic' --assign @dev1"
  local t1; t1=$(id tsk)
  on dev1 work/billing "git switch -q -c feat/login && lore task list --mine"
  on dev1 work/billing "lore sync && lore task list --mine"
  on dev1 work/billing "lore inbox"

  note "done lands on main"
  on dev1 work/billing "lore task start $t1"
  on dev1 work/billing "echo retry > retry.txt && git add retry.txt && git commit -qm 'tighten retry' && lore task done $t1 --commit \$(git rev-parse --short HEAD)"
  on dev1 work/billing "git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3"
  on dev1 work/billing "git log --oneline -1 && git status --short"

  note "urgent rule"
  on dev2 work/billing "lore rule add --body 'never run migrations from the app container' --live"
  on dev2 work/billing "git fetch -q origin && git log --oneline -1 origin/main"

  note "explain"
  on dev1 work/billing "lore explain $t1"

  note "protected main"
  protect initech/billing
  on dev2 work/billing "lore task add 'rotate the webhook secret' --assign @dev1"
  local t2; t2=$(id tsk)
  on dev1 work/billing "lore sync >/dev/null && lore task done $t2"
  on dev1 work/billing "lore explain $t2"
  on dev1 work/billing "echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'"
  on dev1 work/billing "git log --oneline -3"
  on dev1 work/billing "git push -q -u origin feat/login"
  on dev1 work/billing "git status --short"
}

# ---------------------------------------------------------------------------
# agency: Northwind works in the client repo acme/app and keeps internal notes
# in its store northwind/lore; Acme's own developer (acmedev) uses lore too.
s_agency() {
  section agency
  person alice alice@northwind.example; person dev1 dev1@northwind.example; person acmedev dev@acme.example
  northwind_config alice "human:alice@northwind.example" "Alice Doe" alice
  northwind_config dev1 "human:dev1@northwind.example" "Dev One" dev1
  client_repo acme/app app
  remote northwind/lore

  note "store init"
  on alice . "lore store init git@github.com:northwind/lore"
  quiet alice work "git clone -q https://github.com/acme/app acme-app"
  quiet dev1 work "git clone -q https://github.com/acme/app acme-app"

  note "link"
  TTY=1 on alice work/acme-app "lore whoami"
  TTY=1 ANSWER=y on alice work/acme-app "lore org link northwind --project acme"
  on alice work/acme-app "git status --short"
  on alice work/acme-app "lore actor register"
  on dev1 work/acme-app "lore actor register"
  TTY=1 on alice work/acme-app "lore whoami"
  on alice work/acme-app "lore whoami"

  note "internal by default"
  on alice work/acme-app "lore memory add --body 'they approve estimates faster when we send them on WhatsApp first'"
  local m1; m1=$(id mem)
  on alice work/acme-app "lore rule add --body 'money is stored in minor units' --to repo"
  on alice work/acme-app "git status --short"
  on alice work/acme-app "git add -A && git commit -qm 'lore: rule' && git push -q origin main"

  note "internal task"
  on alice work/acme-app "git switch -q -c feat/checkout && lore task add 'send invoice reminder before demo' --to northwind --assign @dev1"
  local t1; t1=$(id tsk)
  on dev1 work/acme-app "lore sync >/dev/null; git switch -q -c feat/login && lore task list"
  on dev1 work/acme-app "lore inbox"
  on dev1 work/acme-app "lore task done $t1"
  on dev1 work/acme-app "git status --short"

  note "render"
  on alice work/acme-app "lore render"
  on alice work/acme-app "cat CLAUDE.local.md; git check-ignore -v CLAUDE.local.md"
  on alice work/acme-app "head -12 ~/.lore/render/*.md"

  note "leak blocked"
  on alice work/acme-app "mkdir -p src && echo '// they approve estimates faster when we send them on WhatsApp' > src/notes.ts && git add src/notes.ts"
  on! alice work/acme-app "git commit -m 'add notes'"
  on alice work/acme-app "git reset -q src/notes.ts && rm -rf src"
  on alice work/acme-app "printf 'Fixes checkout.\n\nThey approve estimates faster when we send them on WhatsApp first.\n' > pr.md"
  on! alice work/acme-app "lore check pr-body pr.md"
  on alice work/acme-app "printf 'Fixes checkout totals for INR.\n' | lore check pr-body -"
  on_known BUG-D2 alice work/acme-app "rm pr.md && lore check leaks --ci"

  note "agent rule refused"
  on alice work/acme-app "lore agent start --label backend-1 --owner @alice"
  local actor session
  actor=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2); session=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  EXTRA_ENV="LORE_ACTOR=$actor LORE_SESSION=$session" on! alice work/acme-app "lore rule add --body 'require prepayment from this client'"

  note "share"
  on alice work/acme-app "lore memory add --body 'checkout must support INR and USD'"
  local m2; m2=$(id mem)
  TTY=1 ANSWER=y on_known BUG-D1 alice work/acme-app "lore share $m2 --to repo"
  on alice work/acme-app "git status --short"

  note "client view"
  on acmedev work/app "git pull -q && lore memory list"
  on acmedev work/app "lore rule list"
  on acmedev work/app "lore task list"
  on acmedev work/app "lore whoami"
  on acmedev work/app "git fetch -q origin '+refs/*:refs/remotes/all/*' && git grep -i -c whatsapp \$(git for-each-ref --format='%(refname)' refs/remotes/all) -- . || echo 'no match in any ref'"
}

# ---------------------------------------------------------------------------
# group: three Globex repos act as one product through one internal project.
s_group() {
  section group
  person alice alice@northwind.example
  northwind_config alice "human:alice@northwind.example" "Alice Doe" alice
  remote northwind/lore
  quiet alice . "lore store init git@github.com:northwind/lore"
  for r in globex-api globex-web globex-proxy; do client_repo "globex/$r" "$r" globexdev; quiet alice work "git clone -q https://github.com/globex/$r"; done

  note "link three repos"
  on alice work/globex-api "lore org link northwind --project globex --yes"
  on alice work/globex-web "lore org link northwind --project globex --yes"
  on alice work/globex-proxy "lore org link northwind --project globex --yes"
  on alice work/globex-api "lore actor register"

  note "mission across repos"
  on alice work/globex-api "lore mission add 'Invite landing page' --to northwind/globex --key invite"
  local mis; mis=$(id msn)
  on alice work/globex-api "lore task add 'invite mutation + email' --to northwind/globex --mission $mis --repo globex-api --key invite/backend"
  local tb; tb=$(id tsk)
  on alice work/globex-api "lore task add 'invite landing route' --to northwind/globex --mission $mis --repo globex-web --after $tb --key invite/frontend"
  on alice work/globex-api "lore task add 'invite mutation + email' --to northwind/globex --mission $mis --repo globex-api --key invite/backend"
  on alice work/globex-web "lore task list"
  on alice work/globex-web "lore task list --all-repos"
  on alice work/globex-proxy "lore mission status $mis"
}

# ---------------------------------------------------------------------------
# staging: the same store, a second branch, chosen only on purpose.
s_staging() {
  section staging
  person alice alice@northwind.example
  config alice <<'Y'
personal:
  store: local
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore", org_folder: org}
    repos: [github.com/acme/*]
  - name: northwind-stag
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore", branch: staging, org_folder: org}
Y
  remote northwind/lore
  client_repo acme/app app
  quiet alice . "lore store init git@github.com:northwind/lore"
  quiet alice work "git clone -q https://github.com/acme/app acme-app"
  quiet alice work/acme-app "lore org link northwind --project acme --yes"

  note "staging branch"
  on alice work/acme-app "lore store init git@github.com:northwind/lore --branch staging"
  on alice work/acme-app "lore org use northwind-stag"
  on alice work/acme-app "lore whoami"
  on alice work/acme-app "lore org link northwind-stag --project acme --yes"
  on alice work/acme-app "lore task add 'seed staging with demo data' --to northwind-stag"
  on alice work/acme-app "lore task list"
  on alice work/acme-app "lore org use northwind"
  on alice work/acme-app "lore task list"
  on alice work/acme-app "LORE_ORG=northwind-stag lore task list"
  on alice work/acme-app "git ls-remote https://github.com/northwind/lore"
}

# ---------------------------------------------------------------------------
# contractor: one store per client; the contractor lists only the company store and Acme's.
s_contractor() {
  section contractor
  person alice alice@northwind.example; person cont1 cont1@northwind.example
  remote northwind/lore-org; remote northwind/lore-acme; remote northwind/lore-globex
  client_repo acme/app app
  config alice <<'Y'
personal:
  store: local
orgs:
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore-org", org_folder: "."}
      - {url: "git@github.com:northwind/lore-acme"}
      - {url: "git@github.com:northwind/lore-globex"}
    repos: [github.com/acme/*, github.com/globex/*]
Y
  config cont1 <<'Y'
personal:
  store: local
orgs:
  - name: northwind
    identity: {key: "human:cont1@northwind.example", name: "Casey Contractor", handle: cont1}
    stores:
      - {url: "git@github.com:northwind/lore-org", org_folder: "."}
      - {url: "git@github.com:northwind/lore-acme"}
    repos: [github.com/acme/app]
Y
  note "one store per client"
  on alice . "lore store init git@github.com:northwind/lore-org && lore store init git@github.com:northwind/lore-acme && lore store init git@github.com:northwind/lore-globex"
  quiet alice work "git clone -q https://github.com/acme/app acme-app"
  on alice work/acme-app "lore org link northwind --project acme --store git@github.com:northwind/lore-acme --yes"
  on alice work/acme-app "lore actor register"
  on alice work/acme-app "lore memory add --body 'the Acme staging database is restored every Sunday night'"
  on alice work/acme-app "lore rule add --to northwind/. --body 'every client estimate is reviewed by two people before it is sent'"

  note "contractor view"
  quiet cont1 work "git clone -q https://github.com/acme/app acme-app"
  TTY=1 on cont1 work/acme-app "lore whoami"
  on cont1 work/acme-app "lore actor register"
  on cont1 work/acme-app "lore memory list"
  on cont1 . "lore store show"
  on cont1 . "ls ~/.lore/clones/github.com/northwind"
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
  - name: northwind
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore", org_folder: org}
    own:   [github.com/northwind/*]
    repos: [github.com/acme/*]
  - name: betalabs
    identity: {key: "human:alice@betalabs.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:betalabs/lore", org_folder: org}
    own: [github.com/betalabs/*]
Y
  remote northwind/lore; remote betalabs/lore
  client_repo acme/app app
  client_repo betalabs/api api betadev
  client_repo someone/tool tool somedev
  quiet alice . "lore store init git@github.com:northwind/lore && lore store init git@github.com:betalabs/lore"
  quiet alice work "git clone -q https://github.com/acme/app acme-app && git clone -q https://github.com/betalabs/api betalabs-api && git clone -q https://github.com/someone/tool forked-tool"
  quiet alice work/acme-app "lore org link northwind --project acme --yes"

  note "whoami per repo"
  on alice work/acme-app "lore whoami"
  on alice work/betalabs-api "lore whoami"
  on alice work/forked-tool "lore whoami"
  on alice work/forked-tool "lore whoami --explain"

  note "personal notes split"
  on alice work/acme-app "lore memory add --body 'ask the Acme PM before touching the payment flow' --to me"
  on alice work/betalabs-api "lore memory add --body 'betalabs deploys only on Tuesdays' --to me"
  on alice work/acme-app "lore memory list"
  on alice work/betalabs-api "lore memory list"
  on alice work/forked-tool "lore memory list"
}

# ---------------------------------------------------------------------------
# personal: private notes, local or synced through your own private repo.
s_personal() {
  section personal
  person alice alice@northwind.example
  northwind_config alice "human:alice@northwind.example" "Alice Doe" alice
  remote northwind/lore; client_repo acme/app app; remote alice/scratch
  quiet alice . "lore store init git@github.com:northwind/lore"
  quiet alice work "git clone -q https://github.com/acme/app acme-app"
  quiet alice work/acme-app "lore org link northwind --project acme --yes"
  quiet alice work "git clone -q https://github.com/alice/scratch 2>/dev/null; cd scratch && git commit -q --allow-empty -m init && git push -q origin main && lore init --non-interactive --name=scratch >/dev/null 2>&1"

  note "local personal store"
  on alice work/acme-app "lore memory add --body 'I always forget the staging VPN profile' --to me"
  on alice work/scratch "lore memory add --body 'I prefer tabs over spaces' --to me"
  TTY=1 ANSWER=n on! alice work/acme-app "lore memory add --body 'northwind invoices go to the finance inbox' --to me --everywhere"
  on alice work/acme-app "lore memory list"
  on alice work/scratch "lore memory list"
  on alice . "find ~/.lore/personal -name '*.json' -path '*memories*' | sed 's#.*/personal/##'"

  note "git personal store"
  remote alice/lore-me
  person alice-devbox alice@northwind.example
  for who in alice alice-devbox; do
    northwind_config "$who" "human:alice@northwind.example" "Alice Doe" alice
    sed -i 's#^  store: local#  store: git@github.com:alice/lore-me#' "$WORLD/home/$who/.lore/config.yaml"
  done
  on alice . "lore store init git@github.com:alice/lore-me"
  on alice work/acme-app "lore memory add --body 'the Acme invoice template lives in the shared drive' --to me"
  quiet alice-devbox work "git clone -q https://github.com/acme/app acme-app"
  on alice-devbox work/acme-app "lore memory list"
}

# ---------------------------------------------------------------------------
# agents: four agents and a person on one Globex mission (the worked example).
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

s_agents() {
  section agents
  person alice alice@northwind.example
  northwind_config alice "human:alice@northwind.example" "Alice Doe" alice
  remote northwind/lore
  quiet alice . "lore store init git@github.com:northwind/lore"
  for r in globex-api globex-web globex-proxy; do
    client_repo "globex/$r" "$r" globexdev
    quiet alice work "git clone -q https://github.com/globex/$r && cd $r && lore org link northwind --project globex --yes"
  done
  quiet alice work/globex-api "lore actor register"
  CLOCK=2026-11-02T10:00:00Z

  note "plan"
  cat > "$WORLD/home/alice/work/globex-api/invite.yaml" <<'Y'
mission: {key: invite, title: "Invite landing page", to: northwind/globex}
approvers: ["@alice"]
tasks:
  - {key: backend,  title: "invite mutation + email",    repo: globex-api,   timebox: 60m}
  - {key: frontend, title: "invite landing route",       repo: globex-web,   after: [backend], timebox: 60m}
  - {key: proxy,    title: "allow /invite on the proxy", repo: globex-proxy}
  - {key: deploy,   title: "deploy to staging",          role: coordinator, after: [frontend, proxy], gate: required}
Y
  on alice work/globex-api "cat invite.yaml"
  start_agent coord work/globex-api
  ag coord work/globex-api "lore plan apply invite.yaml"
  local mis; mis=$(id msn)
  ag coord work/globex-api "lore plan apply invite.yaml"
  ag coord work/globex-api "lore task claim $mis"

  note "workers start"
  for r in globex-api globex-web globex-proxy; do quiet alice work/agents "mkdir -p \$(echo $r) && cd $r && [ -d .git ] || git clone -q https://github.com/globex/$r ."; done
  start_agent backend-1 work/agents/globex-api
  ag backend-1 work/agents/globex-api "lore inbox"
  ag backend-1 work/agents/globex-api "lore task next --mission $mis --repo globex-api"
  local tb; tb=$(id tsk)
  start_agent frontend-1 work/agents/globex-web
  ag! frontend-1 work/agents/globex-web "lore task next --mission $mis --repo globex-web"
  start_agent proxy-1 work/agents/globex-proxy
  ag proxy-1 work/agents/globex-proxy "lore task next --mission $mis --repo globex-proxy --owns nginx/sites/invite.conf"
  local tp; tp=$(id tsk)

  note "finding"
  ag proxy-1 work/agents/globex-proxy "lore finding add --mission $mis 'the proxy strips X-Forwarded-Host; links must use the public base URL'"
  ag backend-1 work/agents/globex-api "lore inbox"
  ag proxy-1 work/agents/globex-proxy "lore task done $tp"

  note "done then merged"
  ag backend-1 work/agents/globex-api "lore task note $tb 'mutation inviteUser(email) returns a token; email template invite.html'"
  ag backend-1 work/agents/globex-api "git switch -q -c agent/backend-1/invite && mkdir -p api && echo 'package api' > api/invite.go && git add api && git commit -qm 'invite mutation' && git push -q -u origin agent/backend-1/invite"
  ag backend-1 work/agents/globex-api "lore task done $tb --commit \$(git rev-parse HEAD) --lesson 'invite tokens expire in 48h, documented in the README'"
  ag! frontend-1 work/agents/globex-web "lore task next --mission $mis --repo globex-web"
  on alice work/globex-api "git pull -q && git merge -q --no-ff --no-edit origin/agent/backend-1/invite && git push -q origin main"
  on alice work/globex-api "lore task list"
  ag frontend-1 work/agents/globex-web "lore task next --mission $mis --repo globex-web"
  local tf; tf=$(id tsk)

  note "crash and reclaim"
  ag frontend-1 work/agents/globex-web "git switch -q -c agent/frontend-1/invite-route && mkdir -p src/routes && echo 'export {}' > src/routes/invite.tsx && git add src && git commit -qm 'route + loader'"
  ag frontend-1 work/agents/globex-web "lore task note $tf 'route + loader done, form next'"
  CLOCK=$(date -u -d "$CLOCK + 25 minutes" +%Y-%m-%dT%H:%M:%SZ)
  ag coord work/globex-api "lore inbox"
  ag coord work/globex-api "lore mission status $mis"
  quiet alice work/agents "mkdir -p fe2 && cd fe2 && git clone -q https://github.com/globex/globex-web"
  start_agent frontend-2 work/agents/fe2/globex-web
  ag frontend-2 work/agents/fe2/globex-web "lore task next --mission $mis --repo globex-web"
  ag frontend-2 work/agents/fe2/globex-web "git fetch -q origin agent/frontend-1/invite-route && git log --oneline -1 FETCH_HEAD"
  ag! frontend-1 work/agents/globex-web "lore task done $tf"
  ag frontend-2 work/agents/fe2/globex-web "git switch -q -c agent/frontend-2/invite-route FETCH_HEAD && echo form >> src/routes/invite.tsx && git commit -qam 'invite form' && git push -q -u origin agent/frontend-2/invite-route && lore task done $tf --commit \$(git rev-parse HEAD)"
  on alice work/globex-web "git pull -q && git merge -q --no-ff --no-edit origin/agent/frontend-2/invite-route && git push -q origin main && lore task list >/dev/null"

  note "merged by hand"
  ag! coord work/globex-api "lore task next --mission $mis --role coordinator"
  on alice work/globex-proxy "lore task merged $tp"

  note "gate"
  ag! coord work/globex-api "lore task next --mission $mis --role coordinator"
  local tg; tg=$(grep -o 'tsk_[0-9a-f]\{32\}' <<<"$LAST_OUT" | head -1 || true)
  ag! coord work/globex-api "lore gate approve $tg"
  on alice work/globex-api "lore gate approve $tg"
  ag coord work/globex-api "lore task next --mission $mis --role coordinator"

  note "mission status"
  on alice work/globex-proxy "lore mission status $mis"
  on alice work/globex-proxy "lore task show $tg --from northwind/globex"
  on alice work/globex-proxy "lore task show $tf --from northwind/globex"
}

# ---------------------------------------------------------------------------
# leak-cleanup: an internal note was written to the client's live ref by mistake.
s_leak_cleanup() {
  section leak-cleanup
  person alice alice@northwind.example
  northwind_config alice "human:alice@northwind.example" "Alice Doe" alice
  remote northwind/lore; client_repo acme/app app
  quiet alice . "lore store init git@github.com:northwind/lore"
  quiet alice work "git clone -q https://github.com/acme/app acme-app && cd acme-app && lore org link northwind --project acme --yes"

  note "the mistake"
  on alice work/acme-app "lore task add 'chase Acme about the unpaid March invoice' --to repo"
  local t; t=$(id tsk)
  on acmedev work/app "lore sync >/dev/null; lore task list"

  note "purge"
  on alice work/acme-app "lore live purge $t"
  on alice work/acme-app "lore live compact --dry-run"
  on alice work/acme-app "lore live compact"
  on alice work/acme-app "lore live log"
  on acmedev work/app "lore sync >/dev/null; lore task list"
  person newdev dev2@acme.example
  on newdev work "git clone -q --mirror https://github.com/acme/app app.git && cd app.git && git for-each-ref --format='%(refname)' && for c in \$(git rev-list --all); do git grep -q -i 'unpaid March' \$c && echo \"found in \$c\"; done; echo 'searched every commit the remote serves'"
}

# ---------------------------------------------------------------------------
# errors: the error messages people meet, each produced for real.
s_errors() {
  section errors
  person alice alice@northwind.example; person dev2 dev2@northwind.example
  northwind_config alice "human:alice@northwind.example" "Alice Doe" alice
  northwind_config dev2 "human:dev2@northwind.example" "Dev Two" dev2
  remote northwind/lore; client_repo acme/app app
  quiet alice . "lore store init git@github.com:northwind/lore"
  quiet alice work "git clone -q https://github.com/acme/app acme-app && cd acme-app && lore org link northwind --project acme --yes && lore actor register"

  note "config invalid"
  cp "$WORLD/home/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  sed -i 's#org_folder: org#org_folder: org\n        branch: release/2026#' "$WORLD/home/alice/.lore/config.yaml"
  on! alice . "lore config check"
  on! alice work/acme-app "lore whoami"
  cp "$WORLD/alice-config.bak" "$WORLD/home/alice/.lore/config.yaml"

  note "not a store"
  client_repo globex/globex-docs globex-docs globexdev
  quiet globexdev work/globex-docs "mkdir -p src && echo 'export {}' > src/index.ts && echo '{}' > package.json && git add . && git commit -qm code && git push -q origin main"
  cp "$WORLD/home/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  sed -i 's#^    own:#      - url: git@github.com:globex/globex-docs\n    own:#' "$WORLD/home/alice/.lore/config.yaml"
  on! alice work/acme-app "lore whoami"
  on! alice . "lore store doctor"
  cp "$WORLD/alice-config.bak" "$WORLD/home/alice/.lore/config.yaml"

  note "org ambiguous"
  cp "$WORLD/home/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  cat >> "$WORLD/home/alice/.lore/config.yaml" <<'Y'
  - name: initech
    identity: {key: "human:alice@initech.example", name: "Alice Doe"}
    repos: [github.com/acme/*]
Y
  client_repo acme/site site
  quiet alice work "git clone -q https://github.com/acme/site acme-site"
  on! alice work/acme-site "lore whoami"
  on! alice work/acme-site "lore memory add --body 'the site deploys from the release branch'"
  cp "$WORLD/alice-config.bak" "$WORLD/home/alice/.lore/config.yaml"

  note "link unknown"
  mv "$HOSTS/northwind/lore.git" "$WORLD/lore.git.away"
  quiet dev2 work "git clone -q https://github.com/acme/app acme-app"
  on! dev2 work/acme-app "lore memory add --body 'the client wants weekly demos'"

  note "offline claim"
  mv "$WORLD/lore.git.away" "$HOSTS/northwind/lore.git"
  quiet alice work/acme-app "lore task add 'rotate the API keys' --to northwind"
  local t; t=$(grep -o 'tsk_[0-9a-f]\{32\}' "$WORLD/quiet.log" | head -1 || true)
  mv "$HOSTS/northwind/lore.git" "$WORLD/lore.git.away"
  on! alice work/acme-app "lore task claim $t"
  on alice work/acme-app "lore memory add --body 'the API keys rotate every 90 days' --to northwind || true"
  mv "$WORLD/lore.git.away" "$HOSTS/northwind/lore.git"
  on alice work/acme-app "lore sync"

  note "store branch missing"
  cat >> "$WORLD/home/alice/.lore/config.yaml" <<'Y'
  - name: northwind-stag
    identity: {key: "human:alice@northwind.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:northwind/lore", branch: staging, org_folder: org}
Y
  on! alice work/acme-app "LORE_ORG=northwind-stag lore org link northwind-stag --project acme --yes"

  note "refs refused by the host"
  remote widgets/portal
  cat > "$HOSTS/widgets/portal.git/hooks/pre-receive" <<'HOOK'
#!/bin/sh
while read old new ref; do
  case "$ref" in refs/lore/*) echo "remote: refusing $ref: only branches and tags may be pushed here" >&2; exit 1;; esac
done
HOOK
  chmod +x "$HOSTS/widgets/portal.git/hooks/pre-receive"
  quiet dev2 work "git clone -q https://github.com/widgets/portal 2>/dev/null; cd portal && git commit -q --allow-empty -m init && git push -q origin main && lore init --non-interactive --name=portal >/dev/null 2>&1"
  on dev2 work/portal "lore task add 'set up the portal CI'"
  on dev2 work/portal "lore live diff"
  on dev2 work/portal "lore doctor 2>&1 | grep -i -E 'live|refs/lore|push' || true"
}

# ---------------------------------------------------------------------------
# internals: what lore actually writes in git, for the concepts and how-it-works pages.
s_internals() {
  section internals
  person alice alice@northwind.example
  northwind_config alice "human:alice@northwind.example" "Alice Doe" alice
  remote northwind/lore; client_repo acme/app app
  quiet alice . "lore store init git@github.com:northwind/lore"
  quiet alice work "git clone -q https://github.com/acme/app acme-app && cd acme-app && lore org link northwind --project acme --yes && lore actor register"

  note "refspec"
  on alice work/acme-app "git config --get-all remote.origin.fetch"

  note "monorepo live tree"
  on acmedev work/app "mkdir -p web && cd web && lore init --non-interactive --name=web >/dev/null 2>&1; cd .. && lore task add 'fix header' --to repo && cd web && lore rule add --body 'use the design tokens' --live --to repo"
  on acmedev work/app "git ls-tree -r --name-only refs/lore/live"
  on acmedev work/app "git log --oneline refs/lore/live"
  on acmedev work/app "git cat-file -p refs/lore/live:_live/meta.json; echo; git cat-file -p refs/lore/live:_live/epoch"
  on acmedev work/app "git cat-file -p refs/lore/live:\$(git ls-tree -r --name-only refs/lore/live | grep '_root/tasks/')"
  on acmedev work/app "git branch -a"

  note "store layout"
  on alice work/acme-app "lore task add 'renew the Acme SSL certificate' --to northwind"
  local t; t=$(id tsk)
  on alice work/acme-app "lore memory add --body 'Acme wants a demo every second Friday'"
  on alice . "git -C ~/.lore/clones/github.com/northwind/lore.git ls-tree -r --name-only main | grep -v '_meta.json\|.gitattributes' | head -20"
  on alice . "git -C ~/.lore/clones/github.com/northwind/lore.git for-each-ref --format='%(refname)'"
  on alice . "git -C ~/.lore/clones/github.com/northwind/lore.git ls-tree -r --name-only refs/lore-remote/origin/live/main"
  on alice . "git -C ~/.lore/clones/github.com/northwind/lore.git cat-file -p main:acme/.lore/data/_meta.json"

  note "signals"
  on alice work/acme-app "lore agent start --label backend-1 --owner @alice"
  local a s2
  a=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2); s2=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  EXTRA_ENV="LORE_ACTOR=$a LORE_SESSION=$s2" AS=backend-1 on alice work/acme-app "lore task claim $t"
  on alice . "cd ~/.lore/clones/github.com/northwind/lore.git && git fetch -q origin && git ls-tree -r --name-only refs/lore-remote/origin/signals/main && git log --oneline refs/lore-remote/origin/signals/main"
  on alice . "cd ~/.lore/clones/github.com/northwind/lore.git && git cat-file -p refs/lore-remote/origin/signals/main:\$(git ls-tree -r --name-only refs/lore-remote/origin/signals/main | head -1)"
  EXTRA_ENV="LORE_ACTOR=$a LORE_SESSION=$s2" AS=backend-1 on alice work/acme-app "lore task heartbeat $t"
  on alice . "cd ~/.lore/clones/github.com/northwind/lore.git && git fetch -q origin && git log --oneline refs/lore-remote/origin/signals/main"

  note "hooks"
  on alice work/acme-app "ls .git/hooks | grep -v sample; grep -h 'lore' .git/hooks/pre-commit | head -5"
  on alice work/acme-app "cat .git/info/exclude | tail -2"
}

main() {
  local all=(getting_started solo small_team agency group staging contractor two_orgs personal agents leak_cleanup errors internals)
  local want=("$@")
  [ ${#want[@]} -eq 0 ] && want=("${all[@]}")
  for s in "${want[@]}"; do
    "s_${s//-/_}"
  done
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
