#!/usr/bin/env bash

BACKUP_MANIFEST_MAX_BYTES=4096
BACKUP_DEFAULT_MAX_BYTES=1099511627776

backup_error() {
  printf '%s\n' "$1" >&2
  return 1
}

require_backup_token() {
  local value="$1"
  local label="$2"
  if [[ ! "${value}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; then
    backup_error "${label} is missing or invalid"
  fi
}

require_trusted_directory_ancestors() {
  local directory="$1"
  local label="$2"
  local mode owner
  local invoking_uid
  invoking_uid="$(id -u)"
  directory="$(realpath -e -- "${directory}")" || return
  while true; do
    mode="$(stat -c '%a' -- "${directory}")" || return
    owner="$(stat -c '%u' -- "${directory}")" || return
    if [[ "${owner}" != 0 && "${owner}" != "${invoking_uid}" ]]; then
      backup_error "${label} has an ancestor owned by an untrusted user"
      return
    fi
    if (( (8#${mode} & 0022) != 0 )) && \
       ! { [[ "${owner}" == 0 ]] && (( (8#${mode} & 01000) != 0 )); }; then
      backup_error "${label} has an unsafe writable ancestor"
      return
    fi
    [[ "${directory}" == / ]] && break
    directory="$(dirname "${directory}")"
  done
}

require_backup_key() {
  local key_path="$1"
  local archive_directory="$2"
  local purpose="$3"

  if [[ -z "${key_path}" ]]; then
    backup_error "${purpose} key file is required"
    return
  fi
  if [[ -L "${key_path}" || ! -f "${key_path}" || ! -r "${key_path}" ]]; then
    backup_error "Refusing missing, unreadable, or non-regular ${purpose} key"
    return
  fi

  key_path="$(realpath -e -- "${key_path}")"
  archive_directory="$(realpath -e -- "${archive_directory}")"
  local key_mode key_owner key_directory key_directory_mode key_directory_owner
  key_mode="$(stat -c '%a' -- "${key_path}")"
  key_owner="$(stat -c '%u' -- "${key_path}")"
  key_directory="$(dirname "${key_path}")"
  require_trusted_directory_ancestors "${key_directory}" "${purpose} key directory" || return
  key_directory_mode="$(stat -c '%a' -- "${key_directory}")"
  key_directory_owner="$(stat -c '%u' -- "${key_directory}")"
  if [[ "${key_owner}" != "$(id -u)" ]] || (( (8#${key_mode} & 0077) != 0 )) || \
     [[ "${key_directory_owner}" != "$(id -u)" ]] || (( (8#${key_directory_mode} & 0022) != 0 )); then
    backup_error "Refusing ${purpose} key or key directory with unsafe ownership or permissions"
    return
  fi
  case "${key_path}" in
    "${archive_directory}"|"${archive_directory}"/*)
      backup_error "Refusing ${purpose} key stored in the backup directory"
      return
      ;;
  esac

  SECURE_BACKUP_KEY_FILE="${key_path}"
}

require_backup_signature() {
  local signature_path="$1"
  if [[ -L "${signature_path}" || ! -f "${signature_path}" || ! -r "${signature_path}" ]]; then
    backup_error "Backup signature is missing, unreadable, or not a regular file"
    return
  fi
  if (( $(stat -c '%s' -- "${signature_path}") > BACKUP_MANIFEST_MAX_BYTES )); then
    backup_error "Backup signature exceeds the safety limit"
    return
  fi
  SECURE_BACKUP_SIGNATURE_FILE="$(realpath -e -- "${signature_path}")"
}

require_backup_manifest() {
  local manifest_path="$1"
  if [[ -L "${manifest_path}" || ! -f "${manifest_path}" || ! -r "${manifest_path}" ]]; then
    backup_error "Backup manifest is missing, unreadable, or not a regular file"
    return
  fi
  if (( $(stat -c '%s' -- "${manifest_path}") > BACKUP_MANIFEST_MAX_BYTES )); then
    backup_error "Backup manifest exceeds the safety limit"
    return
  fi
  SECURE_BACKUP_MANIFEST_FILE="$(realpath -e -- "${manifest_path}")"
}

backup_archive_limit() {
  local limit="${RIVUNE_BACKUP_MAX_BYTES:-${BACKUP_DEFAULT_MAX_BYTES}}"
  if [[ ! "${limit}" =~ ^[1-9][0-9]{0,17}$ ]]; then
    backup_error "RIVUNE_BACKUP_MAX_BYTES is invalid"
    return
  fi
  printf '%s' "${limit}"
}

backup_legacy_archive_limit() {
  local limit="${RIVUNE_BACKUP_LEGACY_MAX_BYTES:-}"
  local maximum
  maximum="$(backup_archive_limit)" || return
  if [[ ! "${limit}" =~ ^[1-9][0-9]{0,17}$ ]] || (( limit > maximum )); then
    backup_error "RIVUNE_BACKUP_LEGACY_MAX_BYTES is required, must be valid, and must not exceed RIVUNE_BACKUP_MAX_BYTES"
    return
  fi
  printf '%s' "${limit}"
}

require_repository_archive() {
  local archive_path="$1"
  if [[ -L "${archive_path}" || ! -f "${archive_path}" || ! -r "${archive_path}" ]]; then
    backup_error "Backup is missing, unreadable, or not a regular file"
    return
  fi
}

require_bounded_archive() {
  local archive_path="$1"
  local limit
  limit="$(backup_archive_limit)" || return
  require_repository_archive "${archive_path}" || return
  local size
  size="$(stat -c '%s' -- "${archive_path}")"
  if (( size < 1 || size > limit )); then
    backup_error "Backup size is outside the configured safety limit"
    return
  fi
}

backup_staging_directory() {
  local configured="${RIVUNE_BACKUP_STAGING_DIR:-}"
  local directory="${configured:-${TMPDIR:-/tmp}}"
  if [[ -L "${directory}" || ! -d "${directory}" || ! -w "${directory}" ]]; then
    backup_error "RIVUNE_BACKUP_STAGING_DIR is missing, unsafe, or not writable"
    return
  fi
  require_trusted_directory_ancestors "${directory}" "Backup staging directory" || return
  directory="$(realpath -e -- "${directory}")" || return
  local directory_owner directory_mode
  directory_owner="$(stat -c '%u' -- "${directory}")"
  directory_mode="$(stat -c '%a' -- "${directory}")"
  if [[ -n "${configured}" ]]; then
    if [[ "${directory_owner}" != "$(id -u)" ]] || \
       (( (8#${directory_mode} & 0077) != 0 )); then
      backup_error "RIVUNE_BACKUP_STAGING_DIR must be private and owned by the current user"
      return
    fi
  elif ! { [[ "${directory_owner}" == "$(id -u)" ]] && \
           (( (8#${directory_mode} & 0022) == 0 )); } && \
       ! { [[ "${directory_owner}" == 0 ]] && \
           (( (8#${directory_mode} & 01000) != 0 )); }; then
    backup_error "TMPDIR must be private or a root-owned sticky directory"
    return
  fi
  printf '%s' "${directory}"
}

stage_bounded_repository_file() {
  local source_path="$1"
  local destination_path="$2"
  local maximum_bytes="$3"
  local staged_size

  # count_bytes makes the rejected excess exactly one byte, rather than one
  # block. Only this bounded private copy is subsequently authenticated/parsed.
  if ! dd if="${source_path}" of="${destination_path}" \
       bs=1048576 count="$(( maximum_bytes + 1 ))" \
       iflag=nofollow,nonblock,fullblock,count_bytes status=none 2>/dev/null; then
    return 1
  fi
  staged_size="$(stat -c '%s' -- "${destination_path}")"
  (( staged_size > 0 && staged_size <= maximum_bytes ))
}

reserve_and_stage_repository_file() {
  local source_path="$1"
  local destination_path="$2"
  local copy_limit="$3"
  local source_size reservation_size staged_size reserved=false

  source_size="$(stat -c '%s' -- "${source_path}")" || return
  if (( source_size < 1 || source_size > copy_limit )); then
    return 1
  fi
  reservation_size="$(( source_size + 1 ))"

  # Reserve the complete bounded copy before opening the repository archive.
  # Linux supplies fallocate; macOS supplies mkfile, which allocates real blocks.
  if command -v fallocate >/dev/null 2>&1; then
    if fallocate --keep-size --length "${reservation_size}" -- "${destination_path}"; then
      reserved=true
    elif (( $? != 127 )); then
      return 1
    fi
  fi
  if [[ "${reserved}" == false ]]; then
    command -v mkfile >/dev/null 2>&1 || return 1
    mkfile "${reservation_size}" "${destination_path}" || return
  fi
  if ! dd if="${source_path}" of="${destination_path}" \
       bs=1048576 count="${reservation_size}" \
       iflag=nofollow,nonblock,fullblock,count_bytes conv=notrunc \
       status=none 2>/dev/null; then
    return 1
  fi
  if [[ "${reserved}" == false ]]; then
    [[ "$(stat -c '%s' -- "${source_path}")" == "${source_size}" ]] || return 1
    truncate -s "${source_size}" -- "${destination_path}" || return
  fi
  staged_size="$(stat -c '%s' -- "${destination_path}")"
  (( staged_size == source_size && staged_size <= copy_limit ))
}

stage_repository_file_exact() {
  local source_path="$1"
  local destination_path="$2"
  local expected_size="$3"
  local observed_size

  require_repository_archive "${source_path}" || return
  observed_size="$(stat -c '%s' -- "${source_path}")" || return
  if (( observed_size != expected_size )); then
    backup_error "Backup archive size does not match the authenticated manifest"
    return
  fi
  reserve_and_stage_repository_file "${source_path}" "${destination_path}" "${expected_size}" || return
  observed_size="$(stat -c '%s' -- "${destination_path}")" || return
  (( observed_size == expected_size ))
}

backup_public_key_id() {
  local key_path="$1"
  openssl pkey -pubin -in "${key_path}" -outform DER 2>/dev/null \
    | openssl dgst -sha256 -r 2>/dev/null \
    | cut -d ' ' -f 1
}

backup_private_key_id() {
  local key_path="$1"
  openssl pkey -in "${key_path}" -pubout -outform DER 2>/dev/null \
    | openssl dgst -sha256 -r 2>/dev/null \
    | cut -d ' ' -f 1
}

parse_authenticated_manifest() {
  local manifest_path="$1"
  local -a lines
  mapfile -t lines < "${manifest_path}"
  MANIFEST_FORMAT=""
  MANIFEST_ARCHIVE_SIZE=""

  if (( ${#lines[@]} == 9 )) && \
     [[ "${lines[0]}" == 'format=rivune-backup-manifest-v2' ]] && \
     [[ "${lines[1]}" == lineage=* ]] && \
     [[ "${lines[2]}" == sequence=* ]] && \
     [[ "${lines[3]}" == backup_id=* ]] && \
     [[ "${lines[4]}" == created_at=* ]] && \
     [[ "${lines[5]}" == archive_name=* ]] && \
     [[ "${lines[6]}" == archive_size=* ]] && \
     [[ "${lines[7]}" == archive_sha256=* ]] && \
     [[ "${lines[8]}" == signing_key_id=* ]]; then
    MANIFEST_FORMAT=v2
    MANIFEST_ARCHIVE_SIZE="${lines[6]#archive_size=}"
    MANIFEST_ARCHIVE_SHA256="${lines[7]#archive_sha256=}"
    MANIFEST_SIGNING_KEY_ID="${lines[8]#signing_key_id=}"
  elif (( ${#lines[@]} == 8 )) && \
       [[ "${lines[0]}" == 'format=rivune-backup-manifest-v1' ]] && \
       [[ "${lines[1]}" == lineage=* ]] && \
       [[ "${lines[2]}" == sequence=* ]] && \
       [[ "${lines[3]}" == backup_id=* ]] && \
       [[ "${lines[4]}" == created_at=* ]] && \
       [[ "${lines[5]}" == archive_name=* ]] && \
       [[ "${lines[6]}" == archive_sha256=* ]] && \
       [[ "${lines[7]}" == signing_key_id=* ]]; then
    MANIFEST_FORMAT=v1
    MANIFEST_ARCHIVE_SHA256="${lines[6]#archive_sha256=}"
    MANIFEST_SIGNING_KEY_ID="${lines[7]#signing_key_id=}"
  else
    backup_error "Backup manifest metadata is invalid"
    return
  fi

  MANIFEST_LINEAGE="${lines[1]#lineage=}"
  MANIFEST_SEQUENCE="${lines[2]#sequence=}"
  MANIFEST_BACKUP_ID="${lines[3]#backup_id=}"
  MANIFEST_CREATED_AT="${lines[4]#created_at=}"
  MANIFEST_ARCHIVE_NAME="${lines[5]#archive_name=}"

  require_backup_token "${MANIFEST_LINEAGE}" "Backup lineage" || return
  if [[ ! "${MANIFEST_SEQUENCE}" =~ ^[1-9][0-9]{0,17}$ ]] || \
     [[ ! "${MANIFEST_BACKUP_ID}" =~ ^[0-9a-f]{32}$ ]] || \
     [[ ! "${MANIFEST_CREATED_AT}" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || \
     [[ ! "${MANIFEST_ARCHIVE_NAME}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$ ]] || \
     [[ ! "${MANIFEST_ARCHIVE_SHA256}" =~ ^[0-9a-f]{64}$ ]] || \
     [[ ! "${MANIFEST_SIGNING_KEY_ID}" =~ ^[0-9a-f]{64}$ ]]; then
    backup_error "Backup manifest metadata is invalid"
    return
  fi

  if [[ "${MANIFEST_FORMAT}" == v2 ]]; then
    local maximum
    maximum="$(backup_archive_limit)" || return
    if [[ ! "${MANIFEST_ARCHIVE_SIZE}" =~ ^[1-9][0-9]{0,17}$ ]] || \
       (( MANIFEST_ARCHIVE_SIZE > maximum )); then
      backup_error "Authenticated backup size is outside the configured safety limit"
      return
    fi
  else
    backup_legacy_archive_limit >/dev/null || return
  fi
}

stage_authenticated_manifest() {
  local archive_path="$1"
  local manifest_path="$2"
  local signature_path="$3"
  local verification_key="$4"
  local staging_directory
  staging_directory="$(backup_staging_directory)" || return

  AUTHENTICATED_BACKUP_FILE=""
  AUTHENTICATED_MANIFEST_FILE="$(mktemp "${staging_directory}/rivune-authenticated-manifest.XXXXXXXX")"
  AUTHENTICATED_SIGNATURE_FILE="$(mktemp "${staging_directory}/rivune-authenticated-signature.XXXXXXXX")"
  chmod 600 -- "${AUTHENTICATED_MANIFEST_FILE}" "${AUTHENTICATED_SIGNATURE_FILE}"
  if ! stage_bounded_repository_file "${manifest_path}" \
       "${AUTHENTICATED_MANIFEST_FILE}" "${BACKUP_MANIFEST_MAX_BYTES}" || \
     ! stage_bounded_repository_file "${signature_path}" \
       "${AUTHENTICATED_SIGNATURE_FILE}" "${BACKUP_MANIFEST_MAX_BYTES}"; then
    cleanup_authenticated_backup
    backup_error "Backup authentication material staging failed"
    return
  fi
  if ! openssl pkeyutl -verify -pubin -inkey "${verification_key}" \
    -sigfile "${AUTHENTICATED_SIGNATURE_FILE}" -rawin -digest sha256 \
    -in "${AUTHENTICATED_MANIFEST_FILE}" >/dev/null 2>&1; then
    cleanup_authenticated_backup
    backup_error "Backup manifest authentication failed"
    return
  fi
  parse_authenticated_manifest "${AUTHENTICATED_MANIFEST_FILE}" || {
    cleanup_authenticated_backup
    return 1
  }

  local key_id
  key_id="$(backup_public_key_id "${verification_key}")"
  if [[ "${key_id}" != "${MANIFEST_SIGNING_KEY_ID}" ]] || \
     [[ "$(basename "${archive_path}")" != "${MANIFEST_ARCHIVE_NAME}" ]]; then
    cleanup_authenticated_backup
    backup_error "Backup manifest does not match the selected archive or key"
    return
  fi
}

stage_authenticated_archive() {
  local archive_path="$1"
  local staging_directory archive_limit archive_digest
  staging_directory="$(backup_staging_directory)" || return
  AUTHENTICATED_BACKUP_FILE="$(mktemp "${staging_directory}/rivune-authenticated-backup.XXXXXXXX.dump")"
  chmod 600 -- "${AUTHENTICATED_BACKUP_FILE}"

  if [[ "${MANIFEST_FORMAT}" == v2 ]]; then
    if ! stage_repository_file_exact "${archive_path}" "${AUTHENTICATED_BACKUP_FILE}" \
         "${MANIFEST_ARCHIVE_SIZE}"; then
      cleanup_authenticated_backup
      backup_error "Backup archive staging failed"
      return
    fi
  else
    archive_limit="$(backup_legacy_archive_limit)" || {
      cleanup_authenticated_backup
      return 1
    }
    if ! require_repository_archive "${archive_path}" || \
       ! reserve_and_stage_repository_file "${archive_path}" \
         "${AUTHENTICATED_BACKUP_FILE}" "${archive_limit}"; then
      cleanup_authenticated_backup
      backup_error "Backup archive staging failed"
      return
    fi
  fi

  archive_digest="$(openssl dgst -sha256 -r "${AUTHENTICATED_BACKUP_FILE}" | cut -d ' ' -f 1)"
  if [[ "${archive_digest}" != "${MANIFEST_ARCHIVE_SHA256}" ]]; then
    cleanup_authenticated_backup
    backup_error "Backup archive authentication failed"
    return
  fi
}

stage_legacy_authenticated_backup() {
  local archive_path="$1"
  local signature_path="$2"
  local verification_key="$3"
  local expected_digest="$4"
  local limit staging_directory
  limit="$(backup_legacy_archive_limit)" || return
  require_repository_archive "${archive_path}" || return
  if [[ ! "${expected_digest}" =~ ^[0-9a-f]{64}$ ]]; then
    backup_error "Legacy backup digest expectation is invalid"
    return
  fi
  staging_directory="$(backup_staging_directory)" || return

  AUTHENTICATED_BACKUP_FILE="$(mktemp "${staging_directory}/rivune-authenticated-backup.XXXXXXXX.dump")"
  AUTHENTICATED_MANIFEST_FILE=""
  AUTHENTICATED_SIGNATURE_FILE="$(mktemp "${staging_directory}/rivune-authenticated-signature.XXXXXXXX")"
  chmod 600 -- "${AUTHENTICATED_BACKUP_FILE}" "${AUTHENTICATED_SIGNATURE_FILE}"
  if ! stage_bounded_repository_file "${signature_path}" \
       "${AUTHENTICATED_SIGNATURE_FILE}" "${BACKUP_MANIFEST_MAX_BYTES}" || \
     ! reserve_and_stage_repository_file "${archive_path}" \
       "${AUTHENTICATED_BACKUP_FILE}" "${limit}" || \
     ! openssl pkeyutl -verify -pubin -inkey "${verification_key}" \
       -sigfile "${AUTHENTICATED_SIGNATURE_FILE}" -rawin -digest sha256 \
       -in "${AUTHENTICATED_BACKUP_FILE}" >/dev/null 2>&1; then
    cleanup_authenticated_backup
    backup_error "Legacy backup authentication failed"
    return
  fi
  LEGACY_ARCHIVE_SHA256="$(openssl dgst -sha256 -r "${AUTHENTICATED_BACKUP_FILE}" | cut -d ' ' -f 1)"
  if [[ "${LEGACY_ARCHIVE_SHA256}" != "${expected_digest}" ]]; then
    cleanup_authenticated_backup
    backup_error "Legacy backup does not match the operator expectation"
    return
  fi
}

cleanup_authenticated_backup() {
  [[ -z "${AUTHENTICATED_BACKUP_FILE:-}" ]] || rm -f -- "${AUTHENTICATED_BACKUP_FILE}"
  [[ -z "${AUTHENTICATED_MANIFEST_FILE:-}" ]] || rm -f -- "${AUTHENTICATED_MANIFEST_FILE}"
  [[ -z "${AUTHENTICATED_SIGNATURE_FILE:-}" ]] || rm -f -- "${AUTHENTICATED_SIGNATURE_FILE}"
  AUTHENTICATED_BACKUP_FILE=""
  AUTHENTICATED_MANIFEST_FILE=""
  AUTHENTICATED_SIGNATURE_FILE=""
}

require_backup_state_location() {
  local state_path="$1"
  local archive_directory="$2"
  local allow_missing="$3"
  if [[ -z "${state_path}" ]]; then
    backup_error "RIVUNE_BACKUP_STATE_FILE is required"
    return
  fi
  local state_parent state_name archive_real
  state_parent="$(dirname "${state_path}")"
  state_name="$(basename "${state_path}")"
  if [[ -L "${state_parent}" || ! -d "${state_parent}" ]]; then
    backup_error "Backup state directory is missing or unsafe"
    return
  fi
  state_parent="$(realpath -e -- "${state_parent}")"
  require_trusted_directory_ancestors "${state_parent}" "Backup state directory" || return
  archive_real="$(realpath -e -- "${archive_directory}")"
  SECURE_BACKUP_STATE_FILE="${state_parent}/${state_name}"
  case "${SECURE_BACKUP_STATE_FILE}" in
    "${archive_real}"|"${archive_real}"/*)
      backup_error "Backup state must be outside the backup directory"
      return
      ;;
  esac
  local parent_mode parent_owner
  parent_mode="$(stat -c '%a' -- "${state_parent}")"
  parent_owner="$(stat -c '%u' -- "${state_parent}")"
  if [[ "${parent_owner}" != "$(id -u)" ]] || (( (8#${parent_mode} & 0022) != 0 )); then
    backup_error "Backup state directory has unsafe ownership or permissions"
    return
  fi
  if [[ -e "${SECURE_BACKUP_STATE_FILE}" || -L "${SECURE_BACKUP_STATE_FILE}" ]]; then
    if [[ -L "${SECURE_BACKUP_STATE_FILE}" || ! -f "${SECURE_BACKUP_STATE_FILE}" || \
          "$(stat -c '%u' -- "${SECURE_BACKUP_STATE_FILE}")" != "$(id -u)" || \
          $(( 8#$(stat -c '%a' -- "${SECURE_BACKUP_STATE_FILE}") & 0077 )) != 0 ]]; then
      backup_error "Backup state file has unsafe ownership or permissions"
      return
    fi
  elif [[ "${allow_missing}" != true ]]; then
    backup_error "Backup state file is required"
    return
  fi
}

lock_backup_state() {
  local lock_file="${SECURE_BACKUP_STATE_FILE}.lock"
  if [[ -L "${lock_file}" ]]; then
    backup_error "Backup state lock is unsafe"
    return
  fi
  umask 077
  if command -v flock >/dev/null 2>&1; then
    exec 9>> "${lock_file}"
    chmod 600 -- "${lock_file}"
    if [[ ! -f "${lock_file}" || "$(stat -c '%u' -- "${lock_file}")" != "$(id -u)" ]]; then
      exec 9>&-
      backup_error "Backup state lock is unsafe"
      return
    fi
    flock -x 9
    BACKUP_STATE_LOCK_KIND=flock
    return
  fi
  if command -v lockf >/dev/null 2>&1; then
    exec 9>> "${lock_file}"
    chmod 600 -- "${lock_file}"
    if [[ ! -f "${lock_file}" || "$(stat -c '%u' -- "${lock_file}")" != "$(id -u)" ]]; then
      exec 9>&-
      backup_error "Backup state lock is unsafe"
      return
    fi
    lockf -s 9
    BACKUP_STATE_LOCK_KIND=lockf
    return
  fi
  backup_error "A supported file-lock utility (flock or lockf) is required"
}

unlock_backup_state() {
  case "${BACKUP_STATE_LOCK_KIND:-}" in
    flock) flock -u 9; exec 9>&- ;;
    lockf) exec 9>&- ;;
  esac
  BACKUP_STATE_LOCK_KIND=
}

load_backup_state() {
  local expected_lineage="$1"
  if [[ ! -f "${SECURE_BACKUP_STATE_FILE}" ]] || \
     (( $(stat -c '%s' -- "${SECURE_BACKUP_STATE_FILE}") > BACKUP_MANIFEST_MAX_BYTES )); then
    backup_error "Backup state is missing or invalid"
    return
  fi
  local -a lines
  mapfile -t lines < "${SECURE_BACKUP_STATE_FILE}"
  if (( ${#lines[@]} != 6 )) || \
     [[ "${lines[0]}" != 'format=rivune-backup-state-v1' ]] || \
     [[ "${lines[1]}" != "lineage=${expected_lineage}" ]] || \
     [[ "${lines[2]}" != next_sequence=* ]] || \
     [[ "${lines[3]}" != latest_sequence=* ]] || \
     [[ "${lines[4]}" != latest_backup_id=* ]] || \
     [[ "${lines[5]}" != latest_archive_sha256=* ]]; then
    backup_error "Backup state metadata is invalid or belongs to another lineage"
    return
  fi
  STATE_LINEAGE="${lines[1]#lineage=}"
  STATE_NEXT_SEQUENCE="${lines[2]#next_sequence=}"
  STATE_LATEST_SEQUENCE="${lines[3]#latest_sequence=}"
  STATE_LATEST_BACKUP_ID="${lines[4]#latest_backup_id=}"
  STATE_LATEST_ARCHIVE_SHA256="${lines[5]#latest_archive_sha256=}"
  if [[ ! "${STATE_NEXT_SEQUENCE}" =~ ^[1-9][0-9]{0,17}$ ]] || \
     [[ ! "${STATE_LATEST_SEQUENCE}" =~ ^(0|[1-9][0-9]{0,17})$ ]] || \
     (( STATE_LATEST_SEQUENCE >= STATE_NEXT_SEQUENCE )) || \
     { (( STATE_LATEST_SEQUENCE == 0 )) && \
       [[ "${STATE_LATEST_BACKUP_ID}" != none || "${STATE_LATEST_ARCHIVE_SHA256}" != none ]]; } || \
     { (( STATE_LATEST_SEQUENCE > 0 )) && \
       { [[ ! "${STATE_LATEST_BACKUP_ID}" =~ ^[0-9a-f]{32}$ ]] || \
         [[ ! "${STATE_LATEST_ARCHIVE_SHA256}" =~ ^[0-9a-f]{64}$ ]]; }; }; then
    backup_error "Backup state metadata is invalid"
    return
  fi
}

write_backup_state() {
  local lineage="$1"
  local next_sequence="$2"
  local latest_sequence="$3"
  local latest_backup_id="$4"
  local latest_digest="$5"
  local state_parent state_name temporary
  state_parent="$(dirname "${SECURE_BACKUP_STATE_FILE}")"
  state_name="$(basename "${SECURE_BACKUP_STATE_FILE}")"
  temporary="$(mktemp --tmpdir="${state_parent}" -- ".${state_name}.partial.XXXXXXXX")"
  chmod 600 -- "${temporary}"
  printf '%s\n' \
    'format=rivune-backup-state-v1' \
    "lineage=${lineage}" \
    "next_sequence=${next_sequence}" \
    "latest_sequence=${latest_sequence}" \
    "latest_backup_id=${latest_backup_id}" \
    "latest_archive_sha256=${latest_digest}" > "${temporary}"
  mv -f -- "${temporary}" "${SECURE_BACKUP_STATE_FILE}"
}

initialize_backup_state() {
  local lineage="$1"
  write_backup_state "${lineage}" 1 0 none none
}

reserve_backup_sequence() {
  local lineage="$1"
  lock_backup_state || return
  if [[ ! -e "${SECURE_BACKUP_STATE_FILE}" ]]; then
    initialize_backup_state "${lineage}"
  fi
  load_backup_state "${lineage}" || {
    unlock_backup_state
    return 1
  }
  if (( STATE_NEXT_SEQUENCE == 999999999999999999 )); then
    unlock_backup_state
    backup_error "Backup generation counter is exhausted"
    return
  fi
  RESERVED_BACKUP_SEQUENCE="${STATE_NEXT_SEQUENCE}"
  write_backup_state "${lineage}" "$(( STATE_NEXT_SEQUENCE + 1 ))" \
    "${STATE_LATEST_SEQUENCE}" "${STATE_LATEST_BACKUP_ID}" "${STATE_LATEST_ARCHIVE_SHA256}"
  unlock_backup_state
}

commit_backup_generation() {
  local lineage="$1"
  local sequence="$2"
  local backup_id="$3"
  local digest="$4"
  lock_backup_state || return
  load_backup_state "${lineage}" || {
    unlock_backup_state
    return 1
  }
  if (( sequence >= STATE_NEXT_SEQUENCE )); then
    unlock_backup_state
    backup_error "Backup state rejected an unreserved generation"
    return
  fi
  if (( sequence > STATE_LATEST_SEQUENCE )); then
    write_backup_state "${lineage}" "${STATE_NEXT_SEQUENCE}" "${sequence}" "${backup_id}" "${digest}"
  fi
  unlock_backup_state
}

record_restore_audit() {
  local action="$1"
  local lineage="$2"
  local sequence="$3"
  local backup_id="$4"
  local digest="$5"
  local audit_file="${SECURE_BACKUP_STATE_FILE}.audit"
  if [[ -L "${audit_file}" ]]; then
    backup_error "Restore audit log is unsafe"
    return
  fi
  umask 077
  touch -- "${audit_file}"
  chmod 600 -- "${audit_file}"
  if [[ ! -f "${audit_file}" || "$(stat -c '%u' -- "${audit_file}")" != "$(id -u)" || \
        $(( 8#$(stat -c '%a' -- "${audit_file}") & 0077 )) != 0 ]]; then
    backup_error "Restore audit log is unsafe"
    return
  fi
  printf '%s action=%s uid=%s lineage=%s sequence=%s backup_id=%s sha256=%s\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${action}" "$(id -u)" "${lineage}" \
    "${sequence}" "${backup_id}" "${digest}" >> "${audit_file}"
}

enforce_current_restore_policy() {
  local expected_backup_id="$1"
  if [[ "${expected_backup_id}" != "${MANIFEST_BACKUP_ID}" ]]; then
    backup_error "Backup does not match the operator-selected backup ID"
    return
  fi
  load_backup_state "${MANIFEST_LINEAGE}" || return
  if [[ "${MANIFEST_SEQUENCE}" != "${STATE_LATEST_SEQUENCE}" || \
        "${MANIFEST_BACKUP_ID}" != "${STATE_LATEST_BACKUP_ID}" || \
        "${MANIFEST_ARCHIVE_SHA256}" != "${STATE_LATEST_ARCHIVE_SHA256}" ]]; then
    backup_error "Backup is not the current trusted generation"
    return
  fi
}
