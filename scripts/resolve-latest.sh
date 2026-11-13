#!/usr/bin/env bash
# Resolve the newest CrashPlan Linux agent offered by the CrashPlan console.
#
# Prints KEY=VALUE lines (CRASHPLAN_VERSION, CRASHPLAN_BUILD, CRASHPLAN_URL).
# With --sha256 the archive is downloaded and CRASHPLAN_SHA256 is added.
set -euo pipefail

CONSOLE_URL="${CRASHPLAN_CONSOLE_URL:-https://console.us2.crashplan.com/api/v3/download/agent/linux}"

location="$(curl -fsS -o /dev/null -w '%{redirect_url}' "$CONSOLE_URL")"
if [[ -z "$location" ]]; then
    echo "ERROR: $CONSOLE_URL did not redirect to an installer" >&2
    exit 1
fi

# Expected: https://download.crashplan.com/installs/agent/cloud/<ver>/<build>/install/CrashPlan_<ver>_<build>_Linux.tgz
re='^https://download\.crashplan\.com/installs/agent/cloud/([0-9]+(\.[0-9]+)+)/([0-9]+)/install/CrashPlan_[0-9.]+_[0-9]+_Linux\.tgz$'
if [[ ! "$location" =~ $re ]]; then
    echo "ERROR: unexpected installer URL: $location" >&2
    exit 1
fi

echo "CRASHPLAN_VERSION=${BASH_REMATCH[1]}"
echo "CRASHPLAN_BUILD=${BASH_REMATCH[3]}"
echo "CRASHPLAN_URL=$location"

if [[ "${1:-}" == "--sha256" ]]; then
    sum="$(curl -fsSL "$location" | sha256sum | cut -d' ' -f1)"
    echo "CRASHPLAN_SHA256=$sum"
fi
