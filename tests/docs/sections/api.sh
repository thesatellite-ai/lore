# api: the local REST API of docs/reference/rest-api.md.
# Alice (an agency developer) serves `lore api` from the client's website repo, calls it
# with curl and her token, rotates the token, then stops the server. The server listens
# on a free port of 127.0.0.1 only, and runs under `timeout`, so it can never outlive
# this section even when a step fails; the section stops it itself at the end.

lore_repo alice client/website

# A free port on 127.0.0.1, so two runs (or another program) never clash. The commands
# below name it $PORT, and the transcript shows it as $PORT too.
local api_port
api_port=$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
local api_env="PORT=$api_port"

# api_start / api_stop: start the server in the background (hidden: a shell job is not
# part of the example) and wait until it answers; stop it and wait until it is gone.
api_start() {
  EXTRA_ENV="$api_env" quiet alice work/website 'timeout 120 lore api --port $PORT > ~/api.log 2>&1 < /dev/null & echo $! > ~/api.pid'
  EXTRA_ENV="$api_env" quiet alice . 'for i in $(seq 1 50); do curl -s -o /dev/null http://127.0.0.1:$PORT/ && exit 0; sleep 0.2; done; echo "lore api did not start"; cat ~/api.log; exit 1'
}
api_stop() {
  quiet alice . 'kill $(cat ~/api.pid); for i in $(seq 1 50); do kill -0 $(cat ~/api.pid) 2>/dev/null || exit 0; sleep 0.2; done; echo "lore api did not stop"; exit 1'
}
api_start

note "api start"
EXTRA_ENV="$api_env" on alice . 'cat api.log'
on alice . 'ls -l .lore/api-token | cut -d" " -f1'

note "api no token"
EXTRA_ENV="$api_env" on alice . 'curl -s -w "\nHTTP %{http_code}\n" http://127.0.0.1:$PORT/v1/rules'

note "api calls"
EXTRA_ENV="$api_env" on alice . 'curl -s -X POST -H "Authorization: Bearer $(cat ~/.lore/api-token)" -d '"'"'{"body": "Run the tests before you push"}'"'"' -w "\nHTTP %{http_code}\n" http://127.0.0.1:$PORT/v1/rules'
EXTRA_ENV="$api_env" on alice . 'curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" http://127.0.0.1:$PORT/v1/rules'
on alice work/website "lore rule list"

note "api error"
EXTRA_ENV="$api_env" on alice . 'curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" -w "\nHTTP %{http_code}\n" "http://127.0.0.1:$PORT/v1/rules?colour=red"'
EXTRA_ENV="$api_env" on alice . 'curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" -w "\nHTTP %{http_code}\n" http://127.0.0.1:$PORT/v1/notes'

# The running server checks the token file on every request, so the rotate reaches it
# at once: no restart between the rotate and the two calls.
note "api rotate"
on alice . 'cp .lore/api-token old-token'
on alice work/website "lore api token rotate"
EXTRA_ENV="$api_env" on alice . 'curl -s -o /dev/null -w "old token: HTTP %{http_code}\n" -H "Authorization: Bearer $(cat ~/old-token)" http://127.0.0.1:$PORT/v1/rules'
EXTRA_ENV="$api_env" on alice . 'curl -s -o /dev/null -w "new token: HTTP %{http_code}\n" -H "Authorization: Bearer $(cat ~/.lore/api-token)" http://127.0.0.1:$PORT/v1/rules'

# Try it: the routes the running server has for one noun, read from its OpenAPI document.
note "try api"
EXTRA_ENV="$api_env" on alice . 'curl -s -H "Authorization: Bearer $(cat ~/.lore/api-token)" http://127.0.0.1:$PORT/openapi.json | python3 -c '"'"'import json, sys; p = json.load(sys.stdin)["paths"]; [print(" ".join(sorted(m.upper() for m in p[k])), k) for k in sorted(p) if k.startswith("/v1/rules")]'"'"''

note "api stop"
api_stop
EXTRA_ENV="$api_env" on alice . 'curl -s http://127.0.0.1:$PORT/v1/rules || echo "no answer (curl exit $?): lore api has stopped"'

note "spec"
on alice . 'lore spec openapi --out api-docs && ls api-docs'
on alice . 'lore spec openapi --out api-docs --check'
on! alice . 'echo "{}" > api-docs/openapi.json; lore spec openapi --out api-docs --check'
on! alice . 'lore spec docs --out api-docs'

# The port is random on every run: the transcript names it $PORT, as the commands do.
sed -i "s/127\.0\.0\.1:$api_port\b/127.0.0.1:\$PORT/g" "$TRANSCRIPT"
