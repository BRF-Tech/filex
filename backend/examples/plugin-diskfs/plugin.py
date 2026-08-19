#!/usr/bin/env python3
"""A complete filex storage plugin in Python, with no SDK.

Why this exists: filex's plugin protocol claims that a plugin can be written
in *any* language. That claim is only worth something if somebody has written
one without the Go SDK, so here it is — standard library only, one file,
serving the whole protocol over the unix socket filex hands it.

It is also the plugin filex's own acceptance run uses to exercise the
capabilities the Go example does not implement: real ranged reads, server-side
move and copy, directories, mtimes and a change stream.

    Admin -> Plugins -> Install a plugin -> upload this file
    (it must be executable: chmod +x plugin.py)

Backed by a real directory: the `root` config field.

⚠ Deliberately small, not defensive. A production plugin would bound
concurrency, stream with a fixed buffer, and be far more careful about paths
than the one check below.
"""

import hashlib
import http.server
import json
import mimetypes
import os
import shutil
import socket
import socketserver
import sys
import tempfile
import threading
import time
from datetime import datetime, timezone

PROTOCOL = 1
TOKEN = os.environ.get("FILEX_PLUGIN_TOKEN", "")
SOCKET_DIR = os.environ.get("FILEX_PLUGIN_SOCKET_DIR", "")
# FILEX_PLUGIN_LISTEN is how a plugin is run BY HAND during development: bind
# this address instead of a socket, and register the result in filex as a
# remote plugin. Honouring it is part of the contract - a plugin that ignores
# it binds a random port and the address the developer configured is wrong.
LISTEN = os.environ.get("FILEX_PLUGIN_LISTEN", "")
# READ_ONLY exists so the same file can prove the other half of the contract:
# a plugin that declares no write/delete must not be offered write at all.
READ_ONLY = os.environ.get("DISKFS_READ_ONLY", "") in ("1", "true")

# Every instance filex creates, by id -> its root directory.
INSTANCES = {}
# SCRATCH holds the instances opened for /v1/selftest, whose directories are
# ours to delete when filex releases them.
SCRATCH = set()
NEXT_ID = [0]
LOCK = threading.Lock()

# Unfinished multipart uploads: id -> {"path": str, "dir": tempdir}.
UPLOADS = {}

# Change events, drained by /watch. A real plugin would use inotify; this one
# records what it did itself, which is enough to prove the stream works.
EVENTS = []
EVENTS_CV = threading.Condition()


def rfc3339(ts: float) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).isoformat().replace("+00:00", "Z")


def emit(op: str, path: str, frm: str = "") -> None:
    with EVENTS_CV:
        EVENTS.append({"op": op, "path": path, "from": frm})
        EVENTS_CV.notify_all()


def describe() -> dict:
    caps = {
        "range": True,
        "write": not READ_ONLY,
        "delete": not READ_ONLY,
        "move": not READ_ONLY,
        "copy": not READ_ONLY,
        "mkdir": not READ_ONLY,
        "set_mtime": not READ_ONLY,
        "watch": True,
        # Resumable uploads, assembled from parts on disk. filex uses them for
        # the staged-upload path; it pushes each part itself, so there are no
        # part URLs to hand out.
        "multipart": not READ_ONLY,
    }
    return {
        "protocol": PROTOCOL,
        "name": "diskfs-ro" if READ_ONLY else "diskfs",
        "version": "1.0.0",
        "label": "Disk (Python example, read-only)" if READ_ONLY else "Disk (Python example)",
        "fields": [
            {
                "key": "root",
                "type": "string",
                "label": "Root directory",
                "help": "A directory on the machine running this plugin.",
                "required": True,
                "root": True,
                "monospace": True,
                "placeholder": "/srv/data",
            }
        ],
        "capabilities": caps,
    }


def resolve(instance: str, rel: str) -> str:
    """Join a request path onto the instance root, refusing to leave it."""
    root = INSTANCES[instance]
    rel = (rel or "").strip("/")
    full = os.path.realpath(os.path.join(root, rel))
    if full != os.path.realpath(root) and not full.startswith(os.path.realpath(root) + os.sep):
        raise PermissionError("path escapes the storage root: " + rel)
    return full


def obj_for(full: str, rel: str) -> dict:
    st = os.stat(full)
    is_dir = os.path.isdir(full)
    o = {
        "path": rel.strip("/"),
        "name": os.path.basename(rel.rstrip("/")) or "/",
        "size": 0 if is_dir else st.st_size,
        "kind": "dir" if is_dir else "file",
        "mtime": rfc3339(st.st_mtime),
    }
    if not is_dir:
        mime, _ = mimetypes.guess_type(full)
        if mime:
            o["mime"] = mime
    return o


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # keep filex's log readable
        sys.stderr.write("diskfs: " + (fmt % args) + "\n")

    # ── plumbing ────────────────────────────────────────────────────────────

    def authorised(self) -> bool:
        if self.headers.get("Authorization") == "Bearer " + TOKEN:
            return True
        self.fail(401, "unauthorized", "bad or missing token")
        return False

    def fail(self, status: int, code: str, message: str = "") -> None:
        body = json.dumps({"error": code, "message": message}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def ok_json(self, payload, status: int = 200) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def no_content(self) -> None:
        self.send_response(204)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def read_json(self) -> dict:
        n = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(n) or b"{}")

    def split(self):
        """(instance, op, query) from /v1/instances/{id}/{op}?…"""
        path, _, qs = self.path.partition("?")
        query = {}
        for pair in qs.split("&"):
            if not pair:
                continue
            k, _, v = pair.partition("=")
            from urllib.parse import unquote_plus

            query[unquote_plus(k)] = unquote_plus(v)
        parts = path.strip("/").split("/")  # v1, instances, id, op...
        inst = parts[2] if len(parts) > 2 else ""
        # The op is EVERYTHING after the instance id, not just the next
        # segment: "multipart/part" is one operation. Taking parts[3] alone
        # made every multipart route answer 404, which filex's conformance
        # run caught before this plugin could be used for anything.
        op = "/".join(parts[3:]) if len(parts) > 3 else ""
        return inst, op, query

    # ── routes ──────────────────────────────────────────────────────────────

    def do_GET(self):
        if not self.authorised():
            return
        if self.path == "/v1/describe":
            return self.ok_json(describe())
        inst, op, q = self.split()
        if inst not in INSTANCES:
            return self.fail(409, "no_instance", "unknown instance " + inst)
        try:
            if op == "list":
                base = resolve(inst, q.get("path", ""))
                out = []
                for name in sorted(os.listdir(base)):
                    rel = os.path.join(q.get("path", "").strip("/"), name).strip("/")
                    out.append(obj_for(os.path.join(base, name), rel))
                return self.ok_json({"objects": out})
            if op == "stat":
                rel = q.get("path", "")
                return self.ok_json(obj_for(resolve(inst, rel), rel))
            if op == "read":
                return self.read_file(inst, q.get("path", ""))
            if op == "watch":
                return self.watch()
        except FileNotFoundError:
            return self.fail(404, "not_found", q.get("path", ""))
        except PermissionError as e:
            return self.fail(403, "read_only", str(e))
        return self.fail(404, "not_found", op)

    def read_file(self, inst: str, rel: str):
        full = resolve(inst, rel)
        size = os.path.getsize(full)
        start, end = 0, size - 1
        status = 200
        rng = self.headers.get("Range")
        if rng and rng.startswith("bytes="):
            spec = rng[len("bytes="):]
            a, _, b = spec.partition("-")
            start = int(a or 0)
            end = int(b) if b else size - 1
            end = min(end, size - 1)
            status = 206
            if start >= size:
                # At or past EOF is an empty answer, not an error — the host
                # cannot always know the current size.
                self.send_response(206)
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
        length = max(0, end - start + 1)
        self.send_response(status)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Content-Length", str(length))
        if status == 206:
            self.send_header("Content-Range", f"bytes {start}-{end}/{size}")
        self.end_headers()
        with open(full, "rb") as f:
            f.seek(start)
            remaining = length
            while remaining > 0:
                chunk = f.read(min(1 << 20, remaining))
                if not chunk:
                    break
                self.wfile.write(chunk)
                remaining -= len(chunk)

    def watch(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        # ⚠ HTTP/1.1 + an open-ended body means chunked or close-delimited.
        # Announcing `Connection: close` keeps this simple and honest.
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.flush()  # ⚠ the host waits for these headers
        seen = 0
        while True:
            with EVENTS_CV:
                while len(EVENTS) <= seen:
                    EVENTS_CV.wait(timeout=15)
                    if len(EVENTS) <= seen:
                        break
                batch = EVENTS[seen:]
                seen = len(EVENTS)
            for ev in batch:
                try:
                    self.wfile.write(("data: " + json.dumps(ev) + "\n\n").encode())
                    self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    return

    def do_POST(self):
        if not self.authorised():
            return
        if self.path == "/v1/selftest":
            # A throwaway directory for filex's conformance probes. Without
            # this endpoint the plugin is registered UNVERIFIED and the checks
            # land on somebody's first real storage instead.
            root = tempfile.mkdtemp(prefix="diskfs-selftest-")
            with LOCK:
                NEXT_ID[0] += 1
                inst = "i%d" % NEXT_ID[0]
                INSTANCES[inst] = root
                SCRATCH.add(inst)
            return self.ok_json({"instance": inst})
        if self.path == "/v1/instances":
            body = self.read_json()
            root = (body.get("config") or {}).get("root") or ""
            if not root:
                return self.fail(400, "invalid", "root is required")
            if not os.path.isdir(root):
                return self.fail(400, "invalid", "no such directory: " + root)
            with LOCK:
                NEXT_ID[0] += 1
                inst = "i%d" % NEXT_ID[0]
                INSTANCES[inst] = root
            return self.ok_json({"instance": inst})

        inst, op, q = self.split()
        if inst not in INSTANCES:
            return self.fail(409, "no_instance", "unknown instance " + inst)
        if READ_ONLY:
            return self.fail(403, "read_only", "this plugin is read-only")
        try:
            body = self.read_json()
            if op == "delete":
                full = resolve(inst, body.get("path", ""))
                if os.path.isdir(full):
                    shutil.rmtree(full)
                elif os.path.exists(full):
                    os.remove(full)
                emit("delete", body.get("path", ""))
                return self.no_content()
            if op == "mkdir":
                os.makedirs(resolve(inst, body.get("path", "")), exist_ok=True)
                emit("create", body.get("path", ""))
                return self.no_content()
            if op in ("move", "copy"):
                src = resolve(inst, body.get("src", ""))
                dst = resolve(inst, body.get("dst", ""))
                os.makedirs(os.path.dirname(dst), exist_ok=True)
                if op == "move":
                    shutil.move(src, dst)
                    emit("move", body.get("dst", ""), body.get("src", ""))
                elif os.path.isdir(src):
                    shutil.copytree(src, dst, dirs_exist_ok=True)
                    emit("create", body.get("dst", ""))
                else:
                    shutil.copy2(src, dst)
                    emit("create", body.get("dst", ""))
                return self.no_content()
            if op.startswith("multipart/"):
                return self.multipart(inst, op, body)
            if op == "set-mtime":
                full = resolve(inst, body.get("path", ""))
                when = body.get("mtime", "")
                ts = datetime.fromisoformat(when.replace("Z", "+00:00")).timestamp()
                os.utime(full, (ts, ts))
                emit("modify", body.get("path", ""))
                return self.no_content()
        except FileNotFoundError:
            return self.fail(404, "not_found", str(body))
        except PermissionError as e:
            return self.fail(403, "read_only", str(e))
        return self.fail(404, "not_found", op)

    def multipart(self, inst, op, body):
        """Parts are files in a temp directory; complete concatenates them.

        ⚠ Assembled into a temp file and then MOVED into place, so a caller
        that fails half way leaves nothing at the destination — a half-written
        object under the real name is worse than no object at all.
        """
        if op == "multipart/init":
            up = "u%d" % (len(UPLOADS) + 1)
            UPLOADS[up] = {"path": body.get("path", ""), "dir": tempfile.mkdtemp(prefix="diskfs-mp-")}
            return self.ok_json({"upload_id": up, "part_urls": []})

        up = UPLOADS.get(body.get("upload_id", ""))
        if up is None:
            return self.fail(404, "not_found", "unknown upload")

        if op == "multipart/abort":
            shutil.rmtree(up["dir"], ignore_errors=True)
            UPLOADS.pop(body.get("upload_id", ""), None)
            return self.no_content()

        # complete
        parts = sorted(body.get("parts", []), key=lambda p: p.get("part_number", 0))
        full = resolve(inst, up["path"])
        os.makedirs(os.path.dirname(full), exist_ok=True)
        tmp = full + ".assembling"
        with open(tmp, "wb") as out:
            for p in parts:
                part_file = os.path.join(up["dir"], "part-%d" % p.get("part_number", 0))
                if not os.path.exists(part_file):
                    os.unlink(tmp)
                    return self.fail(400, "invalid", "missing part %s" % p.get("part_number"))
                with open(part_file, "rb") as src:
                    shutil.copyfileobj(src, out)
        os.replace(tmp, full)
        shutil.rmtree(up["dir"], ignore_errors=True)
        UPLOADS.pop(body.get("upload_id", ""), None)
        emit("create", up["path"])
        return self.no_content()

    def do_PUT(self):
        if not self.authorised():
            return
        inst, op, q = self.split()
        if inst not in INSTANCES:
            return self.fail(409, "no_instance", "unknown instance " + inst)
        if READ_ONLY:
            return self.fail(403, "read_only", "this plugin is read-only")
        if op == "multipart/part":
            up = UPLOADS.get(q.get("upload_id", ""))
            if up is None:
                return self.fail(404, "not_found", "unknown upload")
            n = int(q.get("part", "0"))
            dest = os.path.join(up["dir"], "part-%d" % n)
            with open(dest, "wb") as f:
                remaining = int(self.headers.get("Content-Length") or 0)
                while remaining > 0:
                    chunk = self.rfile.read(min(1 << 20, remaining))
                    if not chunk:
                        break
                    f.write(chunk)
                    remaining -= len(chunk)
            # The etag is whatever the plugin wants, as long as it comes back
            # in the complete call. A digest of the part is the obvious choice.
            with open(dest, "rb") as f:
                digest = hashlib.md5(f.read()).hexdigest()
            return self.ok_json({"etag": digest})
        if op != "write":
            return self.fail(404, "not_found", op)
        rel = q.get("path", "")
        full = resolve(inst, rel)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        # ⚠ X-Filex-Size is the length filex knows; -1 means "unknown", and
        # then the body is chunked. Both have to work: a 200 MB upload does
        # not fit in memory.
        declared = int(self.headers.get("X-Filex-Size") or -1)
        written = 0
        with open(full, "wb") as f:
            if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                while True:
                    line = self.rfile.readline().strip()
                    if not line:
                        continue
                    n = int(line.split(b";")[0], 16)
                    if n == 0:
                        self.rfile.readline()
                        break
                    f.write(self.rfile.read(n))
                    written += n
                    self.rfile.readline()
            else:
                remaining = int(self.headers.get("Content-Length") or 0)
                while remaining > 0:
                    chunk = self.rfile.read(min(1 << 20, remaining))
                    if not chunk:
                        break
                    f.write(chunk)
                    remaining -= len(chunk)
                    written += len(chunk)
        if declared >= 0 and written != declared:
            return self.fail(400, "invalid", f"got {written} bytes, header said {declared}")
        emit("create", rel)
        return self.no_content()

    def do_DELETE(self):
        if not self.authorised():
            return
        inst, op, _ = self.split()
        if not op:
            root = INSTANCES.pop(inst, None)
            # A self-test area is ours to discard; a real storage's directory
            # obviously is not.
            if inst in SCRATCH:
                SCRATCH.discard(inst)
                if root:
                    shutil.rmtree(root, ignore_errors=True)
            return self.no_content()
        return self.fail(404, "not_found", op)


class UnixServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    address_family = socket.AF_UNIX
    daemon_threads = True
    allow_reuse_address = True

    def server_bind(self):
        try:
            os.unlink(self.server_address)
        except OSError:
            pass
        socketserver.TCPServer.server_bind(self)
        os.chmod(self.server_address, 0o600)

    def get_request(self):
        conn, _ = self.socket.accept()
        return conn, ("unix", 0)


class TCPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main():
    if not TOKEN:
        sys.stderr.write("FILEX_PLUGIN_TOKEN is not set - filex starts this program, not you\n")
        sys.exit(1)
    if LISTEN:
        host, _, port = LISTEN.rpartition(":")
        srv = TCPServer((host or "127.0.0.1", int(port)), Handler)
        addr = "tcp:%s:%d" % (srv.server_address[0], srv.server_address[1])
    elif SOCKET_DIR and os.name != "nt":
        path = os.path.join(SOCKET_DIR, "diskfs-%d.sock" % os.getpid())
        srv = UnixServer(path, Handler)
        addr = "unix:" + path
    else:
        # No socket directory (Windows) - a loopback port of the kernel's
        # choosing. ⚠ Loopback only: the token is the only thing between the
        # world and the storage credentials filex sends, and filex refuses a
        # handshake that advertises anything else.
        srv = TCPServer(("127.0.0.1", 0), Handler)
        addr = "tcp:127.0.0.1:%d" % srv.server_address[1]
    # The handshake: one line, on stdout, flushed. Everything else this
    # program prints goes to stderr and lands in filex's log.
    print("FILEX-PLUGIN/%d %s" % (PROTOCOL, addr), flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
