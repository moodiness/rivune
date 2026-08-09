#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${JFCOMPAT_UPSTREAM_URL:-http://127.0.0.1:18096}"

if [[ -z "${JFCOMPAT_UPSTREAM_USERNAME:-}" || -z "${JFCOMPAT_UPSTREAM_PASSWORD:-}" ]]; then
  echo "JFCOMPAT_UPSTREAM_USERNAME and JFCOMPAT_UPSTREAM_PASSWORD are required" >&2
  exit 1
fi
if [[ ! "${BASE_URL}" =~ ^https?://[^/?#]+$ ]]; then
  echo "JFCOMPAT_UPSTREAM_URL must be an HTTP(S) origin without credentials, path, query, or fragment" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
  echo "curl and jq are required" >&2
  exit 1
fi

umask 077
RESPONSE_FILE="$(mktemp "${TMPDIR:-/tmp}/rivune-jellyfin-response.XXXXXX")"
REQUEST_FILE="$(mktemp "${TMPDIR:-/tmp}/rivune-jellyfin-request.XXXXXX")"
AUTH_CONFIG="$(mktemp "${TMPDIR:-/tmp}/rivune-jellyfin-auth.XXXXXX")"
cleanup() {
  : >"${RESPONSE_FILE}"
  : >"${REQUEST_FILE}"
  : >"${AUTH_CONFIG}"
  rm -f "${RESPONSE_FILE}" "${REQUEST_FILE}" "${AUTH_CONFIG}"
}
trap cleanup EXIT

AUTH_READY=0
request() {
  local method="$1"
  local path="$2"
  local with_body="$3"
  local with_auth="$4"
  local status
  local -a args

  args=(
    --silent
    --show-error
    --request "${method}"
    --url "${BASE_URL}${path}"
    --output "${RESPONSE_FILE}"
    --write-out '%{http_code}'
    --connect-timeout 5
    --max-time 45
    --header 'Accept: application/json'
    --header 'X-Emby-Authorization: MediaBrowser Client="Rivune Compatibility Harness", Device="Differential Runner", DeviceId="rivune-jellyfin-compat", Version="1.0"'
  )
  if (( with_auth )); then
    if (( ! AUTH_READY )); then
      echo "Internal error: authenticated request made before login" >&2
      exit 1
    fi
    args+=(--config "${AUTH_CONFIG}")
  fi
  if (( with_body )); then
    args+=(--header 'Content-Type: application/json' --data-binary "@${REQUEST_FILE}")
  fi

  status="$(curl "${args[@]}")"
  printf '%s\n' "${status}"
}

require_status() {
  local actual="$1"
  shift
  local expected
  for expected in "$@"; do
    if [[ "${actual}" == "${expected}" ]]; then
      return 0
    fi
  done
  printf 'Jellyfin bootstrap request returned HTTP %s; expected %s\n' "${actual}" "$*" >&2
  exit 1
}

status="$(request GET /System/Info/Public 0 0)"
require_status "${status}" 200
if ! jq -e '.StartupWizardCompleted == false' "${RESPONSE_FILE}" >/dev/null; then
  echo "Refusing to bootstrap an upstream instance that is not fresh" >&2
  exit 1
fi

jq -n '{ServerName:"Jellyfin 10.11.11 Oracle",UICulture:"en-US",MetadataCountryCode:"US",PreferredMetadataLanguage:"en"}' >"${REQUEST_FILE}"
status="$(request POST /Startup/Configuration 1 0)"
require_status "${status}" 204

status="$(request GET /Startup/User 0 0)"
require_status "${status}" 200

jq -n '{Name:env.JFCOMPAT_UPSTREAM_USERNAME,Password:env.JFCOMPAT_UPSTREAM_PASSWORD}' >"${REQUEST_FILE}"
status="$(request POST /Startup/User 1 0)"
require_status "${status}" 204
: >"${REQUEST_FILE}"

status="$(request POST /Startup/Complete 0 0)"
require_status "${status}" 204

jq -n '{Username:env.JFCOMPAT_UPSTREAM_USERNAME,Pw:env.JFCOMPAT_UPSTREAM_PASSWORD}' >"${REQUEST_FILE}"
unset JFCOMPAT_UPSTREAM_USERNAME JFCOMPAT_UPSTREAM_PASSWORD
status="$(request POST /Users/AuthenticateByName 1 0)"
require_status "${status}" 200
ACCESS_TOKEN="$(jq -er '.AccessToken | select(type == "string" and length > 0)' "${RESPONSE_FILE}")"
if [[ ! "${ACCESS_TOKEN}" =~ ^[A-Za-z0-9._~-]+$ ]]; then
  echo "Jellyfin returned an unsafe access-token representation" >&2
  exit 1
fi
printf 'header = "X-Emby-Token: %s"\n' "${ACCESS_TOKEN}" >"${AUTH_CONFIG}"
AUTH_READY=1
: >"${RESPONSE_FILE}"
: >"${REQUEST_FILE}"
unset ACCESS_TOKEN

printf '{}\n' >"${REQUEST_FILE}"
status="$(request POST '/Library/VirtualFolders?name=Movies&collectionType=movies&paths=%2Fmedia&refreshLibrary=false' 1 1)"
require_status "${status}" 204

status="$(request GET /ScheduledTasks 0 1)"
require_status "${status}" 200
TASK_ID="$(jq -er '[.[] | select(.Key == "RefreshLibrary") | .Id][0]' "${RESPONSE_FILE}")"
if [[ -z "${TASK_ID}" ]]; then
  echo "Jellyfin did not expose its RefreshLibrary scheduled task" >&2
  exit 1
fi
INITIAL_END="$(jq -r '[.[] | select(.Key == "RefreshLibrary") | .LastExecutionResult.EndTimeUtc // ""][0]' "${RESPONSE_FILE}")"

if [[ ! "${TASK_ID}" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "Jellyfin returned an unsafe scheduled-task identifier" >&2
  exit 1
fi
status="$(request POST "/ScheduledTasks/Running/${TASK_ID}" 0 1)"
require_status "${status}" 204

# Poll the task state with a bounded incremental backoff. Completion is tied to
# a new task result, not to an assumed scan duration.
deadline=$((SECONDS + 240))
delay=1
while true; do
  status="$(request GET /ScheduledTasks 0 1)"
  require_status "${status}" 200
  task_state="$(jq -er --arg id "${TASK_ID}" '.[] | select(.Id == $id) | .State' "${RESPONSE_FILE}")"
  task_end="$(jq -r --arg id "${TASK_ID}" '.[] | select(.Id == $id) | .LastExecutionResult.EndTimeUtc // ""' "${RESPONSE_FILE}")"
  task_result="$(jq -r --arg id "${TASK_ID}" '.[] | select(.Id == $id) | .LastExecutionResult.Status // ""' "${RESPONSE_FILE}")"

  if [[ "${task_state}" == "Idle" && -n "${task_end}" && "${task_end}" != "${INITIAL_END}" ]]; then
    if [[ "${task_result}" != "Completed" ]]; then
      echo "Jellyfin library scan did not complete successfully" >&2
      exit 1
    fi
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "Timed out while polling the Jellyfin library scan" >&2
    exit 1
  fi
  sleep "${delay}"
  if (( delay < 5 )); then
    delay=$((delay + 1))
  fi
done

status="$(request GET '/Items?Recursive=true&IncludeItemTypes=Movie&SearchTerm=Rivune%20Demo&Limit=1' 0 1)"
require_status "${status}" 200
if ! jq -e '.Items | type == "array" and length >= 1' "${RESPONSE_FILE}" >/dev/null; then
  echo "The completed Jellyfin scan did not index the Rivune Demo fixture" >&2
  exit 1
fi

printf 'Bootstrapped fresh Jellyfin 10.11.11 oracle and indexed the synthetic fixture\n'
