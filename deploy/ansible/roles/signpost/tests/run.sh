#!/usr/bin/env bash
# Tests the role's node detection against fixtures. Touches nothing outside a temp dir.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../../../../.." && pwd)"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
export ANSIBLE_LOCALHOST_WARNING=False ANSIBLE_INVENTORY_UNPARSED_WARNING=False

for fixture in full sparse; do
  mkdir -p "$out/$fixture"
  ansible-playbook -i "$here/inventory-$fixture.ini" "$here/detect.yml" \
    -e "fixture=$fixture" -e "out=$out/$fixture" >"$out/$fixture.log" 2>&1 || { cat "$out/$fixture.log"; exit 1; }
  # The app must accept what the role rendered, and find the fixture's apps.
  (cd "$repo" && go build -o "$out/signpost" ./cmd/signpost)
  "$out/signpost" -config "$out/$fixture/config.json" -listen 127.0.0.1:18089 >"$out/$fixture.run" 2>&1 &
  pid=$!
  for _ in $(seq 50); do
    curl -fsS -H 'X-Forwarded-Groups: PLATFORM-ADMIN' http://127.0.0.1:18089/api/status >"$out/$fixture/status.json" 2>/dev/null && break
    sleep 0.1
  done
  kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true
  grep -q '"signpost listening"' "$out/$fixture.run" || { cat "$out/$fixture.run"; exit 1; }
  python3 "$here/check.py" "$out/$fixture" "$fixture"
done
