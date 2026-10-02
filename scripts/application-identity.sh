# shellcheck shell=bash

# Read the generated distribution identity without evaluating its contents.
application_identity() {
  local manifest
  manifest="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/contract/application-identity.json"
  python3 -c '
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    identity = json.load(source)
if identity.get("version") != 1:
    raise SystemExit("Unsupported application identity version")
value = identity[sys.argv[2]]
if isinstance(value, bool):
    print(str(value).lower())
elif isinstance(value, str) and value:
    print(value)
else:
    raise SystemExit("Invalid application identity field: " + sys.argv[2])
' "$manifest" "$1"
}
