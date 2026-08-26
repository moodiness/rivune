#!/usr/bin/env python3
"""Hermetic Stremio-compatible add-on used by the release journey."""
from __future__ import annotations
import argparse
import json
import re
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import unquote, urlsplit

MEDIA_FILES = {"demo-720p.mp4": "video/mp4", "demo-360p.mp4": "video/mp4", "demo.en.vtt": "text/vtt; charset=utf-8", "demo.fr.vtt": "text/vtt; charset=utf-8", "artwork.svg": "image/svg+xml"}
RESOURCE_RE = re.compile(r"^/(catalog|meta|stream)/([^/]+)/([^/]+?)(?:/([^/]+))?\.json$")

class FixtureHandler(BaseHTTPRequestHandler):
    server_version = "RivuneReleaseFixture/1"
    protocol_version = "HTTP/1.1"
    def log_message(self, message: str, *args: object) -> None:
        print(f"fixture {self.address_string()} {message % args}", flush=True)
    def _send_json(self, value: object, status: HTTPStatus = HTTPStatus.OK) -> None:
        body = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status); self.send_header("Content-Type", "application/json"); self.send_header("Cache-Control", "no-store"); self.send_header("Content-Length", str(len(body))); self.end_headers()
        if self.command != "HEAD": self.wfile.write(body)
    def _safe_path(self) -> str | None:
        try: decoded = unquote(urlsplit(self.path).path, errors="strict")
        except (UnicodeDecodeError, ValueError): self.send_error(400, "invalid path encoding"); return None
        if "\x00" in decoded or "\\" in decoded or any(part in {".", ".."} for part in decoded.split("/")):
            self.send_error(400, "unsafe path"); return None
        return decoded
    def do_HEAD(self) -> None: self._serve()
    def do_GET(self) -> None: self._serve()
    def _serve(self) -> None:
        path = self._safe_path()
        if path is None: return
        if path == "/health": self._send_json({"status": "ok"}); return
        if path == "/manifest.json":
            self._send_json({"id":"org.rivune.release-journey","version":"1.0.0","name":"Release Journey Fixture","description":"Hermetic release validation media","types":["movie"],"resources":["catalog","meta","stream"],"catalogs":[{"type":"movie","id":"release-search","name":"Release Journey","extra":[{"name":"search","isRequired":True},{"name":"skip"},{"name":"limit"}]}]}); return
        if path.startswith("/media/"): self._serve_media(path.removeprefix("/media/")); return
        match = RESOURCE_RE.fullmatch(path)
        if match is None: self.send_error(404); return
        resource, media_type, encoded_id, encoded_extra = match.groups(); resource_id = unquote(encoded_id)
        if media_type != "movie": self.send_error(404); return
        if resource == "catalog":
            if resource_id != "release-search": self.send_error(404); return
            self._send_json({"metas": [self._meta()]}); return
        if resource_id != "release-demo": self.send_error(404); return
        if resource == "meta": self._send_json({"meta": self._meta()}); return
        if resource == "stream":
            origin = self.server.fixture_origin  # type: ignore[attr-defined]
            self._send_json({"streams":[{"name":"Release Journey 720p","title":"Synthetic H.264/AAC fixture","url":f"{origin}/media/demo-720p.mp4","behaviorHints":{"filename":"release-journey.mp4"}}]}); return
        self.send_error(404)
    @staticmethod
    def _meta() -> dict[str, object]:
        return {"id":"release-demo","type":"movie","name":"Release Journey","description":"A deterministic synthetic movie for release validation.","releaseInfo":"2026","released":"2026-01-01T00:00:00.000Z"}
    def _serve_media(self, name: str) -> None:
        content_type = MEDIA_FILES.get(name)
        if content_type is None or "/" in name or "\\" in name: self.send_error(404); return
        path = self.server.asset_root / name  # type: ignore[attr-defined]
        if not path.is_file() or path.is_symlink(): self.send_error(404); return
        size = path.stat().st_size; start, end, status = 0, size - 1, HTTPStatus.OK
        requested_range = self.headers.get("Range")
        if requested_range:
            match = re.fullmatch(r"bytes=(\d*)-(\d*)", requested_range.strip())
            if match is None or (not match.group(1) and not match.group(2)): self._range_unsatisfied(size); return
            if match.group(1): start = int(match.group(1)); end = int(match.group(2)) if match.group(2) else size - 1
            else:
                suffix = int(match.group(2))
                if suffix <= 0: self._range_unsatisfied(size); return
                start = max(0, size - suffix)
            if start >= size or end < start: self._range_unsatisfied(size); return
            end = min(end, size - 1); status = HTTPStatus.PARTIAL_CONTENT
        length = end - start + 1
        self.send_response(status); self.send_header("Content-Type", content_type); self.send_header("Accept-Ranges", "bytes"); self.send_header("Content-Length", str(length))
        if status == HTTPStatus.PARTIAL_CONTENT: self.send_header("Content-Range", f"bytes {start}-{end}/{size}")
        self.send_header("Cache-Control", "no-store"); self.end_headers()
        if self.command == "HEAD": return
        with path.open("rb") as asset:
            asset.seek(start); remaining = length
            while remaining:
                chunk = asset.read(min(65536, remaining))
                if not chunk: break
                self.wfile.write(chunk); remaining -= len(chunk)
    def _range_unsatisfied(self, size: int) -> None:
        self.send_response(HTTPStatus.REQUESTED_RANGE_NOT_SATISFIABLE); self.send_header("Content-Range", f"bytes */{size}"); self.send_header("Content-Length", "0"); self.end_headers()

def main() -> None:
    parser = argparse.ArgumentParser(); parser.add_argument("--host", default="0.0.0.0"); parser.add_argument("--port", type=int, required=True); parser.add_argument("--assets", type=Path, required=True); parser.add_argument("--public-origin", required=True); args = parser.parse_args()
    assets = args.assets.resolve(strict=True)
    for name in MEDIA_FILES:
        candidate = assets / name
        if not candidate.is_file() or candidate.is_symlink(): raise SystemExit(f"missing or unsafe fixture asset: {name}")
    server = ThreadingHTTPServer((args.host, args.port), FixtureHandler); server.asset_root = assets; server.fixture_origin = args.public_origin.rstrip("/"); server.serve_forever()  # type: ignore[attr-defined]
if __name__ == "__main__": main()
