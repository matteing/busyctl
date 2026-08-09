#!/usr/bin/env python3
"""Serve the deterministic Muni README screenshot fixture on loopback."""

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from urllib.parse import urlparse


PREDICTIONS = {
    "14509": [
        {
            "route": {"id": "N"},
            "values": [
                {
                    "minutes": 4,
                    "timestamp": 1000,
                    "direction": {"destinationName": "Caltrain/Ballpark"},
                }
            ],
        }
    ],
    "14510": [],
    "17166": [
        {
            "route": {"id": "T"},
            "values": [
                {
                    "minutes": 7,
                    "timestamp": 2000,
                    "direction": {"destinationName": "Sunnydale"},
                }
            ],
        }
    ],
    "17397": [],
}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        parts = urlparse(self.path).path.strip("/").split("/")
        stop_code = parts[1] if len(parts) == 3 and parts[0] == "stopcodes" else ""
        payload = json.dumps(PREDICTIONS.get(stop_code, [])).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, format: str, *args: object) -> None:
        pass


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=18081)
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print(f"Muni fixture listening at http://127.0.0.1:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
