# resources: infrastructure facts as typed rows (docs/operations/resources.md). Alice (an
# agency developer) records the client's servers and the services that run on them: kinds
# and fields, add, list, show, graph and impact, edit and history, check and stale, clone,
# rename, bulk edit, import and export, repoint, merge, archive and delete, field and kind
# changes, a secret in a value, lint and report, sensitive fields, and moving the kinds
# into a shared store (adopt, rehome).

lore_repo alice client/infra
W=work/infra

note "kinds"
on alice $W "lore resource kind add --kind host --description 'a server we run'"
on alice $W "lore resource field add --kind host --field provider --type text --required --values hetzner,aws"
on alice $W "lore resource field add --kind host --field ip --type ip"
on alice $W "lore resource field add --kind host --field os --type text --default ubuntu-24.04"
on alice $W "lore resource kind add --kind service --description 'an app we deploy'"
on alice $W "lore resource field add --kind service --field runs_on --type ref --ref-kind host --required"
on alice $W "lore resource field add --kind service --field url --type url"
on alice $W "lore resource field add --kind service --field db_password --type secret_ref"

note "kind show"
on alice $W "lore resource kind show --id host"
on alice $W "lore resource kinds"

note "add"
on alice $W "lore resource add --kind host --name hub_prod --set provider=hetzner --set ip=203.0.113.10"
on alice $W "lore resource add --kind host --name hub_stage --set provider=aws --set ip=203.0.113.11"

note "add refused"
on! alice $W "lore resource add --kind host --name hub_test --set provider=linode"
on! alice $W "lore resource add --kind host --name Hub-Test --set provider=aws"
on! alice $W "lore resource add --kind service --name hub_cron"

note "refs"
on alice $W "lore resource add --kind service --name hub_api --set runs_on=hub_prod --set url=https://api.example.com --set db_password=gopass://acme/hub/db"
on alice $W "lore resource add --kind service --name hub_web --set runs_on=hub_prod --set url=https://www.example.com"

note "list and show"
on alice $W "lore resource list --kind host"
on alice $W "lore resource list --kind service --where runs_on=hub_prod --sort name:desc"
on alice $W "lore resource show --id hub_api"

note "graph"
on alice $W "lore resource graph --id hub_api --down"
on alice $W "lore resource graph --id hub_prod --up"

note "impact"
on alice $W "lore resource list --kind service --json | grep -o 'res_[0-9a-f]*' | head -1"
local api; api=$(id res)
on alice $W "lore task add --title 'Rotate the hub_api database password' --ref $api"
on alice $W "lore resource impact --id hub_prod"

note "check and stale"
on alice $W "lore resource stale"
on alice $W "lore resource check --id hub_prod --note 'looked in the provider console'"
on alice $W "lore resource stale"

note "edit and history"
on alice $W "lore resource edit --id hub_prod --set os=debian-12"
on alice $W "lore resource edit --id hub_stage --unset ip"
on alice $W "lore resource history --id hub_prod"
on alice $W "lore resource history --id hub_prod --field os"

note "clone and rename"
on alice $W "lore resource clone --from hub_prod --name hub_prod_2 --set ip=203.0.113.12"
on alice $W "lore resource rename --id hub_prod_2 --new-name hub_prod_new"

note "bulk edit"
on alice $W "lore resource edit --kind host --where provider=hetzner --set os=debian-13 --dry-run"
on alice $W "lore resource edit --kind host --where provider=hetzner --set os=debian-13 --yes"
on alice $W "lore resource check --kind host --where provider=hetzner --yes"

note "import"
quiet alice $W "printf 'name,provider,ip\nhub_db,aws,203.0.113.20\nhub_cache,aws,203.0.113.21\n' > ~/hosts.csv"
on alice $W "cat ~/hosts.csv"
on alice $W "lore resource import --file ~/hosts.csv --kind host --dry-run"
on alice $W "lore resource import --file ~/hosts.csv --kind host --yes"

note "import refused"
quiet alice $W "printf 'name,provider,ip\nhub_db,aws,203.0.113.30\nhub_mail,linode,203.0.113.40\n' > ~/hosts2.csv"
on! alice $W "lore resource import --file ~/hosts2.csv --kind host --upsert-by name --yes"
on alice $W "lore resource import --file ~/hosts2.csv --kind host --upsert-by name --dry-run"
on alice $W "lore resource import --file ~/hosts2.csv --kind host --upsert-by name --skip-errors --yes"

note "export"
on alice $W "lore resource export --kind host --format csv"
on alice $W "lore resource export --kind service --format yaml --where runs_on=hub_prod"

note "repoint"
on alice $W "lore resource repoint --old hub_prod --new hub_prod_new --dry-run"
on alice $W "lore resource repoint --old hub_prod --new hub_prod_new --yes"
on! alice $W "lore resource add --kind service --name hub_admin --set runs_on=hub_prod"
on alice $W "lore resource list --kind host --archived"

note "merge"
on alice $W "lore resource edit --id hub_db --unset ip"
on alice $W "lore resource merge --id hub_cache --into hub_db --dry-run"
on alice $W "lore resource merge --id hub_cache --into hub_db --yes"

note "archive and delete"
on alice $W "lore resource archive --id hub_stage"
on! alice $W "lore resource delete --id hub_db"
on alice $W "lore resource delete --id hub_prod_new --with-dependents --dry-run"
on alice $W "lore resource delete --id hub_db --yes"

note "fields"
on alice $W "lore resource field edit --kind host --field provider --add-value linode"
on alice $W "lore resource field add --kind host --field region --type text"
on alice $W "lore resource edit --id hub_prod_new --set region=eu-1"
on! alice $W "lore resource field retype --kind host --field region --type number --dry-run"
on alice $W "lore resource field rename --kind host --field os --new-name os_image"
on! alice $W "lore resource field remove --kind host --field region"
on alice $W "lore resource field remove --kind host --field region --drop --yes"
on alice $W "lore resource field add --kind host --field renews --type date --remind-before 30d"

note "kind edit and rename"
on alice $W "lore resource kind edit --id host --description 'a server or VM we run'"
on alice $W "lore resource kind rename --id host --new-name server"
on alice $W "lore resource kinds"

note "secret value"
on alice $W "lore resource field add --kind server --field access_key --type text"
on! alice $W "lore resource edit --id hub_prod_new --set access_key=AKIA''IOSFODNN7EXAMPLE"
on alice $W "lore resource edit --id hub_prod_new --set access_key=AKIA''IOSFODNN7EXAMPLE --secret-ref gopass://acme/hub/aws-key --yes"
on alice $W "lore resource show --id hub_prod_new"

note "secret ref field"
on alice $W "lore resource add --kind service --name hub_jobs --set runs_on=hub_prod_new --set db_password=acme/hub/jobs-db"

note "sensitive"
on alice $W "lore resource field add --kind server --field root_user --type text --sensitive"
on alice $W "lore resource edit --id hub_prod_new --set root_user=deploy"
on alice $W "lore resource list --kind server"
on! alice $W "lore resource show --id hub_prod_new --show-sensitive"
on alice $W "lore resource show --id hub_prod_new --show-sensitive --yes"

note "ambiguous"
on alice $W "lore resource add --kind service --name hub_prod_new --set runs_on=hub_prod_new"
on! alice $W "lore resource show --id hub_prod_new"
on alice $W "lore resource show --id server/hub_prod_new | head -1"
on alice $W "lore resource show --id repo:service/hub_prod_new | head -1"

note "lint and report"
on alice $W "lore resource edit --kind server --where provider=hetzner --set renews=2026-11-20 --yes"
on alice $W "lore resource lint"
on alice $W "lore resource report"

note "files"
on alice $W "git status --short .lore/data"

note "adopt"
on alice $W "lore store init --name infra --url file://\$HOME/stores/infra"
on! alice $W "lore resource adopt --from repo --to infra"
on alice $W "lore resource adopt --from repo --to infra --yes"
on alice $W "lore resource kinds"
on alice $W "lore resource rehome"

# Try it: the page's short recipe in a fresh repo.
lore_repo alice client/shop
note "try it"
on alice work/shop "lore resource kind add --kind host --description 'a server we run'"
on alice work/shop "lore resource field add --kind host --field provider --type text --required --values hetzner,aws"
on alice work/shop "lore resource kind add --kind service --description 'an app we deploy'"
on alice work/shop "lore resource field add --kind service --field runs_on --type ref --ref-kind host --required"
on alice work/shop "lore resource add --kind host --name shop_prod --set provider=hetzner"
on alice work/shop "lore resource add --kind service --name shop_api --set runs_on=shop_prod"
on alice work/shop "lore resource impact --id shop_prod"
