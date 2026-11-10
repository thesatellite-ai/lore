#!/usr/bin/env bash
# CH-2: Clock skew — an edit always wins over the version it replaces
# Catches: R16-10, R27-29 (0.1.18: every row file carries a hybrid logical
# clock stamp "_hlc": "<wall>/<counter>/<actor>"; an edit must be stamped
# above the row's current stamp whatever the wall clock says, or a merge
# would keep the older text)
#
# The skew: the row was last written on a machine whose clock ran an hour
# ahead (its stamp is in our future); this machine then edits it.
source "$(dirname "$0")/../lib/common.sh"
need python3
mk_tmp; init_project ch2

$LORE memory add --body "first" >/dev/null || fail "first add"
F=$(find .lore/data/memories -name 'mem_*.json' | head -1)
[ -n "$F" ] || fail "no row file"
ID=$(basename "$F" .json)
AHEAD=$(python3 -c 'import datetime as d; print((d.datetime.now(d.timezone.utc)+d.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%S.%f")[:23]+"Z")')
python3 - "$F" "$AHEAD" <<'PY' || fail "set the stamp ahead"
import json, sys
p, ahead = sys.argv[1], sys.argv[2]
d = json.load(open(p))
wall, rest = d["_hlc"].split("/", 1)
d["_hlc"] = ahead + "/" + rest
json.dump(d, open(p, "w"), indent=2)
PY

$LORE memory edit "$ID" --body "first, edited behind the skew" >/dev/null || fail "edit"
python3 - "$F" "$AHEAD" <<'PY' || fail "the edit is not stamped above the skewed version"
import json, sys
p, ahead = sys.argv[1], sys.argv[2]
h = json.load(open(p))["_hlc"]
wall, counter, _ = h.split("/", 2)
# Same zero-padded format: strings compare as time, then counter.
if (wall, counter) <= (ahead, "0000"):
    raise SystemExit(f"edit stamp {h} is not above {ahead}/0000")
PY
BODY=$($LORE memory list --json | json_get '",".join(r["body"] for r in d["data"])')
[ "$BODY" = "first, edited behind the skew" ] || fail "lore shows the older text: $BODY"
pass CH-2
