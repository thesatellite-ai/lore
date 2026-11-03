# Step 02 (sourced by run.sh): lore init in the client repo, store init, org link, roster.

begin_step "02 init, store, org link"
# The store first: the leak hooks in the client repo fail closed while the store is not a lore store yet.
ok dev1 dev1/app "lore store init $STORE_URL"
has "lore-store.yaml"
ok dev1 dev1/app "lore init --non-interactive --name=client-website && git add -A && git commit -qm 'lore: init' && retry git push -q origin main"
refused any dev1 dev1/app "printf 'y\\n' | lore org link agency --project client"
has "confirm with --yes"
ok dev1 dev1/app "lore org link agency --project client --yes"
has "linked to .*pushed"
ok dev1 dev1/app "git status --porcelain"
check "org link wrote nothing into the client checkout" "$([ -z "$LAST_OUT" ] && echo 0 || echo 1)"
ok dev2 dev2/app "retry git pull -q && lore sync >/dev/null 2>&1; lore whoami"
has "1 link\\(s\\)"
ok dev1 dev1/app "lore actor register"
ok dev2 dev2/app "lore actor register"
ok dev1 dev1/app "lore whoami"
has "client repo +\\(guards on\\)"
has "^org +agency"
end_step
