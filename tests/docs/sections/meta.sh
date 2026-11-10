# meta: variables (lore vars) and metadata on rows (lore meta)
# (docs/knowledge/variables-and-metadata.md).

lore_repo alice client/website
W=work/website

# --- variables ------------------------------------------------------------------
note "vars set"
on alice $W "lore vars set api_host api.example.com --type dns --description 'The shop API the website reads'"
on alice $W "lore vars set cache_minutes 5 --type number"
on alice $W "lore vars list"
on alice $W "ls .lore/data/vars | wc -l"

note "vars use"
on alice $W "lore memory add --body 'Products come from \${var:api_host} and are cached for \${var:cache_minutes} minutes'"
local m1; m1=$(id mem)
on alice $W "lore show $m1"
on alice $W "lore show $m1 --expand"

note "vars change"
on alice $W "lore vars set cache_minutes 10"
on alice $W "lore vars show cache_minutes"
on alice $W "lore show $m1 --expand"

note "vars rename"
on alice $W "lore vars rename api_host shop_api_host"
on alice $W "lore vars list"
on alice $W "lore show $m1 --expand"
on alice $W "lore vars where shop_api_host"

note "vars type"
on! alice $W "lore vars set support_email not-an-email --type email"

note "vars rm"
on alice $W "lore vars rm cache_minutes --yes"
on alice $W "lore vars list"
on alice $W "lore show $m1 --expand"

# --- metadata -------------------------------------------------------------------
note "meta set"
on alice $W "lore task add 'Fix the Safari checkout button'"
local t1; t1=$(id tsk)
on alice $W "lore meta set $t1 client=acme"
on alice $W "lore meta set $t1 cost_hours:number=3"
on alice $W "lore meta get $t1"

note "meta keys"
on alice $W "lore meta keys"

note "meta add"
on alice $W "lore meta add priority_band --type text --strict --values low,normal,urgent --description 'How fast the client needs it'"
on alice $W "lore meta set $t1 priority_band=urgent"
on! alice $W "lore meta set $t1 priority_band=asap"

note "meta where"
on alice $W "lore task add 'Update the footer links' --meta client=acme && lore task add 'Add dark mode' --meta client=globex"
on alice $W "lore meta set --where client=acme billing=monthly --dry-run"
on alice $W "lore meta set --where client=acme billing=monthly --yes"
on alice $W "lore meta export --key client,billing"

note "meta edit"
on alice $W "lore meta edit priority_band --rename-value urgent=asap"
on alice $W "lore meta get $t1 priority_band"
on alice $W "lore meta keys --key priority_band"

note "meta rename"
on alice $W "lore meta rename client customer --rewrite-rows"
on alice $W "lore meta get $t1"

note "meta unset"
on alice $W "lore meta unset $t1 cost_hours"
on alice $W "lore meta get $t1"

note "meta lint"
on alice $W "lore meta lint"

note "meta history"
quiet alice $W "git add -A && git commit -qm 'lore: metadata'"
on alice $W "lore meta history $t1"
