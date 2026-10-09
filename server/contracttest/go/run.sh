#!/usr/bin/env bash
# Runs the WebSocket contract suite against the Go backend.
#   1. migrate + reseed the dev database (DESTROYS its data)
#   2. start `quack serve` on 127.0.0.1:$CONTRACT_PORT
#   3. go test -tags contract ./contracttest/...
# Needs `task infra:up`. Uses its own database (CONTRACT_DATABASE_URL, default
# quack_contract on the dev Postgres) so the dev database is never wiped.
# Env: QUACK_KAFKA_BROKERS,
# CONTRACT_PORT (default 8766), CONTRACT_KNOWN_BUGS (default: none), GO_TEST_FLAGS,
# QUACK_BUILD_FLAGS (e.g. "-race" to run the server under the race detector),
# CONTRACT_TOPIC_PREFIX (default "contract."),
# CONTRACT_BIN (where to build the server; default bin/quack-contract, so a
# platform running from bin/quack is never overwritten). The history store
# comes from QUACK_HISTORY_DRIVER / QUACK_HISTORY_URL as usual.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER="$(cd "$HERE/../.." && pwd)"
PORT="${CONTRACT_PORT:-8766}"
BIN="${CONTRACT_BIN:-$SERVER/bin/quack-contract}"
export QUACK_DATABASE_URL="${CONTRACT_DATABASE_URL:-postgres://quack:quack@127.0.0.1:5433/quack_contract?sslmode=disable}"
# Own topics too, so a dev stack on the same Redpanda never sees contract devices.
export QUACK_TOPIC_PREFIX="${CONTRACT_TOPIC_PREFIX:-contract.}"
WORK="$(mktemp -d)"
trap 'kill "${PID:-}" 2>/dev/null || true; wait "${PID:-}" 2>/dev/null || true; rm -rf "$WORK"' EXIT

(cd "$SERVER" && go build ${QUACK_BUILD_FLAGS:-} -o "$BIN" ./cmd/quack)
"$BIN" migrate
"$BIN" dev seed --out "$WORK/fixture.json" --ws-base "ws://127.0.0.1:$PORT" --origin "http://127.0.0.1:$PORT"

cat > "$WORK/hook.sh" <<HOOK
#!/usr/bin/env bash
exec "$BIN" dev hook "\$@"
HOOK
chmod +x "$WORK/hook.sh"

QUACK_HTTP_ADDR="127.0.0.1:$PORT" QUACK_WEB_DIR="" QUACK_GATEWAY_ID="gw-contract" \
  QUACK_PRESENCE_TTL=6s QUACK_PRESENCE_HEARTBEAT=2s \
  "$BIN" serve >"$WORK/serve.log" 2>&1 &
PID=$!
for _ in $(seq 1 100); do
  curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done
sleep 1 # let the bus consumer reach the end offsets

export CONTRACT_FIXTURE="$WORK/fixture.json" CONTRACT_HOOK="$WORK/hook.sh"
export CONTRACT_KNOWN_BUGS="${CONTRACT_KNOWN_BUGS:-}"
status=0
(cd "$SERVER" && go test -tags contract -count=1 ./contracttest/... -v ${GO_TEST_FLAGS:-}) || status=$?
if grep -q "DATA RACE" "$WORK/serve.log"; then
  echo "---- DATA RACE detected in quack serve ----"; grep -A 30 "DATA RACE" "$WORK/serve.log" | head -120
  status=1
fi
if [[ $status -ne 0 ]]; then
  echo "---- quack serve log (tail) ----"; tail -n 80 "$WORK/serve.log"
fi
exit $status
