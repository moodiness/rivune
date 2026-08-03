#!/bin/sh
set -eu

EXPECTED_FFMPEG_VERSION=8.1.2
SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPOSITORY_DIR=$(CDPATH= cd "$SCRIPT_DIR/.." && pwd)
ASSET_DIR="$REPOSITORY_DIR/server/internal/demo/assets"
STAGING_DIR="$ASSET_DIR/.generate-demo-assets.$$"

FFMPEG_VERSION=$(ffmpeg -version | sed -n '1p')
printf 'Expected FFmpeg version: %s\n' "$EXPECTED_FFMPEG_VERSION"
printf 'Detected %s\n' "$FFMPEG_VERSION"
case "$FFMPEG_VERSION" in
  "ffmpeg version $EXPECTED_FFMPEG_VERSION"*) ;;
  *)
    printf 'This recipe requires FFmpeg %s.\n' "$EXPECTED_FFMPEG_VERSION" >&2
    exit 1
    ;;
esac

mkdir -p "$ASSET_DIR"
rm -rf "$STAGING_DIR"
mkdir "$STAGING_DIR"
cleanup() {
  rm -rf "$STAGING_DIR"
}
trap cleanup EXIT HUP INT TERM

make_video() {
  width=$1
  height=$2
  crf=$3
  output=$4

  ffmpeg -hide_banner -nostdin -loglevel warning -y \
    -f lavfi -i "testsrc2=size=${width}x${height}:rate=24:duration=12" \
    -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=12" \
    -f lavfi -i "sine=frequency=554.37:sample_rate=48000:duration=12" \
    -filter_complex \
      "[1:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo[eng];[2:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo[fra]" \
    -map 0:v:0 -map '[eng]' -map '[fra]' \
    -map_metadata -1 \
    -c:v libx264 -preset slow -crf "$crf" -profile:v high -threads:v 1 \
    -pix_fmt yuv420p -r 24 -g 48 -keyint_min 48 -sc_threshold 0 \
    -c:a aac -b:a 96k -ar 48000 -ac 2 -threads:a 1 \
    -metadata:s:a:0 language=eng -metadata:s:a:0 title=English \
    -metadata:s:a:1 language=fra -metadata:s:a:1 title=French \
    -disposition:a:0 default -disposition:a:1 0 \
    -movflags +faststart -t 12 "$output"
}

make_video 1280 720 30 "$STAGING_DIR/demo-720p.mp4"
make_video 640 360 28 "$STAGING_DIR/demo-360p.mp4"

cat > "$STAGING_DIR/demo.en.vtt" <<'VTT'
WEBVTT

00:00:00.000 --> 00:00:03.500
Rivune synthetic demonstration

00:00:04.000 --> 00:00:07.500
Signal Horizon — playback preview

00:00:08.000 --> 00:00:12.000
No third-party media is used.
VTT

cat > "$STAGING_DIR/demo.fr.vtt" <<'VTT'
WEBVTT

00:00:00.000 --> 00:00:03.500
Démonstration synthétique de Rivune

00:00:04.000 --> 00:00:07.500
Signal Horizon — aperçu de lecture

00:00:08.000 --> 00:00:12.000
Aucun média tiers n’est utilisé.
VTT

for asset in demo-720p.mp4 demo-360p.mp4 demo.en.vtt demo.fr.vtt; do
  mv "$STAGING_DIR/$asset" "$ASSET_DIR/$asset"
done

printf 'Generated demo assets in %s\n' "$ASSET_DIR"
