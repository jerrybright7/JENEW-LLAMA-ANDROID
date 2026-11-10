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
import re

# Anchor to the standalone http.Client.Timeout field. A substring search also
# matches TLSHandshakeTimeout, which must remain enabled.
pattern = re.compile(r"(?m)^(\\s*)Timeout:\\s*10\\s*\\*\\s*time\\.Second,\\s*$")
source, count = pattern.subn(
    r"\\1Timeout:       0, // JENEW: disable only the overall HTTP request timeout",
    source,
    count=1,
)
if count != 1:
    raise SystemExit(
        f"Expected one standalone http.Client.Timeout field, found {count}; "
        "refusing to apply an uncertain patch."
    )
path.write_text(source)
PY

go mod edit -replace=github.com/openziti/sdk-golang=./"$PATCHED_DIR"

grep -n 'Timeout:' "$PATCHED_DIR/edge-apis/component.go"
echo "Prepared OpenZiti SDK v1.2.8 with no overall HTTP client timeout."
