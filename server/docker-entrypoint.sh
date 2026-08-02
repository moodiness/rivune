#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  exec "$@"
fi

puid="${PUID:-65532}"
pgid="${PGID:-65532}"
video_gid="${RIVUNE_VIDEO_GROUP_ID:-}"

case "$puid" in
  ''|*[!0-9]*) echo "PUID must be a positive numeric user ID" >&2; exit 64 ;;
esac
case "$pgid" in
  ''|*[!0-9]*) echo "PGID must be a positive numeric group ID" >&2; exit 64 ;;
esac
if [ "$puid" -eq 0 ] || [ "$pgid" -eq 0 ]; then
  echo "PUID and PGID must be greater than zero" >&2
  exit 64
fi
if [ -n "$video_gid" ]; then
  case "$video_gid" in
    *[!0-9]*) echo "RIVUNE_VIDEO_GROUP_ID must be a positive numeric group ID" >&2; exit 64 ;;
  esac
  if [ "$video_gid" -eq 0 ]; then
    echo "RIVUNE_VIDEO_GROUP_ID must be greater than zero" >&2
    exit 64
  fi
fi

media_directory="${RIVUNE_MEDIA_TEMP_DIR:-}"
if [ -n "$media_directory" ]; then
  mkdir -p "$media_directory"
  chown "$puid:$pgid" "$media_directory"
fi

artwork_directory="${RIVUNE_ARTWORK_CACHE_DIR:-/var/lib/rivune/artwork}"
mkdir -p "$artwork_directory"
chown "$puid:$pgid" "$artwork_directory"

if [ -n "$video_gid" ]; then
  exec setpriv --reuid="$puid" --regid="$pgid" --groups="$video_gid" --no-new-privs -- "$@"
fi
exec setpriv --reuid="$puid" --regid="$pgid" --clear-groups --no-new-privs -- "$@"
