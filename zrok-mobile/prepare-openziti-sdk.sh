#!/usr/bin/env bash
set -euo pipefail

# OpenZiti SDK v1.2.8 hard-codes a 10-second overall HTTP client timeout.
# JENEW needs controller requests to wait for the network/controller response;
# transport-level TLS handshake limits remain in place.
SDK_DIR="$(go mod download -json github.com/openziti/sdk-golang@v1.2.8 | python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')"
PATCHED_DIR=".jenew-sdk-golang-patched"

test -f "$SDK_DIR/edge-apis/component.go"
rm -rf "$PATCHED_DIR"
cp -a "$SDK_DIR" "$PATCHED_DIR"
chmod -R u+w "$PATCHED_DIR"

python3 - "$PATCHED_DIR/edge-apis/component.go" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
source = path.read_text()
old = "Timeout:       10 * time.Second,"
new = "Timeout:       0, // JENEW: disable the overall HTTP request timeout"
count = source.count(old)
if count != 1:
    raise SystemExit(
        f"Expected exactly one OpenZiti HTTP timeout assignment, found {count}; "
        "refusing to apply an uncertain patch."
    )
path.write_text(source.replace(old, new, 1))
PY

go mod edit -replace=github.com/openziti/sdk-golang=./"$PATCHED_DIR"

grep -n 'Timeout:' "$PATCHED_DIR/edge-apis/component.go"
echo "Prepared OpenZiti SDK v1.2.8 with no overall HTTP client timeout."
