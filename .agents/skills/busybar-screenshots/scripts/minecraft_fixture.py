#!/usr/bin/env python3
"""Serve a deterministic Minecraft Java status response on loopback."""

import argparse
import json
import socketserver


MAX_PACKET_SIZE = 1 << 20


def read_exact(stream, length: int) -> bytes:
    payload = stream.read(length)
    if len(payload) != length:
        raise EOFError("truncated packet")
    return payload


def read_varint(stream) -> int:
    value = 0
    for shift in range(0, 35, 7):
        current = read_exact(stream, 1)[0]
        value |= (current & 0x7F) << shift
        if current & 0x80 == 0:
            return value
    raise ValueError("VarInt is longer than five bytes")


def encode_varint(value: int) -> bytes:
    payload = bytearray()
    while True:
        current = value & 0x7F
        value >>= 7
        if value:
            current |= 0x80
        payload.append(current)
        if not value:
            return bytes(payload)


def read_packet(stream) -> bytes:
    length = read_varint(stream)
    if length < 0 or length > MAX_PACKET_SIZE:
        raise ValueError(f"invalid packet length {length}")
    return read_exact(stream, length)


class Handler(socketserver.StreamRequestHandler):
    def handle(self) -> None:
        try:
            read_packet(self.rfile)  # handshake
            read_packet(self.rfile)  # status request
            status = json.dumps(
                {
                    "version": {"name": "Paper 26.2", "protocol": 776},
                    "players": {"max": 5, "online": 2},
                    "description": "Screenshot fixture",
                },
                separators=(",", ":"),
            ).encode()
            payload = b"\x00" + encode_varint(len(status)) + status
            self.wfile.write(encode_varint(len(payload)) + payload)
        except (EOFError, OSError, ValueError):
            return


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=25566)
    args = parser.parse_args()
    server = Server(("127.0.0.1", args.port), Handler)
    print(f"Minecraft fixture listening at 127.0.0.1:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
