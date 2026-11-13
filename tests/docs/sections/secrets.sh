# secrets: secret references, who holds them, and the rules that find secrets
# (docs/operations/secrets-and-pii.md). Alice keeps the client's credentials in her
# password manager; the notes say where each one lives. No manager is opened except
# env:// (an environment variable set for one command, a dummy value).
lore_repo alice client/website

note "secret ref"
on alice work/website "lore memory add --secret-ref 'gopass://acme/website/staging-deploy' --body 'Deploy key for staging: AKIA''IOSFODNN7EXAMPLE'"
on alice work/website "lore memory list"
on alice work/website "grep -rh staging .lore/data/memories"
on alice work/website "lore secrets list"

# The example passwords are joined at run time, so no committed file (this script,
# the transcript, the page) holds a secret-shaped literal; lore's detector sees the
# joined value through the environment, exactly as it would a typed one.
local relay_password db_password
relay_password=$(printf '%s' Sup3r S3cret Relay9)
db_password=$(printf '%s' Hx7p Q2mZ 9vR4 tL8w)

note "unstored"
EXTRA_ENV="RELAY_PASSWORD=$relay_password" on alice work/website "lore memory add --secret-unstored --body \"Mail relay password=\$RELAY_PASSWORD\""
local unstored_mem; unstored_mem=$(id mem)
on alice work/website "lore secrets list --unstored"
on alice work/website "lore task list"

note "link"
on alice work/website "lore secrets link --id $unstored_mem --at 1 --ref 'gopass://acme/website/mail-relay'"
on alice work/website "lore secrets show --id $unstored_mem"

note "rename entry"
on alice work/website "lore secrets rename-entry --manager gopass --from acme/website/mail-relay --to acme/website/smtp-relay"
on alice work/website "lore secrets list --manager gopass"
on alice work/website "lore secrets relabel"

note "move"
person bob bob@agency.example
quiet alice work/website "git add -A && git commit -qm 'lore: notes' && git push -q origin main"
quiet bob work "git clone -q https://github.com/client/website"
on bob work/website "lore whoami | grep identity"
local bob_act; bob_act=$(id act)
on alice work/website "lore whoami --json | grep '\"machine\"'"
local alice_mch; alice_mch=$(grep -o 'mch_[0-9a-f]*' <<<"$LAST_OUT" | head -1)
on alice work/website "lore secrets move --from $alice_mch --to $bob_act --yes"
on alice work/website "lore secrets list --holder $bob_act"
on alice work/website "lore task list"

note "env ref"
EXTRA_ENV="DB_PASSWORD=$db_password" on alice work/website "lore memory add --secret-ref 'env://STAGING_DB_PASSWORD' --body \"Staging database password=\$DB_PASSWORD\""
on alice work/website "lore secrets check"
EXTRA_ENV="STAGING_DB_PASSWORD=dummy-value" on alice work/website "lore secrets check"

note "reveal"
EXTRA_ENV="STAGING_DB_PASSWORD=dummy-value" on! alice work/website "lore secrets show --id env://STAGING_DB_PASSWORD --reveal"
EXTRA_ENV="STAGING_DB_PASSWORD=dummy-value" on alice work/website "lore secrets show --id env://STAGING_DB_PASSWORD --reveal --force"

note "allow"
on alice work/website "lore memory add --body 'Test fixture token: ghp_''16C7e42F292c6912E7710c838347Ae178B4a' --not-a-secret --reason 'fixture in tests' --yes"
local fx; fx=$(id mem)
on alice work/website "lore secrets allow list"
local sal; sal=$(grep -o 'sal_[0-9a-f]\{32\}' <<<"$LAST_OUT" | tail -1)
on alice work/website "lore secrets allow rm --id $sal --yes"
on alice work/website "lore secrets allow list | grep -c sal_"
on alice work/website "lore secrets allow $fx --rule github-pat --match 'ghp_''16C7e42F292c6912E7710c838347Ae178B4a' --reason 'fixture in tests'"

note "rules"
on alice work/website "lore secrets rules"
on alice work/website "lore secrets rule off --id generic-password --reason 'our tests use fake passwords' --yes"
on alice work/website "lore secrets rule edit --id aws_access_key --description 'cloud keys, any account' --yes"
on alice work/website "lore secrets rules"
on alice work/website "lore secrets rule on --id generic-password --yes"
on alice work/website "lore secrets rules"

note "pii pattern"
on! alice work/website "lore pii-pattern add --name phone --regex '[0-9]{10}'"
on! alice work/website "lore pii-pattern disable --id phone"
on alice work/website "lore pii-pattern list"
