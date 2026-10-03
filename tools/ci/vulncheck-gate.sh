#!/usr/bin/env bash
# Gate on a govulncheck JSON report: fail when our code CALLS a vulnerable symbol,
# unless that vulnerability is listed in the allowlist with a reason.
#
#   govulncheck -format json ./... > report.json
#   tools/ci/vulncheck-gate.sh report.json .govulncheck-allow
#
# A finding is "called" when the first frame of its trace names a function
# (govulncheck's symbol-level result). Allowlist lines: "<OSV id>  # reason".
set -euo pipefail

report=${1:?usage: vulncheck-gate.sh <report.json> [allowlist]}
allow=${2:-}
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

called=$(jq -r 'select(.finding != null and .finding.trace[0].function != null) | .finding.osv' "$report" | sort -u)
allowed=""
if [ -n "$allow" ] && [ -f "$allow" ]; then
  allowed=$(sed -e 's/#.*//' -e 's/[[:space:]]//g' "$allow" | grep -v '^$' | sort -u || true)
fi

failed=0
for id in $called; do
  if printf '%s\n' "$allowed" | grep -qx "$id"; then
    echo "allowed (reviewed): $id"
  else
    echo "VULNERABLE (called): $id  https://pkg.go.dev/vuln/$id"
    failed=1
  fi
done
for id in $allowed; do
  printf '%s\n' "$called" | grep -qx "$id" || echo "note: allowlisted $id is no longer reported; remove it from $allow"
done
exit "$failed"
