# Step 10 (sourced by run.sh): a second org entry on the same store URL, branch staging.

begin_step "10 staging store branch"
refused any dev1 dev1/app "lore task add 'stag before init' --to agency-stag"
ok dev1 dev1/app "lore store init $STORE_URL --branch staging"
has "staging"
ok dev1 dev1/app "lore org use agency-stag && lore org link agency-stag --project client --yes"
ok dev1 dev1/app "lore task add 'INTERNAL-MARKER-14 seed staging with demo data' --to agency-stag"
has "agency-stag/client"
ok dev1 dev1/app "lore memory list; lore task list"
lacks "INTERNAL-MARKER-1 "
lacks "INTERNAL-MARKER-2 "
ok dev1 dev1/app "lore org use agency && lore task list"
lacks "INTERNAL-MARKER-14"
ok dev1 dev1/app "LORE_ORG=agency-stag lore task list"
has "INTERNAL-MARKER-14"
ok dev2 dev2/app "lore sync >/dev/null 2>&1; lore task list"
lacks "INTERNAL-MARKER-14"
ok dev2 dev2/app "LORE_ORG=agency-stag lore task list"
has "INTERNAL-MARKER-14"
staging_ref=$(retry git ls-remote "$STORE_URL" refs/lore/live/staging)
check "the store has refs/lore/live/staging" "$([ -n "$staging_ref" ] && echo 0 || echo 1)"
end_step
