#!/usr/bin/env bash
set -euo pipefail

LABEL=io.rivune.discovery
DOMAIN="gui/$(id -u)"
AGENT_DIRECTORY="${HOME:?HOME is required}/Library/LaunchAgents"
LOG_DIRECTORY="${HOME}/Library/Logs/Rivune"
PLIST_FILE="${AGENT_DIRECTORY}/${LABEL}.plist"
LOG_FILE="${LOG_DIRECTORY}/discovery.log"

usage() {
  printf 'Usage: %s start ORIGIN NAME VERSION | stop | status | logs\n' "$0" >&2
  exit 64
}

xml_escape() {
  local value="$1"
  value="${value//&/\&amp;}"
  value="${value//</\&lt;}"
  value="${value//>/\&gt;}"
  value="${value//\"/\&quot;}"
  value="${value//\'/\&apos;}"
  printf '%s' "${value}"
}

stop_agent() {
  launchctl bootout "${DOMAIN}/${LABEL}" >/dev/null 2>&1 || true
  rm -f "${PLIST_FILE}"
}

start_agent() {
  (( $# == 3 )) || usage
  local origin="$1" name="$2" version="$3" authority port
  local escaped_origin escaped_name escaped_version escaped_log temporary=""

  [[ "${origin}" =~ ^https?://[^[:space:]/?#]+/?$ ]] || {
    printf 'RIVUNE_DISCOVERY_URL is not an HTTP(S) origin\n' >&2
    exit 1
  }
  [[ -n "${name}" && ${#name} -le 63 && "${name}" != *$'\n'* && "${name}" != *$'\r'* ]] || {
    printf 'RIVUNE_DISCOVERY_NAME is invalid\n' >&2
    exit 1
  }
  [[ "${version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
    printf 'RIVUNE_VERSION is not a stable numeric version\n' >&2
    exit 1
  }
  command -v launchctl >/dev/null 2>&1 || {
    printf 'launchctl is required for macOS LAN discovery\n' >&2
    exit 1
  }
  [[ -x /usr/bin/dns-sd ]] || {
    printf 'dns-sd is required for macOS LAN discovery\n' >&2
    exit 1
  }

  authority="${origin#*://}"
  authority="${authority%/}"
  if [[ "${authority}" =~ ^\[[^]]+\]:([0-9]+)$ || "${authority}" =~ ^[^:]+:([0-9]+)$ ]]; then
    port="${BASH_REMATCH[1]}"
  elif [[ "${origin}" == https://* ]]; then
    port=443
  else
    port=80
  fi
  (( 10#${port} >= 1 && 10#${port} <= 65535 )) || {
    printf 'RIVUNE_DISCOVERY_URL contains an invalid port\n' >&2
    exit 1
  }

  umask 077
  mkdir -p "${AGENT_DIRECTORY}" "${LOG_DIRECTORY}"
  chmod 700 "${LOG_DIRECTORY}"
  temporary="$(mktemp "${PLIST_FILE}.XXXXXXXX")"
  cleanup() {
    [[ -z "${temporary}" ]] || rm -f "${temporary}"
  }
  trap cleanup EXIT HUP INT QUIT TERM

  escaped_origin="$(xml_escape "${origin%/}")"
  escaped_name="$(xml_escape "${name}")"
  escaped_version="$(xml_escape "${version}")"
  escaped_log="$(xml_escape "${LOG_FILE}")"
  cat > "${temporary}" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/dns-sd</string>
    <string>-R</string>
    <string>${escaped_name}</string>
    <string>_rivune._tcp</string>
    <string>local.</string>
    <string>${port}</string>
    <string>url=${escaped_origin}</string>
    <string>protocol=20</string>
    <string>version=${escaped_version}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>${escaped_log}</string>
  <key>StandardErrorPath</key>
  <string>${escaped_log}</string>
  <key>ThrottleInterval</key>
  <integer>5</integer>
</dict>
</plist>
PLIST
  chmod 600 "${temporary}"

  launchctl bootout "${DOMAIN}/${LABEL}" >/dev/null 2>&1 || true
  mv -f "${temporary}" "${PLIST_FILE}"
  temporary=""
  if ! launchctl bootstrap "${DOMAIN}" "${PLIST_FILE}"; then
    rm -f "${PLIST_FILE}"
    exit 1
  fi
  launchctl kickstart -k "${DOMAIN}/${LABEL}"
  trap - EXIT HUP INT QUIT TERM
}

command_name="${1:-}"
(( $# > 0 )) || usage
shift
case "${command_name}" in
  start) start_agent "$@" ;;
  stop)
    (( $# == 0 )) || usage
    stop_agent
    ;;
  status)
    (( $# == 0 )) || usage
    launchctl print "${DOMAIN}/${LABEL}"
    ;;
  logs)
    (( $# == 0 )) || usage
    [[ -f "${LOG_FILE}" ]] && cat "${LOG_FILE}"
    ;;
  *) usage ;;
esac
