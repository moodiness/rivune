from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps(
            {
                "forwardedFor": self.headers.get("X-Forwarded-For", ""),
                "forwardedHost": self.headers.get("X-Forwarded-Host", ""),
                "forwardedProto": self.headers.get("X-Forwarded-Proto", ""),
                "realIP": self.headers.get("X-Real-IP", ""),
            }
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass


ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
