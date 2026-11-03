# Step 01 (sourced by run.sh): homes, git identities, lore configs, one clone per person.

begin_step "01 setup"

# mk_home NAME DISPLAY EMAIL: a machine (HOME) with a git identity.
mk_home() {
  local h="$T/homes/$1"
  mkdir -p "$h/.lore"
  HOME=$h git config --global user.name "$2"
  HOME=$h git config --global user.email "$3"
  HOME=$h git config --global init.defaultBranch main
  HOME=$h git config --global pull.rebase false
}
mk_home dev1 "Dev One" dev1@example.test
mk_home dev2 "Dev Two" dev2@example.test
mk_home client "Carol Client" carol@client.test

# agency_cfg KEY NAME: an agency developer's config: org agency (store branch main)
# and org agency-stag (same store, branch staging), short leases so a killed agent
# expires within the trial.
agency_cfg() {
  cat <<Y
identity:
  key: human:$1@example.test
  name: $2
personal:
  store: local
lease:
  ttl: 90s
  renew_every: 30s
  grace: 15s
orgs:
  - name: agency
    identity: {key: "human:$1@agency.test", name: "$2", handle: $1}
    stores:
      - url: $STORE_URL
        org_folder: org
    repos: [$CLIENT_SLUG]
    defaults:
      client_repos: {task: agency, memory: agency, rule: repo, decision: repo}
  - name: agency-stag
    identity: {key: "human:$1@agency.test", name: "$2", handle: $1}
    stores:
      - {url: "$STORE_URL", branch: staging, org_folder: org}
Y
}
agency_cfg dev1 "Dev One" > "$T/homes/dev1/.lore/config.yaml"
agency_cfg dev2 "Dev Two" > "$T/homes/dev2/.lore/config.yaml"
chmod 600 "$T"/homes/*/.lore/config.yaml

for a in dev1 dev2 client agent-a agent-b; do
  mkdir -p "$T/work/$a"
  HOME=$(home_of "$a") retry git clone -q "$CLIENT_URL" "$T/work/$a/app" >> "$LOG" 2>&1
  check "clone the client repo for $a" "$([ -f "$T/work/$a/app/$MARKER_FILE" ] && echo 0 || echo 1)"
done
ok dev1 dev1/app "lore --version; lore config check"
has "is valid: 2 org entries"
ok dev2 dev2/app "lore config check"
has "is valid: 2 org entries"
end_step
