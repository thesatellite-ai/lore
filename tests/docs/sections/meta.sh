# meta: variables (lore vars) and metadata on rows (lore meta)
# (docs/knowledge/variables-and-metadata.md).

lore_repo alice client/website
W=work/website

# --- variables ------------------------------------------------------------------
note "vars set"
on alice $W "lore vars set --name api_host --value api.example.com --type dns --description 'The shop API the website reads'"
on alice $W "lore vars set --name cache_minutes --value 5 --type number"
on alice $W "lore vars list"
on alice $W "ls .lore/data/vars | wc -l"

note "vars use"
on alice $W "lore memory add --body 'Products come from \${var:api_host} and are cached for \${var:cache_minutes} minutes'"
local m1; m1=$(id mem)
on alice $W "lore show --id $m1"
on alice $W "lore show --id $m1 --expand"

note "vars change"
on alice $W "lore vars set --name cache_minutes --value 10"
on alice $W "lore vars show --id cache_minutes"
on alice $W "lore show --id $m1 --expand"

note "vars rename"
on alice $W "lore vars rename --id api_host --new-name shop_api_host"
on alice $W "lore vars list"
on alice $W "lore show --id $m1 --expand"
on alice $W "lore vars where --id shop_api_host"

note "vars type"
on! alice $W "lore vars set --name support_email --value not-an-email --type email"

note "vars rm"
on alice $W "lore vars rm --id cache_minutes --yes"
on alice $W "lore vars list"
on alice $W "lore show --id $m1 --expand"

# --- metadata -------------------------------------------------------------------
note "meta set"
on alice $W "lore task add --title 'Fix the Safari checkout button'"
local t1; t1=$(id tsk)
on alice $W "lore meta set --id $t1 --pair client=acme"
on alice $W "lore meta set --id $t1 --pair cost_hours:number=3"
on alice $W "lore meta get --id $t1"

note "meta keys"
on alice $W "lore meta keys"

note "meta add"
on alice $W "lore meta add --name priority_band --type text --strict --values low,normal,urgent --description 'How fast the client needs it'"
on alice $W "lore meta set --id $t1 --pair priority_band=urgent"
on! alice $W "lore meta set --id $t1 --pair priority_band=asap"

note "meta where"
on alice $W "lore task add --title 'Update the footer links' --meta client=acme && lore task add --title 'Add dark mode' --meta client=globex"
on alice $W "lore meta set --where client=acme --id billing=monthly --dry-run"
on alice $W "lore meta set --where client=acme --id billing=monthly --yes"
on alice $W "lore meta export --key client,billing"

note "meta edit"
on alice $W "lore meta edit --id priority_band --rename-value urgent=asap"
on alice $W "lore meta get --id $t1 --key priority_band"
on alice $W "lore meta keys --key priority_band"

note "meta rename"
on alice $W "lore meta rename --id client --new-name customer --rewrite-rows"
on alice $W "lore meta get --id $t1"

note "meta unset"
on alice $W "lore meta unset --id $t1 --key cost_hours"
on alice $W "lore meta get --id $t1"

note "meta lint"
on alice $W "lore meta lint"

note "meta history"
quiet alice $W "git add -A && git commit -qm 'lore: metadata'"
on alice $W "lore meta history --id $t1"
