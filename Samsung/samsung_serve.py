#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""
Samsung EM32DX local image push — reverse-engineered MDC protocol.

The phone app runs an HTTP server on port 6868. When the user pushes an image,
it sends an MDC (Multi Display Control) command over TCP port 1515 telling the
display to download content from http://phone_ip:6868/content?id=X&content_type=Y.
We replicate both sides: serve the image and send the MDC command.

One-time setup: pair the display with the Samsung E-Paper app at least once
so the display has Wi-Fi credentials. After that this script takes over.

Usage:
    uv run samsung_serve.py serve              # start HTTP server (keep running)
    uv run samsung_serve.py push image.png     # push image to display
    uv run samsung_serve.py wake               # Wake-on-LAN only
"""

import argparse
import http.server
import os
import shutil
import socket
import ssl
import time

SAMSUNG_IP   = "192.168.1.101"
SAMSUNG_MAC  = "b0:f2:f6:57:d5:cd"
MAC_IP       = "192.168.1.100"
PORT         = 6868          # must match what the app uses
MDC_PORT     = 1515          # standard Samsung MDC port
MDC_PIN      = "250126"
IMAGE_PATH   = "/tmp/samsung_current.png"
CONTENT_ID   = "local"
CONTENT_TYPE = "png"


# ---------------------------------------------------------------------------
# SSDP discovery
# ---------------------------------------------------------------------------

SSDP_ADDR = "239.255.255.250"
SSDP_PORT = 1900
SSDP_MX   = 3

def ssdp_discover(timeout: float = SSDP_MX + 1.0, st: str = "ssdp:all") -> list[dict]:
    """Send an SSDP M-SEARCH and return all responses as dicts with keys: ip, st, usn, location, raw."""
    msg = (
        f"M-SEARCH * HTTP/1.1\r\n"
        f"HOST: {SSDP_ADDR}:{SSDP_PORT}\r\n"
        f'MAN: "ssdp:discover"\r\n'
        f"MX: {SSDP_MX}\r\n"
        f"ST: {st}\r\n"
        f"\r\n"
    ).encode()

    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM, socket.IPPROTO_UDP)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_TTL, 2)
    sock.settimeout(timeout)
    sock.bind(("", 0))
    sock.sendto(msg, (SSDP_ADDR, SSDP_PORT))

    seen = {}
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            data, addr = sock.recvfrom(4096)
        except socket.timeout:
            break
        text = data.decode(errors="replace")
        headers = {}
        for line in text.splitlines()[1:]:
            if ":" in line:
                k, _, v = line.partition(":")
                headers[k.strip().upper()] = v.strip()
        ip = addr[0]
        key = (ip, headers.get("ST", ""), headers.get("USN", ""))
        if key not in seen:
            seen[key] = {
                "ip":       ip,
                "st":       headers.get("ST", ""),
                "usn":      headers.get("USN", ""),
                "location": headers.get("LOCATION", ""),
                "server":   headers.get("SERVER", ""),
                "raw":      text,
            }
    sock.close()
    return list(seen.values())


def find_samsung(timeout: float = SSDP_MX + 1.0) -> str | None:
    """Return the IP of the first SSDP responder that looks like the Samsung display, or None."""
    devices = ssdp_discover(timeout=timeout)
    for d in devices:
        combined = f"{d['st']} {d['usn']} {d['server']} {d['location']}".lower()
        if "samsung" in combined or "epaper" in combined or "mdc" in combined:
            return d["ip"]
    # Fallback: if only one device responded from our subnet, assume it's ours
    subnet = ".".join(SAMSUNG_IP.split(".")[:3]) + "."
    local = [d for d in devices if d["ip"].startswith(subnet) and d["ip"] != MAC_IP]
    if len(local) == 1:
        return local[0]["ip"]
    return None


# ---------------------------------------------------------------------------
# MDC packet builder
# Reverse-engineered from MDCContentDownloadCommand.Set.l() in the APK.
#
# Packet layout:
#   [0xAA] [cmdId=0xC7] [devId=0x00] [dataLen] [subCmd=0x53] [dtype=0x00] [urlLen] [url...] [checksum]
#
# dataLen  = len(url) + 3   (subCmd + dtype + urlLen fields)
# checksum = (cmdId + devId + dataLen + subCmd + dtype + urlLen + sum(url)) & 0xFF
# ---------------------------------------------------------------------------

def _mdc_content_download(url: str) -> bytes:
    url_bytes = url.encode("utf-8")
    if len(url_bytes) > 255:
        raise ValueError(f"URL too long ({len(url_bytes)} bytes, max 255)")
    cmd_id   = 0xC7   # MDC_COMMAND_CONTENT_DOWNLOAD commandId = 199
    dev_id   = 0x00
    sub_cmd  = 0x53   # subCommand = 83
    dtype    = 0x00   # dataType (DEFAULT_INITIAL_BUFFER_SIZE=4096 cast to byte = 0)
    url_len  = len(url_bytes)
    data_len = url_len + 3

    checksum = (cmd_id + dev_id + data_len + sub_cmd + dtype + url_len + sum(url_bytes)) & 0xFF

    return bytes([0xAA, cmd_id, dev_id, data_len, sub_cmd, dtype, url_len]) + url_bytes + bytes([checksum])


def send_mdc(url: str, ip: str = SAMSUNG_IP, port: int = MDC_PORT, timeout: float = 10.0, pin: str = MDC_PIN):
    # Auth protocol (reverse-engineered from libepaper_socket.so):
    #   1. TCP connect → read cleartext banner "MDCSTART<<TLS>>"
    #   2. TLS handshake (no client cert; display has Samsung-signed server cert)
    #   3. SSL_write(pin)  — raw PIN string, e.g. "000000"
    #   4. SSL_read  → expect "MDCAUTH<<PASS>>" (fail: "MDCAUTH<<FAIL:0x01>>")
    #   5. SSL_write(mdc_binary_packet)
    #   6. SSL_read  → MDC response
    pkt = _mdc_content_download(url)
    print(f"  MDC → {ip}:{port}  url={url}", flush=True)
    print(f"  packet: {pkt.hex()}", flush=True)

    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    with socket.create_connection((ip, port), timeout=timeout) as raw:
        raw.settimeout(timeout)
        banner = raw.recv(64)
        print(f"  banner: {banner}", flush=True)

        with ctx.wrap_socket(raw, server_hostname=ip) as s:
            # Step 3: authenticate with PIN
            s.sendall(pin.encode("utf-8"))
            auth_resp = s.recv(64)
            print(f"  auth: {auth_resp}", flush=True)
            if b"MDCAUTH<<PASS>>" not in auth_resp:
                print(f"  AUTH FAILED — wrong PIN or cert? Got: {auth_resp}", flush=True)
                return

            # Step 5: send MDC command
            s.sendall(pkt)
            try:
                resp = s.recv(64)
                print(f"  response: {resp.hex()} = {resp}", flush=True)
            except socket.timeout:
                print("  (no response within timeout)", flush=True)


def wait_for_wake(ip: str = SAMSUNG_IP, timeout_per_attempt: float = 2.0,
                  poll_interval: float = 3.0, max_wait: float = 300.0) -> bool:
    print(f"Waiting for display to wake (press the button on the display)…", flush=True)
    deadline = time.time() + max_wait
    while time.time() < deadline:
        try:
            with socket.create_connection((ip, MDC_PORT), timeout=timeout_per_attempt):
                print("Display is awake!", flush=True)
                return True
        except (OSError, socket.timeout):
            time.sleep(poll_interval)
    print(f"Timed out after {max_wait:.0f}s — display never woke.", flush=True)
    return False


def send_wol(mac: str = SAMSUNG_MAC):
    mac_bytes = bytes.fromhex(mac.replace(":", "").replace("-", ""))
    packet = b"\xff" * 6 + mac_bytes * 16
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
        s.sendto(packet, ("<broadcast>", 9))
    print(f"WoL sent to {mac}")


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        path = self.path.split("?")[0]
        params = {}
        if "?" in self.path:
            for kv in self.path.split("?", 1)[1].split("&"):
                if "=" in kv:
                    k, v = kv.split("=", 1)
                    params[k] = v

        if path == "/content":
            try:
                data = open(IMAGE_PATH, "rb").read()
                self.send_response(200)
                self.send_header("Content-Type", "image/png")
                self.send_header("Content-Length", len(data))
                self.end_headers()
                self.wfile.write(data)
            except FileNotFoundError:
                self.send_response(404)
                self.end_headers()
        elif path == "/image":
            # Also serve at /image?path=... as the app does
            try:
                data = open(IMAGE_PATH, "rb").read()
                self.send_response(200)
                self.send_header("Content-Type", "image/png")
                self.send_header("Content-Length", len(data))
                self.end_headers()
                self.wfile.write(data)
            except FileNotFoundError:
                self.send_response(404)
                self.end_headers()
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, fmt, *args):
        print(f"  [{time.strftime('%H:%M:%S')}] {fmt % args}", flush=True)


def main():
    parser = argparse.ArgumentParser(description="Samsung EM32DX MDC image push")
    parser.add_argument("--port", type=int, default=PORT)
    parser.add_argument("--display-ip", default=None,
                        help=f"Display IP (default: auto-discover via SSDP, fallback {SAMSUNG_IP})")
    sub = parser.add_subparsers(dest="cmd")

    sub.add_parser("serve", help="Start HTTP server (keep running)")

    sub.add_parser("discover", help="SSDP scan and print all devices found")

    p = sub.add_parser("push", help="Push image to display via MDC")
    p.add_argument("image", help="Pre-dithered PNG to display")
    p.add_argument("--no-wake", action="store_true", help="Skip WoL")
    p.add_argument("--wait-for-wake", action="store_true",
                   help="Poll MDC port until display wakes (press button); skips WoL")
    p.add_argument("--no-mdc", action="store_true", help="Skip MDC command (serve only)")
    p.add_argument("--pin", default=MDC_PIN, help=f"MDC PIN (default: {MDC_PIN})")

    sub.add_parser("wake", help="Send Wake-on-LAN only")

    args = parser.parse_args()

    # Resolve display IP: explicit > SSDP > hardcoded fallback
    display_ip = args.display_ip
    if display_ip is None and args.cmd in ("push", "wake", "discover", None):
        if args.cmd != "discover":
            print("No --display-ip given, scanning via SSDP…", flush=True)
        found = find_samsung()
        if found:
            print(f"Found display at {found}", flush=True)
            display_ip = found
        else:
            display_ip = SAMSUNG_IP
            if args.cmd != "discover":
                print(f"SSDP found nothing, falling back to {display_ip}", flush=True)

    if args.cmd == "serve":
        print(f"Serving at http://0.0.0.0:{args.port}/", flush=True)
        print(f"Content URL: http://{MAC_IP}:{args.port}/content?id={CONTENT_ID}&content_type={CONTENT_TYPE}", flush=True)
        http.server.HTTPServer(("0.0.0.0", args.port), Handler).serve_forever()

    elif args.cmd == "discover":
        devices = ssdp_discover()
        if not devices:
            print("No SSDP devices found.")
        for d in devices:
            print(f"\n  ip={d['ip']}  ST={d['st']}")
            if d['usn']:      print(f"    USN:      {d['usn']}")
            if d['server']:   print(f"    Server:   {d['server']}")
            if d['location']: print(f"    Location: {d['location']}")

    elif args.cmd == "push":
        shutil.copy2(args.image, IMAGE_PATH)
        print(f"Image: {args.image} → {IMAGE_PATH}", flush=True)
        if args.wait_for_wake:
            if not wait_for_wake(ip=display_ip):
                return
        elif not args.no_wake:
            send_wol()
            time.sleep(2)
        if not args.no_mdc:
            url = f"http://{MAC_IP}:{args.port}/content?id={CONTENT_ID}&content_type={CONTENT_TYPE}"
            send_mdc(url, ip=display_ip, pin=args.pin)

    elif args.cmd == "wake":
        send_wol()

    else:
        parser.print_help()


if __name__ == "__main__":
    main()
